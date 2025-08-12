/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	appsv1kube "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
	"github.com/Jonatanitsko/StatefulSingleton.git/internal/podutil"
)

const (
	phaseTransitioning = "Transitioning"
)

// StatefulSingletonReconciler reconciles a StatefulSingleton object
type StatefulSingletonReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=apps.statefulsingleton.com,resources=statefulsingletons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.statefulsingleton.com,resources=statefulsingletons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.statefulsingleton.com,resources=statefulsingletons/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=core,resources=replicationcontrollers,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps.openshift.io,resources=deploymentconfigs,verbs=get;list;watch

// Reconcile handles StatefulSingleton resources
func (r *StatefulSingletonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("controller")
	logger.Info("Reconciling StatefulSingleton", "request", req.NamespacedName)

	// Get StatefulSingleton resource
	var singleton appsv1.StatefulSingleton
	if err := r.Get(ctx, req.NamespacedName, &singleton); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted
			logger.Info("StatefulSingleton resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object
		logger.Error(err, "Failed to get StatefulSingleton")
		return ctrl.Result{}, err
	}

	// Ensure the wrapper ConfigMap exists
	if err := r.ensureWrapperConfigMap(ctx, req.Namespace); err != nil {
		logger.Error(err, "Failed to ensure wrapper ConfigMap")
		return ctrl.Result{}, err
	}

	// Get pods matching our selector
	pods, err := r.getPodsForStatefulSingleton(ctx, &singleton)
	if err != nil {
		logger.Error(err, "Failed to get pods for StatefulSingleton")
		return ctrl.Result{}, err
	}

	// Filter managed pods
	var managedPods []corev1.Pod
	for _, pod := range pods {
		if podutil.HasSingletonReadinessGate(&pod) {
			managedPods = append(managedPods, pod)
		}
	}

	// Sort pods by creation timestamp (newer pods last)
	sort.Slice(managedPods, func(i, j int) bool {
		return managedPods[i].CreationTimestamp.Before(&managedPods[j].CreationTimestamp)
	})

	logger.Info("Found managed pods", "count", len(managedPods))

	// Handle pod transitions
	result, err := r.handlePodTransitions(ctx, &singleton, managedPods, logger)
	if err != nil {
		logger.Error(err, "Error handling pod transitions")
		return result, err
	}

	return result, nil
}

