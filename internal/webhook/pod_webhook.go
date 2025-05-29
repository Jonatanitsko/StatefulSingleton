// In the internal/webhook/pod_webhook.go file, add the import for podutil
package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	appsv1 "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
	"github.com/Jonatanitsko/StatefulSingleton.git/internal/podutil"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,groups="",resources=pods,verbs=create,versions=v1,name=mpod.kb.io,sideEffects=None,admissionReviewVersions=v1

// PodMutator mutates Pods
type PodMutator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// shouldManagePod determines if a pod should be managed by our controller
func (m *PodMutator) shouldManagePod(ctx context.Context, pod *corev1.Pod) (*appsv1.StatefulSingleton, bool, error) {
	logger := log.FromContext(ctx).WithName("pod-webhook")

	// List all StatefulSingleton resources in this namespace
	var singletonList appsv1.StatefulSingletonList
	if err := m.Client.List(ctx, &singletonList, client.InNamespace(pod.Namespace)); err != nil {
		return nil, false, err
	}

	// Check if pod labels match any StatefulSingleton selectors
	for _, singleton := range singletonList.Items {
		selector, err := metav1.LabelSelectorAsSelector(&singleton.Spec.Selector)
		if err != nil {
			logger.Error(err, "Failed to convert label selector", "singleton", singleton.Name)
			continue
		}

		if selector.Matches(labels.Set(pod.Labels)) {
			logger.Info("Pod matches StatefulSingleton selector",
				"pod", pod.Name, "singleton", singleton.Name)
			return &singleton, true, nil
		}
	}

	return nil, false, nil
}

