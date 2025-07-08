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

package v1

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	appsv1 "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
)

// PodDefaulter implements the defaulting webhook for Pods
type PodDefaulter struct {
	Client client.Client
}

var _ webhook.CustomDefaulter = &PodDefaulter{}

const signalStartRequeueTime string = "10"

//+kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,groups="",resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io,sideEffects=None,admissionReviewVersions=v1

// Default implements the defaulting logic for Pods
func (r *PodDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected a Pod but got a %T", obj)
	}

	log := log.FromContext(ctx).WithName("pod-defaulter")
	log.Info("Pod webhook called", "pod", pod.Name, "namespace", pod.Namespace)

	// Check if we should manage this pod
	singleton, isManaged, err := r.shouldManagePod(ctx, pod)
	if err != nil {
		log.Error(err, "Failed to determine if pod should be managed")
		return nil // Pod creation/update should go through even if mutation faild by our operator
	}

	if !isManaged {
		log.Info("Pod not managed by StatefulSingleton", "pod", pod.Name)
		return nil
	}

	log.Info("Mutating pod for StatefulSingleton", "pod", pod.Name, "singleton", singleton.Name)

	// Apply webhook pod mutation
	if err := r.mutatePodSpec(pod, singleton); err != nil {
		log.Error(err, "Failed to mutate pod spec")
		return nil // Pod creation/update should go through even if mutation faild by our operator
	}

	return nil
}

// shouldManagePod determines if a pod should be managed by our controller
func (r *PodDefaulter) shouldManagePod(ctx context.Context, pod *corev1.Pod) (*appsv1.StatefulSingleton, bool, error) {
	log := log.FromContext(ctx)

	// List all StatefulSingleton resources in this namespace
	var singletonList appsv1.StatefulSingletonList
	if err := r.Client.List(ctx, &singletonList, client.InNamespace(pod.Namespace)); err != nil {
		return nil, false, err
	}

	// Check if pod matches any StatefulSingleton selectors
	for _, singleton := range singletonList.Items {
		selector, err := metav1.LabelSelectorAsSelector(&singleton.Spec.Selector)
		if err != nil {
			log.Error(err, "Failed to convert label selector", "singleton", singleton.Name)
			continue
		}

		if selector.Matches(labels.Set(pod.Labels)) {
			log.Info("Pod matches StatefulSingleton selector",
				"pod", pod.Name, "singleton", singleton.Name)
			return &singleton, true, nil
		}
	}

	return nil, false, nil
}

// mutatePodSpec modifies the pod spec to include our customizations
func (r *PodDefaulter) mutatePodSpec(pod *corev1.Pod, singleton *appsv1.StatefulSingleton) error {
	// Add readiness gate
	if pod.Spec.ReadinessGates == nil {
		pod.Spec.ReadinessGates = []corev1.PodReadinessGate{}
	}

	// Check if our readiness gate already exists (meaning a reconcile happand for the pod)
	readinessGateExists := false
	for _, gate := range pod.Spec.ReadinessGates {
		if gate.ConditionType == "apps.statefulsingleton.com/singleton-ready" {
			readinessGateExists = true
			break
		}
	}

	if !readinessGateExists {
		pod.Spec.ReadinessGates = append(pod.Spec.ReadinessGates, corev1.PodReadinessGate{
			ConditionType: "apps.statefulsingleton.com/singleton-ready",
		})
	}

	// Set termination grace period if specified
	if singleton != nil && singleton.Spec.TerminationGracePeriod > 0 {
		gracePeriod := int64(singleton.Spec.TerminationGracePeriod)

		// Should original grace period be respected
		if singleton.Spec.RespectPodGracePeriod &&
			pod.Spec.TerminationGracePeriodSeconds != nil &&
			*pod.Spec.TerminationGracePeriodSeconds > gracePeriod {
			// Keep pod's grace period as it's longer
		} else {
			// Use the singleton's grace period
			pod.Spec.TerminationGracePeriodSeconds = &gracePeriod
		}
	}

	// Add volumes for signaling and wrapper scripts if they don't exist
	r.addRequiredVolumes(pod)

	// Modify containers
	r.modifyContainers(pod)

	// Add status sidecar if not already present
	r.addStatusSidecar(pod)

	// Add annotation to track that we've modified this pod
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["apps.statefulsingleton.com/statefulsingleton-managed"] = "true"

	return nil
}

// addRequiredVolumes adds signal and warpper volumes required for signaling to start pod and replace original entrypoint (pod level)
func (r *PodDefaulter) addRequiredVolumes(pod *corev1.Pod) {
	signalVolumeExists := false
	wrapperVolumeExists := false

	// Checking if our control volumes exist
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "signal-volume" {
			signalVolumeExists = true
		}
		if volume.Name == "wrapper-scripts" {
			wrapperVolumeExists = true
		}
	}

	// Adding signal volume for runtime control
	if !signalVolumeExists {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: "signal-volume",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	// Adding our wrapper script
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
}

// modifyContainers replaces original entrypoint with wrapper in main container and adds status-sidecar to control signal and start
func (r *PodDefaulter) modifyContainers(pod *corev1.Pod) {
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		// Skip if this is our sidecar
		if container.Name == "status-sidecar" {
			continue
		}

		// Capture original entrypoint
		originalEntrypoint := map[string]interface{}{
			"command": container.Command,
			"args":    container.Args,
			"image":   container.Image,
		}

		entrypointJSON, _ := json.Marshal(originalEntrypoint)

		// Add volume mounts
		r.addVolumesToContainer(container)

		// Set environment variable with original entrypoint
		r.setOriginalEntrypointEnv(container, string(entrypointJSON))

		// Replace command with wrapper script
		container.Command = []string{"/opt/wrapper/entrypoint-wrapper.sh"}
		container.Args = []string{}
	}
}

// addVolumesToContainer adds signal and warpper volumes required for signaling to start pod and replace original entrypoint (container level)
func (r *PodDefaulter) addVolumesToContainer(container *corev1.Container) {
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
}

// setOriginalEntrypointEnv registers the orignal entrypoint as an environment variable for future reference (upon starting the containers original function)
func (r *PodDefaulter) setOriginalEntrypointEnv(container *corev1.Container, entrypointJSON string) {
	entrypointEnvExists := false
	for j, env := range container.Env {
		if env.Name == "ORIGINAL_ENTRYPOINT" {
			container.Env[j].Value = entrypointJSON
			entrypointEnvExists = true
			break
		}
	}

	if !entrypointEnvExists {
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "ORIGINAL_ENTRYPOINT",
			Value: entrypointJSON,
		})
	}
}

// addStatusSidecar adds the status-sidecar container to control signal and start
func (r *PodDefaulter) addStatusSidecar(pod *corev1.Pod) {
	// Check if sidecar already exists
	for _, container := range pod.Spec.Containers {
		if container.Name == "status-sidecar" {
			return
		}
	}

	statusSidecar := corev1.Container{
		Name:    "status-sidecar",
		Image:   "registry.access.redhat.com/ubi8/ubi-minimal:latest",
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"mkdir -p /var/run/signal && while true; do sleep " + signalStartRequeueTime + "; done"}, // Running start signal validataion
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "signal-volume",
				MountPath: "/var/run/signal",
			},
		},
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

// SetupWithManager sets up the webhook with the Manager
func SetupStatefulSingletonWithManager(mgr ctrl.Manager) error {

	return ctrl.NewWebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithDefaulter(&PodDefaulter{
			Client: mgr.GetClient(),
		}).
		Complete()
}