// handlePodTransitions manages the transition between pods
func (r *StatefulSingletonReconciler) handlePodTransitions(
	ctx context.Context,
	singleton *appsv1.StatefulSingleton,
	pods []corev1.Pod,
	logger logr.Logger,
) (ctrl.Result, error) {
	// No pods found
	if len(pods) == 0 {
		logger.Info("No pods found for StatefulSingleton")

		// Update status to reflect no active pods
		return r.updateStatus(ctx, singleton, "", "Running", "No pods found")
	}

	// Single pod case - ensure it's running and has our signal for future activity.
	if len(pods) == 1 {
		pod := &pods[0]

		// Check if pod has start signal
		hasSignal, err := podutil.HasStartSignal(ctx, r.Client, pod)
		if err != nil {
			logger.Error(err, "Failed to check start signal", "pod", pod.Name)
			return ctrl.Result{}, err
		}

		if !hasSignal {
			// Check if pod is running before creating start signal
			if !podutil.IsPodRunning(pod) {
				logger.Info("Pod not running yet, requeuing", "pod", pod.Name, "phase", pod.Status.Phase)
				return ctrl.Result{RequeueAfter: time.Second * 5}, nil
			}

			logger.Info("Creating start signal for single pod", "pod", pod.Name)

			// Create signal file
			if err := podutil.CreateStartSignalFile(ctx, r.Client, pod); err != nil {
				logger.Error(err, "Failed to create start signal", "pod", pod.Name)
				return ctrl.Result{}, err
			}

			// Update readiness condition
			if err := podutil.UpdateReadinessCondition(ctx, r.Client, pod, true, "Pod ready"); err != nil {
				logger.Error(err, "Failed to update readiness condition", "pod", pod.Name)
				return ctrl.Result{}, err
			}

			// Record event
			r.Recorder.Event(singleton, corev1.EventTypeNormal, "PodStarted",
				fmt.Sprintf("Started pod %s", pod.Name))
		}

		// Update status
		return r.updateStatus(ctx, singleton, pod.Name, "Running", "Pod running")
	}

	// Multiple pods - handle transition
	oldPod := &pods[0]
	newPod := &pods[len(pods)-1]

	logger.Info("Handling pod transition", "oldPod", oldPod.Name, "newPod", newPod.Name)

	// Check if transition timeout has been exceeded
	if result, err := r.checkTransitionTimeout(ctx, singleton, oldPod, newPod, logger); result != nil {
		return *result, err
	}

	// Check if old pod still exists
	oldPodExists := true
	var currentOldPod corev1.Pod
	err := r.Get(ctx, types.NamespacedName{Name: oldPod.Name, Namespace: oldPod.Namespace}, &currentOldPod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Old pod is completely gone
			oldPodExists = false
		} else {
			// Error occurred
			return ctrl.Result{}, err
		}
	}

	// If old pod exists and is terminating, check if we need to respect grace period
	if result, err := r.handleGracePeriod(ctx, singleton, oldPod, newPod, &currentOldPod, oldPodExists, logger); result != nil {
		return *result, err
	}

	// If old pod still exists, we must wait even after grace period. We do handle deployments with max surge neq to 0.
	if oldPodExists {
		// Check if old pod is in a failed state (CrashLoopBackOff, Failed, etc.)
		oldPodFailed := podutil.IsPodFailed(&currentOldPod)

		// Check if old pod is terminating
		oldPodTerminating := podutil.IsPodTerminating(&currentOldPod)

		if oldPodFailed {
			logger.Info("Old pod is in failed state, proceeding with transition",
				"pod", currentOldPod.Name, "phase", currentOldPod.Status.Phase)

			// Don't wait for failed pods - proceed with transition
		} else if !oldPodTerminating {
			// Check if this is a RollingUpdate deployment with MaxSurge > 0
			// which requires controlled termination to break the deadlock
			logger.Info("Old pod is not terminating, checking deployment strategy", "oldPod", currentOldPod.Name)
			isRollingUpdateWithSurge, err := r.isRollingUpdateWithSurge(ctx, oldPod)
			if err != nil {
				logger.Error(err, "Failed to check deployment strategy")
				return ctrl.Result{}, err
			}

			logger.Info("Deployment strategy check result", "isRollingUpdateWithSurge", isRollingUpdateWithSurge)

			if isRollingUpdateWithSurge {
				// Rolling update with surge detected: scale down old ReplicaSet to break deadlock
				logger.Info("RollingUpdate with MaxSurge > 0 detected, scaling down old ReplicaSet",
					"oldPod", currentOldPod.Name, "newPod", newPod.Name)

				// Find and scale down the old ReplicaSet
				err := r.scaleDownOldReplicaSet(ctx, oldPod, logger)
				if err != nil {
					logger.Error(err, "Failed to scale down old ReplicaSet", "oldPod", currentOldPod.Name)
					return ctrl.Result{}, err
				}

				// Keep new pod not ready until old pod is completely gone
				if err := podutil.UpdateReadinessCondition(ctx, r.Client, newPod, false,
					"Old ReplicaSet scaling down (controlled termination)"); err != nil {
					return ctrl.Result{}, err
				}

				// Update status
				return r.updateStatus(ctx, singleton, oldPod.Name, phaseTransitioning,
					fmt.Sprintf("Scaled down old ReplicaSet for pod %s to break RollingUpdate deadlock",
						oldPod.Name))
			} else {
				// Non-surge deployment or Recreate strategy - wait for natural termination
				logger.Info("Non-surge deployment detected, waiting for natural pod lifecycle",
					"oldPod", currentOldPod.Name, "newPod", newPod.Name)

				// Keep new pod in not-ready state for healthy pods
				if err := podutil.UpdateReadinessCondition(ctx, r.Client, newPod, false,
					"Waiting for old pod to begin natural termination"); err != nil {
					return ctrl.Result{}, err
				}

				// Update status
				return r.updateStatus(ctx, singleton, oldPod.Name, phaseTransitioning,
					fmt.Sprintf("Waiting for pod %s to begin natural termination",
						oldPod.Name))
			}
		} else {
			// Old pod is terminating - wait for complete termination
			logger.Info("Old pod is terminating, waiting for complete termination",
				"oldPod", currentOldPod.Name, "newPod", newPod.Name)

			// Keep new pod not ready until old pod is completely gone
			if err := podutil.UpdateReadinessCondition(ctx, r.Client, newPod, false,
				"Waiting for old pod to completely terminate"); err != nil {
				return ctrl.Result{}, err
			}

			// Update status
			return r.updateStatus(ctx, singleton, oldPod.Name, phaseTransitioning,
				fmt.Sprintf("Waiting for pod %s to completely terminate",
					oldPod.Name))
		}
	}

	// Old pod is completely gone, signal the new pod to start
	hasSignal, err := podutil.HasStartSignal(ctx, r.Client, newPod)
	if err != nil {
		logger.Error(err, "Failed to check start signal", "pod", newPod.Name)
		return ctrl.Result{}, err
	}

	if !hasSignal {
		logger.Info("Old pod fully terminated, signaling new pod to start", "newPod", newPod.Name)

		// Create signal file
		if err := podutil.CreateStartSignalFile(ctx, r.Client, newPod); err != nil {
			logger.Error(err, "Failed to create start signal", "pod", newPod.Name)
			return ctrl.Result{}, err
		}

		// Update readiness condition
		if err := podutil.UpdateReadinessCondition(ctx, r.Client, newPod, true,
			"Starting new pod"); err != nil {
			logger.Error(err, "Failed to update readiness condition", "pod", newPod.Name)
			return ctrl.Result{}, err
		}

		// Record event
		r.Recorder.Event(singleton, corev1.EventTypeNormal, "PodTransition",
			fmt.Sprintf("Old pod terminated, starting new pod %s", newPod.Name))
	}

	// Update status to reflect running state
	return r.updateStatus(ctx, singleton, newPod.Name, "Running",
		fmt.Sprintf("Pod %s is running", newPod.Name))
}