// mutatePodSpec modifies the pod spec to include our customizations
func (m *PodMutator) mutatePodSpec(pod *corev1.Pod, singleton *appsv1.StatefulSingleton) error {
	// Add readiness gate
	if pod.Spec.ReadinessGates == nil {
		pod.Spec.ReadinessGates = []corev1.PodReadinessGate{}
	}

	readinessGateExists := false
	for _, gate := range pod.Spec.ReadinessGates {
		if gate.ConditionType == podutil.SingletonReadyConditionType {
			readinessGateExists = true
			break
		}
	}

	if !readinessGateExists {
		pod.Spec.ReadinessGates = append(pod.Spec.ReadinessGates, corev1.PodReadinessGate{
			ConditionType: podutil.SingletonReadyConditionType,
		})
	}

	// Set termination grace period if specified in the StatefulSingleton. Default set to 30s.
	if singleton != nil && singleton.Spec.TerminationGracePeriod > 0 {
		gracePeriod := int64(singleton.Spec.TerminationGracePeriod)

		// Checking if the pod has its own grace period - if so we should respect it
		if singleton.Spec.RespectPodGracePeriod &&
			pod.Spec.TerminationGracePeriodSeconds != nil &&
			*pod.Spec.TerminationGracePeriodSeconds > gracePeriod {
			// Keep pod's grace period as it's longer than suggested by StatefulSingleton
		} else {
			// Use the singleton's grace period
			pod.Spec.TerminationGracePeriodSeconds = &gracePeriod
		}
	}

	// Add volumes for signaling and wrapper scripts if they don't exist
	signalVolumeExists := false
	wrapperVolumeExists := false

	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "signal-volume" {
			signalVolumeExists = true
		}
		if volume.Name == "wrapper-scripts" {
			wrapperVolumeExists = true
		}
	}

	// Using emptydir for minimal creation time
	if !signalVolumeExists {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "signal-volume",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	// Wrapper script is a configmap created in the pods namespace when pod is controlled by StatefulSingleton
	if !wrapperVolumeExists {
		defaultMode := int32(0755)
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "wrapper-scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "statefulsingleton-wrapper",
					},
					DefaultMode: &defaultMode,
				},
			},
		})
	}

	// For each container, always capture entrypoint information for proper handling
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		// Skip if this is our sidecar
		if container.Name == "status-sidecar" {
			continue
		}

		// Always capture original entrypoint information, even if empty
		// This allows our wrapper script to distinguish between:
		// 1. Explicitly defined commands/args in pod spec
		// 2. Containers that rely on Dockerfile ENTRYPOINT/CMD (both will be empty arrays)
		originalEntrypoint := map[string]interface{}{
			"command": container.Command, // May be empty array []
			"args":    container.Args,    // May be empty array []
			"image":   container.Image,   // Include for reference/debugging
		}

		entrypointJSON, _ := json.Marshal(originalEntrypoint)

		// Add our wrapper and signal volume mounts if they don't exist
		signalMountExists := false
		wrapperMountExists := false

		for _, mount := range container.VolumeMounts {
			if mount.Name == "signal-volume" {
				signalMountExists = true
			}
			if mount.Name == "wrapper-scripts" {
				wrapperMountExists = true
			}
		}

		if !signalMountExists {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "signal-volume",
				MountPath: "/var/run/signal",
			})
		}

		if !wrapperMountExists {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "wrapper-scripts",
				MountPath: "/opt/wrapper",
			})
		}

		// Always set the environment variable with captured entrypoint information
		entrypointEnvExists := false
		for j, env := range container.Env {
			if env.Name == "ORIGINAL_ENTRYPOINT" {
				// Update existing environment variable
				container.Env[j].Value = string(entrypointJSON)
				entrypointEnvExists = true
				break
			}
		}

		if !entrypointEnvExists {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "ORIGINAL_ENTRYPOINT",
				Value: string(entrypointJSON),
			})
		}

		// Replace command with wrapper script
		// This is safe because we've captured the original command/args above
		container.Command = []string{"/opt/wrapper/entrypoint-wrapper.sh"}
		container.Args = []string{} // Clear args since wrapper will handle them
	}

	// Add status sidecar if not already present
	sidecarExists := false
	for _, container := range pod.Spec.Containers {
		if container.Name == "status-sidecar" {
			sidecarExists = true
			break
		}
	}

	// Creating sidecar container to handle signal.
	// Sidecar container is our way for the operator to communicate with pods without modifying the main application
	if !sidecarExists {
		statusSidecar := corev1.Container{
			Name:    "status-sidecar",
			Image:   "registry.access.redhat.com/ubi8/ubi-minimal:latest",
			Command: []string{"/bin/sh", "-c"},
			Args:    []string{"mkdir -p /var/run/signal && while true; do sleep 30; done"},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "signal-volume",
					MountPath: "/var/run/signal",
				},
			},
			// Arbitrary low resources for sidecar container, mininmal requirements for above signal script.
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		}
		pod.Spec.Containers = append(pod.Spec.Containers, statusSidecar)
	}

	// Add annotation to track that we've modified this pod, for controller tracking
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["openshift.yourdomain.com/statefulsingleton-managed"] = "true"

	return nil
}

// Handle implements admission.Handler
func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := log.FromContext(ctx).WithName("pod-webhook")

	// Decode the Pod
	pod := &corev1.Pod{}
	err := m.decoder.Decode(req, pod)
	if err != nil {
		logger.Error(err, "Failed to decode pod")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Check if this pod should be managed by our controller
	singleton, managed, err := m.shouldManagePod(ctx, pod)
	if err != nil {
		logger.Error(err, "Failed to determine if pod should be managed")
		return admission.Allowed("Error checking management status")
	}

	if !managed {
		return admission.Allowed("Pod not managed by StatefulSingleton")
	}

	logger.Info("Mutating pod", "namespace", pod.Namespace, "name", pod.Name)

	// Apply modifications to the pod spec
	if err := m.mutatePodSpec(pod, singleton); err != nil {
		logger.Error(err, "Failed to mutate pod spec")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// Create the patch
	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		logger.Error(err, "Failed to marshal pod")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// InjectDecoder injects the decoder.
// Called automatically by the controller-runtime framework during webhook setup, before the webhook starts handling requests
func (m *PodMutator) InjectDecoder(d *admission.Decoder) error {
	m.decoder = d
	return nil
}

// SetupWithManager sets up the webhook with the Manager
func (m *PodMutator) SetupWithManager(mgr ctrl.Manager) error {
	m.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithDefaulter(m).
		Complete()
}
