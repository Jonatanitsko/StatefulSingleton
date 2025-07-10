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
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	appsv1 "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
)

// This test suite verifies that our mutating webhook correctly modifies pods
// when they match a StatefulSingleton selector
var _ = Describe("Pod Webhook", func() {
	var (
		testNamespace string
		podLabels     map[string]string
	)

	// BeforeEach runs before each test case
	BeforeEach(func() {
		// Create a unique namespace for each test
		testNamespace = fmt.Sprintf("test-ns-%d-%d",
			time.Now().Unix(),
			rand.Intn(10000)) // HHMMSS format
		createTestNamespace(testNamespace)

		// Set up common test data
		podLabels = map[string]string{
			"app":  "test-app",
			"tier": "database",
		}
	})

	// AfterEach runs after each test case
	AfterEach(func() {
		// Clean up: delete the namespace
		ns := &corev1.Namespace{}
		ns.Name = testNamespace
		_ = k8sClient.Delete(ctx, ns)
	})

	// Context groups related webhook test scenarios
	Context("When a pod matches a StatefulSingleton selector", func() {
		var statefulSingleton *appsv1.StatefulSingleton

		BeforeEach(func() {
			// Create a StatefulSingleton that will match our test pods
			statefulSingleton = createBasicStatefulSingleton(
				"test-singleton",
				testNamespace,
				podLabels,
			)
		})

		It("should test webhook logic directly", func() {
			By("creating a pod with matching labels")
			pod := createBasicPodSpec("webhook-test-pod", testNamespace, podLabels)

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}

			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying webhook modifications")

			// Check that readiness gate was added
			Expect(pod.Spec.ReadinessGates).To(HaveLen(1))
			Expect(pod.Spec.ReadinessGates[0].ConditionType).To(Equal(corev1.PodConditionType("apps.statefulsingleton.com/singleton-ready")))

			// Check that required volumes were added
			var signalVolume, wrapperVolume *corev1.Volume
			for i := range pod.Spec.Volumes {
				if pod.Spec.Volumes[i].Name == "signal-volume" {
					signalVolume = &pod.Spec.Volumes[i]
				}
				if pod.Spec.Volumes[i].Name == "wrapper-scripts" {
					wrapperVolume = &pod.Spec.Volumes[i]
				}
			}

			Expect(signalVolume).NotTo(BeNil(), "signal-volume should be added")
			Expect(signalVolume.EmptyDir).NotTo(BeNil(), "signal-volume should use EmptyDir")

			Expect(wrapperVolume).NotTo(BeNil(), "wrapper-scripts volume should be added")
			Expect(wrapperVolume.ConfigMap).NotTo(BeNil(), "wrapper-scripts should use ConfigMap")
			Expect(wrapperVolume.ConfigMap.Name).To(Equal("statefulsingleton-wrapper"))

			// Check that status sidecar was added
			var sidecarContainer *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == "status-sidecar" {
					sidecarContainer = &pod.Spec.Containers[i]
					break
				}
			}

			Expect(sidecarContainer).NotTo(BeNil(), "status-sidecar container should be added")
			Expect(sidecarContainer.Image).To(Equal("registry.access.redhat.com/ubi8/ubi-minimal:latest"))

			// Check that the management annotation was added
			Expect(pod.Annotations).To(HaveKeyWithValue("apps.statefulsingleton.com/statefulsingleton-managed", "true"))
		})

		It("should add readiness gate and volumes to matching pods", func() {
			By("creating a pod with matching labels")
			pod := createBasicPodSpec("test-pod", testNamespace, podLabels)

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the webhook added required components")

			// Check that readiness gate was added
			Expect(pod.Spec.ReadinessGates).To(HaveLen(1))
			Expect(pod.Spec.ReadinessGates[0].ConditionType).To(Equal(corev1.PodConditionType("apps.statefulsingleton.com/singleton-ready")))

			// Check that required volumes were added
			var signalVolume, wrapperVolume *corev1.Volume
			for i := range pod.Spec.Volumes {
				if pod.Spec.Volumes[i].Name == "signal-volume" {
					signalVolume = &pod.Spec.Volumes[i]
				}
				if pod.Spec.Volumes[i].Name == "wrapper-scripts" {
					wrapperVolume = &pod.Spec.Volumes[i]
				}
			}

			Expect(signalVolume).NotTo(BeNil(), "signal-volume should be added")
			Expect(signalVolume.EmptyDir).NotTo(BeNil(), "signal-volume should use EmptyDir")

			Expect(wrapperVolume).NotTo(BeNil(), "wrapper-scripts volume should be added")
			Expect(wrapperVolume.ConfigMap).NotTo(BeNil(), "wrapper-scripts should use ConfigMap")
			Expect(wrapperVolume.ConfigMap.Name).To(Equal("statefulsingleton-wrapper"))

			// Check that status sidecar was added
			var sidecarContainer *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == "status-sidecar" {
					sidecarContainer = &pod.Spec.Containers[i]
					break
				}
			}

			Expect(sidecarContainer).NotTo(BeNil(), "status-sidecar container should be added")
			Expect(sidecarContainer.Image).To(Equal("registry.access.redhat.com/ubi8/ubi-minimal:latest"))

			// Verify sidecar has the signal volume mount
			var signalMount *corev1.VolumeMount
			for i := range sidecarContainer.VolumeMounts {
				if sidecarContainer.VolumeMounts[i].Name == "signal-volume" {
					signalMount = &sidecarContainer.VolumeMounts[i]
					break
				}
			}
			Expect(signalMount).NotTo(BeNil(), "sidecar should have signal volume mount")
			Expect(signalMount.MountPath).To(Equal("/var/run/signal"))

			// Check that the annotation was added
			Expect(pod.Annotations).To(HaveKeyWithValue("apps.statefulsingleton.com/statefulsingleton-managed", "true"))
		})

		It("should modify container commands and preserve original entrypoint information", func() {
			By("creating a pod with specific command and args")
			pod := createBasicPodSpec("test-pod-cmd", testNamespace, podLabels)

			// Set specific command and args
			originalCommand := []string{"/usr/bin/myapp"}
			originalArgs := []string{"--config", "/etc/config.yaml", "--verbose"}
			pod.Spec.Containers[0].Command = originalCommand
			pod.Spec.Containers[0].Args = originalArgs

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the mutations")

			// The main container should now use the wrapper script
			mainContainer := &pod.Spec.Containers[0]
			Expect(mainContainer.Command).To(Equal([]string{"/opt/wrapper/entrypoint-wrapper.sh"}))
			Expect(mainContainer.Args).To(HaveLen(0), "Args should be cleared since wrapper handles them")

			// Check that original entrypoint was captured in environment variable
			var originalEntrypointEnv *corev1.EnvVar
			for i := range mainContainer.Env {
				if mainContainer.Env[i].Name == "ORIGINAL_ENTRYPOINT" {
					originalEntrypointEnv = &mainContainer.Env[i]
					break
				}
			}

			Expect(originalEntrypointEnv).NotTo(BeNil(), "ORIGINAL_ENTRYPOINT env var should be set")

			// Parse and verify the captured entrypoint
			var capturedEntrypoint map[string]any
			err = json.Unmarshal([]byte(originalEntrypointEnv.Value), &capturedEntrypoint)
			Expect(err).NotTo(HaveOccurred(), "ORIGINAL_ENTRYPOINT should be valid JSON")

			// Convert command and args back for comparison
			capturedCommand, exists := capturedEntrypoint["command"].([]any)
			Expect(exists).To(BeTrue(), "command should be present")
			Expect(len(capturedCommand)).To(Equal(len(originalCommand)))
			for i, cmd := range originalCommand {
				Expect(capturedCommand[i].(string)).To(Equal(cmd))
			}

			capturedArgs, exists := capturedEntrypoint["args"].([]any)
			Expect(exists).To(BeTrue(), "args should be present")
			Expect(len(capturedArgs)).To(Equal(len(originalArgs)))
			for i, arg := range originalArgs {
				Expect(capturedArgs[i].(string)).To(Equal(arg))
			}

			// Check that volume mounts were added to main container
			var signalMount, wrapperMount bool
			for _, mount := range mainContainer.VolumeMounts {
				if mount.Name == "signal-volume" && mount.MountPath == "/var/run/signal" {
					signalMount = true
				}
				if mount.Name == "wrapper-scripts" && mount.MountPath == "/opt/wrapper" {
					wrapperMount = true
				}
			}
			Expect(signalMount).To(BeTrue(), "main container should have signal volume mount")
			Expect(wrapperMount).To(BeTrue(), "main container should have wrapper scripts mount")
		})

		It("should respect StatefulSingleton grace period settings", func() {
			// Update the StatefulSingleton to have specific grace period settings
			statefulSingleton.Spec.TerminationGracePeriod = 120
			statefulSingleton.Spec.RespectPodGracePeriod = false
			Expect(k8sClient.Update(ctx, statefulSingleton)).To(Succeed())

			By("creating a pod with its own grace period")
			pod := createBasicPodSpec("test-pod-grace", testNamespace, podLabels)
			podGracePeriod := int64(60) // Pod has 60s, StatefulSingleton has 120s
			pod.Spec.TerminationGracePeriodSeconds = &podGracePeriod

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the grace period was overridden")
			// Since RespectPodGracePeriod is false, it should use StatefulSingleton's value
			Expect(*pod.Spec.TerminationGracePeriodSeconds).To(Equal(int64(120)))
		})

		It("should use pod's grace period when it's longer and RespectPodGracePeriod is true", func() {
			// StatefulSingleton has RespectPodGracePeriod=true by default
			statefulSingleton.Spec.TerminationGracePeriod = 60
			Expect(k8sClient.Update(ctx, statefulSingleton)).To(Succeed())

			By("creating a pod with longer grace period")
			pod := createBasicPodSpec("test-pod-longer-grace", testNamespace, podLabels)
			podGracePeriod := int64(180) // Pod has 180s, StatefulSingleton has 60s
			pod.Spec.TerminationGracePeriodSeconds = &podGracePeriod

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the pod's longer grace period was kept")
			// Should keep pod's longer grace period
			Expect(*pod.Spec.TerminationGracePeriodSeconds).To(Equal(int64(180)))
		})

		It("should add sidecar container with appropriate resource limits", func() {
			By("creating a pod that will have a sidecar added")
			pod := createBasicPodSpec("test-pod-resources", testNamespace, podLabels)

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying sidecar resource limits")
			// Find the sidecar container
			var sidecar *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == "status-sidecar" {
					sidecar = &pod.Spec.Containers[i]
					break
				}
			}

			Expect(sidecar).NotTo(BeNil())

			// Verify resource limits
			Expect(sidecar.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("10m")))
			Expect(sidecar.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("32Mi")))
			Expect(sidecar.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("20m")))
			Expect(sidecar.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("64Mi")))
		})

		It("should handle multiple containers in a pod", func() {
			By("creating a pod with multiple containers")
			pod := createBasicPodSpec("multi-container-pod", testNamespace, podLabels)

			// Add a second container
			pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
				Name:    "second-container",
				Image:   "busybox:latest",
				Command: []string{"/bin/sh"},
				Args:    []string{"-c", "sleep infinity"},
			})

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying both containers were modified")
			// Both main containers should have wrapper script
			Expect(pod.Spec.Containers[0].Command).To(Equal([]string{"/opt/wrapper/entrypoint-wrapper.sh"}))
			Expect(pod.Spec.Containers[1].Command).To(Equal([]string{"/opt/wrapper/entrypoint-wrapper.sh"}))

			// Both should have volume mounts
			for i := 0; i < 2; i++ {
				container := &pod.Spec.Containers[i]
				var signalMount, wrapperMount bool
				for _, mount := range container.VolumeMounts {
					if mount.Name == "signal-volume" {
						signalMount = true
					}
					if mount.Name == "wrapper-scripts" {
						wrapperMount = true
					}
				}
				Expect(signalMount).To(BeTrue(), "container %d should have signal volume mount", i)
				Expect(wrapperMount).To(BeTrue(), "container %d should have wrapper mount", i)
			}

			// Should have status sidecar (total 3 containers)
			Expect(pod.Spec.Containers).To(HaveLen(3))
			Expect(pod.Spec.Containers[2].Name).To(Equal("status-sidecar"))
		})

		It("should handle existing volume mounts in containers", func() {
			By("creating a pod with existing volume mounts")
			pod := createBasicPodSpec("existing-mounts-pod", testNamespace, podLabels)

			// Add existing volume mounts
			pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
				{
					Name:      "existing-mount",
					MountPath: "/existing",
				},
				{
					Name:      "signal-volume", // Same name as webhook volume
					MountPath: "/custom/signal/path",
				},
			}

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying existing mounts were preserved and new ones added")
			container := &pod.Spec.Containers[0]

			var existingMount, signalMount, wrapperMount bool
			signalMountPath := ""

			for _, mount := range container.VolumeMounts {
				switch mount.Name {
				case "existing-mount":
					existingMount = true
				case "signal-volume":
					signalMount = true
					signalMountPath = mount.MountPath
				case "wrapper-scripts":
					wrapperMount = true
				}
			}

			Expect(existingMount).To(BeTrue(), "existing mount should be preserved")
			Expect(signalMount).To(BeTrue(), "signal mount should exist")
			Expect(signalMountPath).To(Equal("/custom/signal/path"), "existing signal mount path should be preserved")
			Expect(wrapperMount).To(BeTrue(), "wrapper mount should be added")
		})

		It("should handle existing environment variables", func() {
			By("creating a pod with existing environment variables")
			pod := createBasicPodSpec("existing-env-pod", testNamespace, podLabels)

			// Add existing env vars including conflicting name
			pod.Spec.Containers[0].Env = []corev1.EnvVar{
				{Name: "EXISTING_VAR", Value: "value1"},
				{Name: "ORIGINAL_ENTRYPOINT", Value: "should-be-overwritten"},
			}

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying environment variables were handled correctly")
			container := &pod.Spec.Containers[0]

			var existingVar, originalEntrypoint *corev1.EnvVar
			for i := range container.Env {
				switch container.Env[i].Name {
				case "EXISTING_VAR":
					existingVar = &container.Env[i]
				case "ORIGINAL_ENTRYPOINT":
					originalEntrypoint = &container.Env[i]
				}
			}

			Expect(existingVar).NotTo(BeNil(), "existing env var should be preserved")
			Expect(existingVar.Value).To(Equal("value1"))

			Expect(originalEntrypoint).NotTo(BeNil(), "ORIGINAL_ENTRYPOINT should be set")
			Expect(originalEntrypoint.Value).NotTo(Equal("should-be-overwritten"), "ORIGINAL_ENTRYPOINT should be updated by webhook")

			// Verify it contains valid JSON
			var entrypoint map[string]any
			err = json.Unmarshal([]byte(originalEntrypoint.Value), &entrypoint)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// Context for testing non-matching pods
	Context("When a pod does not match any StatefulSingleton selector", func() {
		It("should not modify pods that don't match any StatefulSingleton", func() {
			// Create a StatefulSingleton with specific labels
			createBasicStatefulSingleton("test-singleton", testNamespace, map[string]string{
				"app": "database",
			})

			By("creating a pod with different labels")
			pod := createBasicPodSpec("non-matching-pod", testNamespace, map[string]string{
				"app": "web-server", // Different from StatefulSingleton selector
			})

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the pod was NOT modified by the webhook")
			// Should not have readiness gates
			Expect(pod.Spec.ReadinessGates).To(HaveLen(0))

			// Should not have our volumes
			for _, volume := range pod.Spec.Volumes {
				Expect(volume.Name).NotTo(Equal("signal-volume"))
				Expect(volume.Name).NotTo(Equal("wrapper-scripts"))
			}

			// Should not have sidecar container
			for _, container := range pod.Spec.Containers {
				Expect(container.Name).NotTo(Equal("status-sidecar"))
			}

			// Should not have our annotation
			Expect(pod.Annotations).NotTo(HaveKey("apps.statefulsingleton.com/statefulsingleton-managed"))

			// Original command should be unchanged
			Expect(pod.Spec.Containers[0].Command).To(Equal([]string{"/usr/sbin/nginx"}))
		})
	})

	// Context for testing error handling
	Context("When handling error scenarios", func() {
		var statefulSingleton *appsv1.StatefulSingleton

		BeforeEach(func() {
			// Create a StatefulSingleton that will match our test pods
			statefulSingleton = createBasicStatefulSingleton(
				"test-singleton",
				testNamespace,
				podLabels,
			)
		})

		It("should handle invalid object type", func() {
			By("calling webhook with non-pod object")
			defaulter := &PodDefaulter{Client: k8sClient}

			// Pass a different object type
			configMap := &corev1.ConfigMap{}
			err := defaulter.Default(ctx, configMap)

			By("verifying error is returned")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected a Pod but got"))
		})

		It("should handle pods with nil labels", func() {
			By("creating a pod with nil labels")
			pod := createBasicPodSpec("nil-labels-pod", testNamespace, nil)

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying pod was not modified")
			// Should not have readiness gates since labels don't match
			Expect(pod.Spec.ReadinessGates).To(HaveLen(0))
			Expect(pod.Annotations).NotTo(HaveKey("apps.statefulsingleton.com/statefulsingleton-managed"))
		})

		It("should handle pod with existing readiness gate", func() {
			By("creating a pod with existing readiness gate")
			pod := createBasicPodSpec("existing-gate-pod", testNamespace, podLabels)

			// Add existing readiness gate (same as webhook)
			pod.Spec.ReadinessGates = []corev1.PodReadinessGate{
				{ConditionType: "apps.statefulsingleton.com/singleton-ready"},
			}

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying no duplicate readiness gate was added")
			Expect(pod.Spec.ReadinessGates).To(HaveLen(1))
			Expect(pod.Spec.ReadinessGates[0].ConditionType).To(Equal(corev1.PodConditionType("apps.statefulsingleton.com/singleton-ready")))
		})

		It("should handle StatefulSingleton with zero grace period", func() {
			// Update to have zero grace period
			statefulSingleton.Spec.TerminationGracePeriod = 0
			Expect(k8sClient.Update(ctx, statefulSingleton)).To(Succeed())

			By("creating a pod")
			pod := createBasicPodSpec("zero-grace-pod", testNamespace, podLabels)
			originalGracePeriod := int64(30)
			pod.Spec.TerminationGracePeriodSeconds = &originalGracePeriod

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying grace period was not modified")
			// Should keep original grace period since singleton has 0
			Expect(*pod.Spec.TerminationGracePeriodSeconds).To(Equal(int64(30)))
		})
	})

	// Context for testing edge cases
	Context("When handling edge cases", func() {
		BeforeEach(func() {
			createBasicStatefulSingleton("test-singleton", testNamespace, podLabels)
		})

		It("should handle pods with no explicit command/args (relying on Dockerfile)", func() {
			By("creating a pod with no command or args")
			pod := createBasicPodSpec("no-cmd-pod", testNamespace, podLabels)
			pod.Spec.Containers[0].Command = nil
			pod.Spec.Containers[0].Args = nil

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying webhook handled empty command/args")
			// Should still get wrapper script as command
			Expect(pod.Spec.Containers[0].Command).To(Equal([]string{"/opt/wrapper/entrypoint-wrapper.sh"}))

			// Check that empty command/args were captured
			var originalEntrypointEnv *corev1.EnvVar
			for _, env := range pod.Spec.Containers[0].Env {
				if env.Name == "ORIGINAL_ENTRYPOINT" {
					originalEntrypointEnv = &env
					break
				}
			}

			Expect(originalEntrypointEnv).NotTo(BeNil())

			var captured map[string]any
			err = json.Unmarshal([]byte(originalEntrypointEnv.Value), &captured)
			Expect(err).NotTo(HaveOccurred())

			// Should have nil values for command and args when not specified
			Expect(captured["command"]).To(BeNil())
			Expect(captured["args"]).To(BeNil())
			Expect(captured["image"]).To(Equal("nginx:latest"))
		})

		It("should not modify existing sidecar containers", func() {
			By("creating a pod that already has a status-sidecar container")
			pod := createBasicPodSpec("existing-sidecar-pod", testNamespace, podLabels)

			// Add an existing sidecar
			existingSidecar := corev1.Container{
				Name:  "status-sidecar",
				Image: "custom-sidecar:latest",
			}
			pod.Spec.Containers = append(pod.Spec.Containers, existingSidecar)

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying existing sidecar was not modified")
			// Should still have only 2 containers (main + existing sidecar)
			Expect(pod.Spec.Containers).To(HaveLen(2))

			// Find the sidecar
			var sidecar *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == "status-sidecar" {
					sidecar = &pod.Spec.Containers[i]
					break
				}
			}

			// Should keep the original image
			Expect(sidecar.Image).To(Equal("custom-sidecar:latest"))
		})

		It("should handle pods with existing volumes of the same name", func() {
			By("creating a pod with existing signal-volume")
			pod := createBasicPodSpec("existing-volume-pod", testNamespace, podLabels)

			// Add an existing volume with the same name
			existingVolume := corev1.Volume{
				Name: "signal-volume",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/tmp/existing",
					},
				},
			}
			pod.Spec.Volumes = []corev1.Volume{existingVolume}

			By("calling webhook logic directly")
			defaulter := &PodDefaulter{Client: k8sClient}
			err := defaulter.Default(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			By("verifying existing volume was preserved")
			// Should still have the existing volume (webhook shouldn't add duplicate)
			var signalVolume *corev1.Volume
			volumeCount := 0
			for i := range pod.Spec.Volumes {
				if pod.Spec.Volumes[i].Name == "signal-volume" {
					signalVolume = &pod.Spec.Volumes[i]
					volumeCount++
				}
			}

			Expect(volumeCount).To(Equal(1), "Should have only one signal-volume")
			Expect(signalVolume.HostPath).NotTo(BeNil(), "Should keep existing HostPath volume")
			Expect(signalVolume.HostPath.Path).To(Equal("/tmp/existing"))
		})
	})
})