// getPodsForStatefulSingleton returns pods managed by this StatefulSingleton
func (r *StatefulSingletonReconciler) getPodsForStatefulSingleton(
	ctx context.Context,
	singleton *appsv1.StatefulSingleton,
) ([]corev1.Pod, error) {
	var podList corev1.PodList

	selector, err := metav1.LabelSelectorAsSelector(&singleton.Spec.Selector)
	if err != nil {
		return nil, err
	}

	listOpts := &client.ListOptions{
		Namespace:     singleton.Namespace,
		LabelSelector: selector,
	}

	if err := r.List(ctx, &podList, listOpts); err != nil {
		return nil, err
	}

	return podList.Items, nil
}

// checkTransitionTimeout checks if transition has exceeded timeout and returns result if timeout occurred
func (r *StatefulSingletonReconciler) checkTransitionTimeout(
	ctx context.Context,
	singleton *appsv1.StatefulSingleton,
	oldPod, newPod *corev1.Pod,
	logger logr.Logger,
) (*ctrl.Result, error) {
	// Check if transition is already in progress
	isTransitioning := singleton.Status.Phase == phaseTransitioning
	if isTransitioning && singleton.Status.TransitionTimestamp != nil {
		// Calculate how long we've been transitioning
		transitionDuration := time.Since(singleton.Status.TransitionTimestamp.Time)
		maxDuration := time.Duration(singleton.Spec.MaxTransitionTime) * time.Second

		// Check if we've exceeded the maximum transition time
		if transitionDuration > maxDuration {
			logger.Info("Transition exceeded max duration",
				"duration", transitionDuration.Seconds(),
				"maxDuration", maxDuration.Seconds())

			// Log warning event
			r.Recorder.Event(singleton, corev1.EventTypeWarning, "TransitionTimeout",
				fmt.Sprintf("Transition from %s to %s exceeded timeout of %d seconds",
					oldPod.Name, newPod.Name, singleton.Spec.MaxTransitionTime))

			// Update status to reflect transition timeout
			result, err := r.updateStatus(ctx, singleton, oldPod.Name, phaseTransitioning,
				fmt.Sprintf("Transition from %s to %s exceeded transition timeout of %d seconds",
					oldPod.Name, newPod.Name, singleton.Spec.MaxTransitionTime))
			return &result, err
		}
	}

	return nil, nil
}

