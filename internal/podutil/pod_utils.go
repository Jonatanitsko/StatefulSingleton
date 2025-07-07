// Package podutil provides utility functions for managing pods
package podutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// HasSingletonReadinessGate checks if a pod has our readiness gate
func HasSingletonReadinessGate(pod *corev1.Pod) bool {
	for _, gate := range pod.Spec.ReadinessGates {
		if gate.ConditionType == "apps.statefulsingleton.com/singleton-ready" {
			return true
		}
	}
	return false
}

// IsPodRunning checks if a pod is in the Running phase
func IsPodRunning(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodRunning
}

// IsPodTerminating checks if a pod is being terminated
func IsPodTerminating(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp != nil
}

// IsPodReady checks if a pod is ready according to its conditions
func IsPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// isTestEnvironment detects if we're running in envtest
func isTestEnvironment() bool {
	for _, arg := range os.Args {
		if strings.Contains(arg, "test") {
			return true
		}
	}
	return false
}

// HasStartSignal checks if a pod has been signaled to start
func HasStartSignal(ctx context.Context, c client.Client, pod *corev1.Pod) (bool, error) {
	if isTestEnvironment() {
		var currentPod corev1.Pod
		if err := c.Get(ctx, client.ObjectKey{Name: pod.Name, Namespace: pod.Namespace}, &currentPod); err != nil {
			return false, err
		}

		if currentPod.Annotations == nil {
			return false, nil
		}
		_, hasSignal := currentPod.Annotations["test.statefulsingleton.com/has-start-signal"]
		return hasSignal, nil

	}

	// Skip if pod is not running
	if !IsPodRunning(pod) {
		return false, nil
	}

	// Skip if pod is terminating
	if IsPodTerminating(pod) {
		return false, nil
	}

	// Create exec command to check for signal file
	restConfig, err := config.GetConfig()
	if err != nil {
		return false, fmt.Errorf("failed to get rest config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return false, fmt.Errorf("failed to create clientset: %v", err)
	}

	// Find the status sidecar container
	containerName := "status-sidecar"
	containerExists := false
	for _, container := range pod.Spec.Containers {
		if container.Name == containerName {
			containerExists = true
			break
		}
	}

	if !containerExists {
		return false, fmt.Errorf("status-sidecar container not found in pod %s", pod.Name)
	}

	// Check if the status checking container is ready
	containerReady := false
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == containerName && status.Ready {
			containerReady = true
			break
		}
	}

	if !containerReady {
		return false, nil
	}

	// Create exec request to check for file inside status container
	execRequest := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   []string{"ls", "-la", "/var/run/signal/start-signal"},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	// Create SPDY connection to create an executor to run inside commands inside containers.
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", execRequest.URL())
	if err != nil {
		return false, fmt.Errorf("failed to create executor: %v", err)
	}

	// Execute the command inside the sidecar container
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	// File exists if command succeeds and there's output
	return err == nil && stdout.Len() > 0, nil
}

// CreateStartSignalFile creates the start signal file in a pod using a dedicated sidecar-status container
func CreateStartSignalFile(ctx context.Context, c client.Client, pod *corev1.Pod) error {
	if isTestEnvironment() {
		var currentPod corev1.Pod
		if err := c.Get(ctx, client.ObjectKey{Name: pod.Name, Namespace: pod.Namespace}, &currentPod); err != nil {
			return err
		}

		// Set the specific start signal annotation
		currentPod.Annotations["test.statefulsingleton.com/has-start-signal"] = "true"
		return c.Update(ctx, &currentPod)
	}

	// Skip if pod is not running
	if !IsPodRunning(pod) {
		return fmt.Errorf("pod %s is not running", pod.Name)
	}

	// Skip if pod is terminating
	if IsPodTerminating(pod) {
		return fmt.Errorf("pod %s is terminating", pod.Name)
	}

	// Create exec command to create signal file
	restConfig, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get rest config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %v", err)
	}

	// Find the status sidecar container
	containerName := "status-sidecar"
	containerExists := false
	for _, container := range pod.Spec.Containers {
		if container.Name == containerName {
			containerExists = true
			break
		}
	}

	if !containerExists {
		return fmt.Errorf("status-sidecar container not found in pod %s", pod.Name)
	}

	// Create exec request to create file
	execRequest := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   []string{"sh", "-c", "mkdir -p /var/run/signal && touch /var/run/signal/start-signal"},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	// Create SPDY connection to create an executor to run inside commands inside containers.
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", execRequest.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %v", err)
	}

	// Execute the command
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		return fmt.Errorf("failed to execute command: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

// UpdateReadinessCondition updates the pod's readiness condition
func UpdateReadinessCondition(ctx context.Context, c client.Client, pod *corev1.Pod, ready bool, message string) error {
	// Get current pod to avoid update conflicts
	var currentPod corev1.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: pod.Name}, &currentPod); err != nil {
		return err
	}

	// Skip if pod is terminating
	if IsPodTerminating(&currentPod) {
		return nil
	}

	// Find or create condition
	var conditionType corev1.PodConditionType = "apps.statefulsingleton.com/singleton-ready"
	statusValue := corev1.ConditionFalse
	if ready {
		statusValue = corev1.ConditionTrue
	}

	// Update conditions
	found := false
	for i, condition := range currentPod.Status.Conditions {
		if condition.Type == conditionType {
			if condition.Status != statusValue || condition.Message != message {
				currentPod.Status.Conditions[i].Status = statusValue
				currentPod.Status.Conditions[i].Message = message
				currentPod.Status.Conditions[i].LastTransitionTime = metav1.Now()
			}
			found = true
			break
		}
	}

	if !found {
		currentPod.Status.Conditions = append(currentPod.Status.Conditions, corev1.PodCondition{
			Type:               conditionType,
			Status:             statusValue,
			LastTransitionTime: metav1.Now(),
			Message:            message,
		})
	}

	// Update pod status
	return c.Status().Update(ctx, &currentPod)
}

// GetEffectiveGracePeriod returns the effective termination grace period
func GetEffectiveGracePeriod(pod *corev1.Pod, specifiedGracePeriod int, respectPodGracePeriod bool) int64 {
	// Default grace period
	var gracePeriod int64 = 30

	// Use pod's grace period if specified
	if pod.Spec.TerminationGracePeriodSeconds != nil {
		gracePeriod = *pod.Spec.TerminationGracePeriodSeconds
	}

	// Override with specified grace period if needed
	if specifiedGracePeriod > 0 {
		if respectPodGracePeriod {
			// Use whichever is longer
			if int64(specifiedGracePeriod) > gracePeriod {
				gracePeriod = int64(specifiedGracePeriod)
			}
		} else {
			// Always use specified grace period
			gracePeriod = int64(specifiedGracePeriod)
		}
	}

	return gracePeriod
}