// handleGracePeriod handles grace period logic for terminating pods
func (r *StatefulSingletonReconciler) handleGracePeriod(
	ctx context.Context,
	singleton *appsv1.StatefulSingleton,
	oldPod, newPod, currentOldPod *corev1.Pod,
	oldPodExists bool,
	logger logr.Logger,
) (*ctrl.Result, error) {
	// If old pod exists and is terminating, check if we need to respect grace period
	if oldPodExists && podutil.IsPodTerminating(currentOldPod) {
		// Get the effective grace period
		gracePeriod := podutil.GetEffectiveGracePeriod(
			currentOldPod,
			singleton.Spec.TerminationGracePeriod,
			singleton.Spec.RespectPodGracePeriod,
		)

		// Calculate how long the pod has been terminating
		if currentOldPod.DeletionTimestamp != nil {
			terminatingDuration := time.Since(currentOldPod.DeletionTimestamp.Time)

			logger.Info("Old pod terminating",
				"pod", currentOldPod.Name,
				"terminatingFor", terminatingDuration.Seconds(),
				"gracePeriod", gracePeriod)

			// If still within grace period, wait
			if terminatingDuration.Seconds() < float64(gracePeriod) {
				// Keep new pod in not-ready state
				if err := podutil.UpdateReadinessCondition(ctx, r.Client, newPod, false,
					fmt.Sprintf("Waiting for old pod to terminate (grace period: %ds, elapsed: %.1fs)",
						gracePeriod, terminatingDuration.Seconds())); err != nil {
					return nil, err
				}

				// Update status
				result, err := r.updateStatus(ctx, singleton, oldPod.Name, phaseTransitioning,
					fmt.Sprintf("Respecting grace period: waiting for pod %s to terminate (%.1fs of %ds)",
						oldPod.Name, terminatingDuration.Seconds(), gracePeriod))
				return &result, err
			}
		}
	}

	return nil, nil
}

// ensureWrapperConfigMap creates or updates the wrapper script ConfigMap
func (r *StatefulSingletonReconciler) ensureWrapperConfigMap(ctx context.Context, namespace string) error {
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: "statefulsingleton-wrapper", Namespace: namespace}, configMap)

	// ConfigMap wrapper script content with enhanced Dockerfile ENTRYPOINT/CMD discovery
	wrapperScript := `#!/bin/sh
set -e

echo "StatefulSingleton: Container starting, waiting for signal before executing application..."

# Wait for signal file with optimized polling to minimize startup delay
while [ ! -f /var/run/signal/start-signal ]; do
  sleep 0.05
done

echo "StatefulSingleton: Start signal received, executing application"

# Parse the captured entrypoint information
if [ -n "$ORIGINAL_ENTRYPOINT" ]; then
  echo "StatefulSingleton: DEBUG - ORIGINAL_ENTRYPOINT: $ORIGINAL_ENTRYPOINT"
  ENTRYPOINT_JSON="$ORIGINAL_ENTRYPOINT"
  
  # Extract command and args arrays from JSON, handling quoted strings properly
  COMMAND_ARRAY=$(echo "$ENTRYPOINT_JSON" | grep -o '"command":\[[^]]*\]' | sed 's/"command":\[//;s/\]$//')
  ARGS_ARRAY=$(echo "$ENTRYPOINT_JSON" | grep -o '"args":\[[^]]*\]' | sed 's/"args":\[//;s/\]$//')
  
  echo "StatefulSingleton: DEBUG - COMMAND_ARRAY: '$COMMAND_ARRAY'"
  echo "StatefulSingleton: DEBUG - ARGS_ARRAY: '$ARGS_ARRAY'"
  
  # Convert JSON arrays to shell arguments, preserving quoted strings
  if [ -n "$COMMAND_ARRAY" ] && [ "$COMMAND_ARRAY" != "null" ]; then
    COMMAND=$(echo "$COMMAND_ARRAY" | sed 's/,/ /g' | sed 's/"//g')
  else
    COMMAND=""
  fi
  
  if [ -n "$ARGS_ARRAY" ] && [ "$ARGS_ARRAY" != "null" ]; then
    ARGS=$(echo "$ARGS_ARRAY" | sed 's/,/ /g' | sed 's/"//g')
  else
    ARGS=""
  fi
  
  echo "StatefulSingleton: DEBUG - Final COMMAND: '$COMMAND'"
  echo "StatefulSingleton: DEBUG - Final ARGS: '$ARGS'"
  
  # Execute based on what was captured from the pod spec
  if [ -n "$COMMAND" ]; then
    echo "StatefulSingleton: DEBUG - Taking COMMAND path"
    if [ -n "$ARGS" ]; then
      echo "StatefulSingleton: Executing captured command with args: $COMMAND $ARGS"
      echo "StatefulSingleton: DEBUG - About to exec: sh -c \"$COMMAND $ARGS\""
      exec sh -c "$COMMAND $ARGS"
    else
      echo "StatefulSingleton: Executing captured command: $COMMAND"
      echo "StatefulSingleton: DEBUG - About to exec: sh -c \"$COMMAND\""
      exec sh -c "$COMMAND"
    fi
  elif [ -n "$ARGS" ]; then
    echo "StatefulSingleton: DEBUG - Taking ARGS-only path"
    echo "StatefulSingleton: Executing captured args: $ARGS"
    echo "StatefulSingleton: DEBUG - About to exec: sh -c \"$ARGS\""
    exec sh -c "$ARGS"
  else
    echo "StatefulSingleton: DEBUG - Taking discovery path (no command/args)"
    # Both command and args were empty - container relies on Dockerfile ENTRYPOINT/CMD
    echo "StatefulSingleton: No explicit command/args, attempting to discover image entrypoint"
    
    # Strategy 1: Try to find common entrypoint script locations
    if [ -f "/docker-entrypoint.sh" ]; then
      echo "StatefulSingleton: Found /docker-entrypoint.sh, executing..."
      echo "StatefulSingleton: DEBUG - About to exec: /docker-entrypoint.sh (with no args)"
      echo "StatefulSingleton: DEBUG - This is likely the problem - entrypoint needs args!"
      exec /docker-entrypoint.sh
    elif [ -f "/entrypoint.sh" ]; then
      echo "StatefulSingleton: Found /entrypoint.sh, executing..."
      exec /entrypoint.sh
    elif [ -f "/usr/local/bin/docker-entrypoint.sh" ]; then
      echo "StatefulSingleton: Found /usr/local/bin/docker-entrypoint.sh, executing..."
      exec /usr/local/bin/docker-entrypoint.sh
    elif [ -f "/app/entrypoint.sh" ]; then
      echo "StatefulSingleton: Found /app/entrypoint.sh, executing..."
      exec /app/entrypoint.sh
    
    # Strategy 2: Try to detect common application patterns
    elif command -v java >/dev/null 2>&1; then
      # Java application detection
      JAR_FILE=""
      for location in "/app" "/" "/opt/app" "/usr/app"; do
        if [ -d "$location" ]; then
          JAR_FILE=$(find "$location" -maxdepth 2 -name "*.jar" 2>/dev/null | head -1)
          if [ -n "$JAR_FILE" ]; then
            break
          fi
        fi
      done
      
      if [ -n "$JAR_FILE" ]; then
        echo "StatefulSingleton: Found Java application: $JAR_FILE"
        exec java -jar "$JAR_FILE"
      fi
    
    elif command -v node >/dev/null 2>&1; then
      # Node.js application detection
      for location in "/app" "/" "/usr/app" "/opt/app"; do
        if [ -f "$location/package.json" ]; then
          echo "StatefulSingleton: Found Node.js application in $location"
          cd "$location"
          if grep -q '"start"' package.json; then
            exec npm start
          elif [ -f "index.js" ]; then
            exec node index.js
          elif [ -f "server.js" ]; then
            exec node server.js
          elif [ -f "app.js" ]; then
            exec node app.js
          fi
          break
        fi
      done
    
    elif command -v python3 >/dev/null 2>&1 || command -v python >/dev/null 2>&1; then
      # Python application detection
      PYTHON_CMD="python3"
      if ! command -v python3 >/dev/null 2>&1; then
        PYTHON_CMD="python"
      fi
      
      for location in "/app" "/" "/usr/app" "/opt/app"; do
        if [ -f "$location/main.py" ]; then
          echo "StatefulSingleton: Found Python application: $location/main.py"
          exec $PYTHON_CMD "$location/main.py"
        elif [ -f "$location/app.py" ]; then
          echo "StatefulSingleton: Found Python application: $location/app.py"
          exec $PYTHON_CMD "$location/app.py"
        elif [ -f "$location/server.py" ]; then
          echo "StatefulSingleton: Found Python application: $location/server.py"
          exec $PYTHON_CMD "$location/server.py"
        fi
      done
    
    elif command -v go >/dev/null 2>&1; then
      # Go application detection (compiled binaries)
      for location in "/app/main" "/app/app" "/usr/local/bin/app" "/opt/app/app"; do
        if [ -f "$location" ] && [ -x "$location" ]; then
          echo "StatefulSingleton: Found Go application: $location"
          exec "$location"
        fi
      done
    fi
    
    # Strategy 3: Try common binary locations and names
    for binary in "/usr/local/bin/app" "/app/app" "/bin/app" "/opt/app/app" "/usr/bin/app"; do
      if [ -f "$binary" ] && [ -x "$binary" ]; then
        echo "StatefulSingleton: Found executable application: $binary"
        exec "$binary"
      fi
    done
    
    # Strategy 4: Look for any executable in common app directories
    for dir in "/app" "/usr/app" "/opt/app"; do
      if [ -d "$dir" ]; then
        # Find the first executable file that's not a system binary
        APP_BINARY=$(find "$dir" -maxdepth 2 -type f -executable 2>/dev/null | grep -v -E "(\.sh$|/bin/|/usr/)" | head -1)
        if [ -n "$APP_BINARY" ]; then
          echo "StatefulSingleton: Found executable in app directory: $APP_BINARY"
          exec "$APP_BINARY"
        fi
      fi
    done
    
    # Final fallback - log warning and provide shell access for debugging
    echo "StatefulSingleton: DEBUG - Reached final fallback - no entrypoint discovered"
    echo "StatefulSingleton: WARNING - Could not automatically determine application entrypoint"
    echo "StatefulSingleton: This usually means the container relies on a Dockerfile ENTRYPOINT/CMD"
    echo "StatefulSingleton: that could not be automatically discovered."
    echo "StatefulSingleton: Please check the container image documentation or consider"
    echo "StatefulSingleton: specifying explicit command/args in your pod specification."
    echo "StatefulSingleton: Starting shell for manual investigation..."
    echo "StatefulSingleton: DEBUG - About to exec: /bin/sh"
    exec /bin/sh
  fi
else
  # Fallback if somehow ORIGINAL_ENTRYPOINT wasn't set (should never happen)
  echo "StatefulSingleton: DEBUG - ORIGINAL_ENTRYPOINT is empty or unset!"
  echo "StatefulSingleton: ERROR - No entrypoint information available"
  echo "StatefulSingleton: This indicates a problem with the operator setup"
  echo "StatefulSingleton: DEBUG - About to exec: /bin/sh (emergency fallback)"
  exec /bin/sh
fi`

	if errors.IsNotFound(err) {
		// Create the ConfigMap
		newConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "statefulsingleton-wrapper",
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/part-of":    "statefulsingleton-operator",
					"app.kubernetes.io/managed-by": "statefulsingleton-controller",
				},
			},
			Data: map[string]string{
				"entrypoint-wrapper.sh": wrapperScript,
			},
		}

		return r.Create(ctx, newConfigMap)
	} else if err != nil {
		return err
	}

	// ConfigMap exists, check if it needs updating
	if configMap.Data["entrypoint-wrapper.sh"] != wrapperScript {
		// Update the script
		configMap.Data["entrypoint-wrapper.sh"] = wrapperScript

		// Ensure the labels exist
		if configMap.Labels == nil {
			configMap.Labels = make(map[string]string)
		}
		configMap.Labels["app.kubernetes.io/part-of"] = "statefulsingleton-operator"
		configMap.Labels["app.kubernetes.io/managed-by"] = "statefulsingleton-controller"

		return r.Update(ctx, configMap)
	}

	return nil
}

// updateStatus updates the StatefulSingleton status with retry logic for conflicts
func (r *StatefulSingletonReconciler) updateStatus(
	ctx context.Context,
	singleton *appsv1.StatefulSingleton,
	activePod string,
	phase string,
	message string,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("controller")

	// Check if status has changed
	if singleton.Status.ActivePod == activePod &&
		singleton.Status.Phase == phase &&
		singleton.Status.Message == message {
		// No changes needed
		logger.Info("Status unchanged, no update needed",
			"activePod", activePod, "phase", phase, "message", message)

		// Determine requeue interval based on state
		if phase == phaseTransitioning {
			// During transitions, reconcile more frequently
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	logger.Info("Performing status update",
		"currentActivePod", singleton.Status.ActivePod, "newActivePod", activePod,
		"currentPhase", singleton.Status.Phase, "newPhase", phase,
		"currentMessage", singleton.Status.Message, "newMessage", message)

	// Always update status timestamp for any status change
	now := metav1.Now()
	singleton.Status.StatusTimestamp = &now
	logger.Info("Setting status timestamp", "timestamp", now)

	// Handle transition timestamp lifecycle - only for pod-to-pod transitions
	if phase == phaseTransitioning {
		// Check if we're entering a new transition or if we are in the middle of one
		if singleton.Status.Phase != phaseTransitioning {
			// New transition - set timestamp
			singleton.Status.TransitionTimestamp = &now
			logger.Info("Setting transition timestamp for new transition", "timestamp", now)
		} else if singleton.Status.TransitionTimestamp == nil {
			// Ongoing transition but missing timestamp (shouldn't happen, but handle it)
			singleton.Status.TransitionTimestamp = &now
			logger.Info("Setting missing transition timestamp for ongoing transition", "timestamp", now)
		}
		// If ongoing transition with existing timestamp, keep timestamp
	} else {
		// Clear timestamp when exiting transition phase
		singleton.Status.TransitionTimestamp = nil
		logger.Info("Clearing transition timestamp - exiting transition phase")
	}

	// Update status fields
	singleton.Status.ActivePod = activePod
	singleton.Status.Phase = phase
	singleton.Status.Message = message

	// Update the status
	logger.Info("Attempting status update", "resourceVersion", singleton.ResourceVersion)
	err := r.Status().Update(ctx, singleton)
	if err != nil {
		logger.Error(err, "Status update failed")
		return ctrl.Result{}, err
	}

	logger.Info("Status updated successfully", "activePod", activePod, "phase", phase)

	// Determine requeue interval based on state
	if phase == phaseTransitioning {
		// During transitions, reconcile more frequently
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// findObjectsForPod maps pods to StatefulSingleton resources
func (r *StatefulSingletonReconciler) findObjectsForPod(ctx context.Context, pod client.Object) []reconcile.Request {
	// List all StatefulSingleton resources in the pod's namespace
	var singletonList appsv1.StatefulSingletonList
	err := r.List(ctx, &singletonList,
		client.InNamespace(pod.GetNamespace()))
	if err != nil {
		return nil
	}

	// Find StatefulSingletons that manage this pod
	var requests []reconcile.Request
	for _, singleton := range singletonList.Items {
		selector, err := metav1.LabelSelectorAsSelector(&singleton.Spec.Selector)
		if err != nil {
			continue
		}

		if selector.Matches(labels.Set(pod.GetLabels())) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      singleton.Name,
					Namespace: singleton.Namespace,
				},
			})
		}
	}

	return requests
}

// scaleDownOldReplicaSet scales down the ReplicaSet that owns the old pod to 0 replicas
func (r *StatefulSingletonReconciler) scaleDownOldReplicaSet(ctx context.Context, oldPod *corev1.Pod, logger logr.Logger) error {
	// Find the ReplicaSet that owns the old pod
	for _, ownerRef := range oldPod.OwnerReferences {
		if ownerRef.Kind == "ReplicaSet" {
			// Get the ReplicaSet
			var rs appsv1kube.ReplicaSet
			if err := r.Get(ctx, types.NamespacedName{
				Name:      ownerRef.Name,
				Namespace: oldPod.Namespace,
			}, &rs); err != nil {
				return fmt.Errorf("failed to get ReplicaSet %s: %w", ownerRef.Name, err)
			}

			// Check if already scaled down
			if rs.Spec.Replicas != nil && *rs.Spec.Replicas == 0 {
				logger.Info("ReplicaSet already scaled down", "replicaSet", rs.Name)
				return nil
			}

			// Scale down to 0
			zero := int32(0)
			rs.Spec.Replicas = &zero

			if err := r.Update(ctx, &rs); err != nil {
				return fmt.Errorf("failed to scale down ReplicaSet %s: %w", rs.Name, err)
			}

			logger.Info("Successfully scaled down ReplicaSet", "replicaSet", rs.Name, "oldReplicas", rs.Status.Replicas, "newReplicas", 0)
			return nil
		}
	}

	return fmt.Errorf("no ReplicaSet owner found for pod %s", oldPod.Name)
}

// isRollingUpdateWithSurge checks if the pods belong to a RollingUpdate deployment with MaxSurge > 0
func (r *StatefulSingletonReconciler) isRollingUpdateWithSurge(ctx context.Context, oldPod *corev1.Pod) (bool, error) {
	logger := log.FromContext(ctx).WithName("controller")
	logger.Info("Checking if pod belongs to RollingUpdate deployment with surge", "pod", oldPod.Name)

	// Find the deployment that owns these pods by looking at owner references
	var deployment *appsv1kube.Deployment

	// Check old pod's owner references
	logger.Info("Checking pod owner references", "ownerRefs", len(oldPod.OwnerReferences))
	for _, ownerRef := range oldPod.OwnerReferences {
		logger.Info("Found owner reference", "kind", ownerRef.Kind, "name", ownerRef.Name)
		if ownerRef.Kind == "ReplicaSet" {
			// Get the ReplicaSet
			var rs appsv1kube.ReplicaSet
			if err := r.Get(ctx, types.NamespacedName{
				Name:      ownerRef.Name,
				Namespace: oldPod.Namespace,
			}, &rs); err != nil {
				logger.Error(err, "Failed to get ReplicaSet", "replicaSet", ownerRef.Name)
				continue // Try next owner reference
			}

			logger.Info("Found ReplicaSet, checking its owner references", "replicaSet", rs.Name, "ownerRefs", len(rs.OwnerReferences))
			// Check ReplicaSet's owner references for Deployment
			for _, rsOwnerRef := range rs.OwnerReferences {
				logger.Info("Found ReplicaSet owner reference", "kind", rsOwnerRef.Kind, "name", rsOwnerRef.Name)
				if rsOwnerRef.Kind == "Deployment" {
					var dep appsv1kube.Deployment
					if err := r.Get(ctx, types.NamespacedName{
						Name:      rsOwnerRef.Name,
						Namespace: oldPod.Namespace,
					}, &dep); err == nil {
						deployment = &dep
						logger.Info("Found Deployment", "deployment", dep.Name)
						break
					} else {
						logger.Error(err, "Failed to get Deployment", "deployment", rsOwnerRef.Name)
					}
				}
			}
			if deployment != nil {
				break
			}
		}
	}

	if deployment == nil {
		// Could not find deployment - assume not a rolling update with surge
		logger.Info("Could not find deployment for pod", "pod", oldPod.Name)
		return false, nil
	}

	logger.Info("Checking deployment strategy", "deployment", deployment.Name, "strategyType", deployment.Spec.Strategy.Type)

	// Check if deployment uses RollingUpdate strategy
	if deployment.Spec.Strategy.Type != appsv1kube.RollingUpdateDeploymentStrategyType {
		logger.Info("Deployment does not use RollingUpdate strategy", "strategyType", deployment.Spec.Strategy.Type)
		return false, nil
	}

	// Check if MaxSurge > 0
	rollingUpdate := deployment.Spec.Strategy.RollingUpdate
	if rollingUpdate == nil {
		// Default RollingUpdate settings have MaxSurge > 0
		logger.Info("RollingUpdate settings are nil, using defaults (MaxSurge > 0)")
		return true, nil
	}

	if rollingUpdate.MaxSurge == nil {
		// Default MaxSurge is 25%, which means surge is allowed
		logger.Info("MaxSurge is nil, using default (25% > 0)")
		return true, nil
	}

	// Check if MaxSurge > 0
	maxSurge, err := intstr.GetValueFromIntOrPercent(rollingUpdate.MaxSurge, int(*deployment.Spec.Replicas), true)
	if err != nil {
		logger.Error(err, "Failed to calculate MaxSurge value")
		return false, err
	}

	logger.Info("Calculated MaxSurge value", "maxSurge", maxSurge, "replicas", *deployment.Spec.Replicas)
	return maxSurge > 0, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StatefulSingletonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSingleton{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				return r.findObjectsForPod(ctx, obj)
			}),
		).
		Complete(r)
}
