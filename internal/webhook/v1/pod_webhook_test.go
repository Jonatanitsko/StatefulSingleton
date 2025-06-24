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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

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
		testNamespace = "webhook-test-" + time.Now().Format("150405")
		createTestNamespace(testNamespace)

		// Set up common test data
		podLabels = map[string]string{
			"app":  "test-app",
			"tier": "database",
		}

		// Create the webhook mutator for direct testing
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

		// Test case 1: Basic webhook mutation
		It("should add readiness gate and volumes to matching pods", func() {
			// Create a basic pod that matches the StatefulSingleton selector
			By("creating a pod with matching labels")
			pod := createBasicPodSpec("test-pod", testNamespace, podLabels)

			// Create the pod - this should trigger the webhook
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			// Verify the webhook made the expected mutations
			By("verifying the webhook added required components")
			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Check that readiness gate was added
			Expect(createdPod.Spec.ReadinessGates).To(HaveLen(1))
			Expect(createdPod.Spec.ReadinessGates[0].ConditionType).To(Equal(corev1.PodConditionType("statefulsingleton.com/singleton-ready")))

			// Check that required volumes were added
			var signalVolume, wrapperVolume *corev1.Volume
			for i := range createdPod.Spec.Volumes {
				if createdPod.Spec.Volumes[i].Name == "signal-volume" {
					signalVolume = &createdPod.Spec.Volumes[i]
				}
				if createdPod.Spec.Volumes[i].Name == "wrapper-scripts" {
					wrapperVolume = &createdPod.Spec.Volumes[i]
				}
			}

			Expect(signalVolume).NotTo(BeNil(), "signal-volume should be added")
			Expect(signalVolume.EmptyDir).NotTo(BeNil(), "signal-volume should use EmptyDir")

			Expect(wrapperVolume).NotTo(BeNil(), "wrapper-scripts volume should be added")
			Expect(wrapperVolume.ConfigMap).NotTo(BeNil(), "wrapper-scripts should use ConfigMap")
			Expect(wrapperVolume.ConfigMap.Name).To(Equal("statefulsingleton-wrapper"))

			// Check that status sidecar was added
			var sidecarContainer *corev1.Container
			for i := range createdPod.Spec.Containers {
				if createdPod.Spec.Containers[i].Name == "status-sidecar" {
					sidecarContainer = &createdPod.Spec.Containers[i]
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
			Expect(createdPod.Annotations).To(HaveKeyWithValue("openshift.yourdomain.com/statefulsingleton-managed", "true"))
		})

		// Test case 2: Container command/args modification
		It("should modify container commands and preserve original entrypoint information", func() {
			By("creating a pod with specific command and args")
			pod := createBasicPodSpec("test-pod-cmd", testNamespace, podLabels)

			// Set specific command and args
			originalCommand := []string{"/usr/bin/myapp"}
			originalArgs := []string{"--config", "/etc/config.yaml", "--verbose"}
			pod.Spec.Containers[0].Command = originalCommand
			pod.Spec.Containers[0].Args = originalArgs

			// Create the pod
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			// Verify the mutations
			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// The main container should now use the wrapper script
			mainContainer := &createdPod.Spec.Containers[0]
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
			var capturedEntrypoint map[string]interface{}
			err := json.Unmarshal([]byte(originalEntrypointEnv.Value), &capturedEntrypoint)
			Expect(err).NotTo(HaveOccurred(), "ORIGINAL_ENTRYPOINT should be valid JSON")

			// Convert command and args back for comparison
			capturedCommand, exists := capturedEntrypoint["command"].([]interface{})
			Expect(exists).To(BeTrue(), "command should be present")
			Expect(len(capturedCommand)).To(Equal(len(originalCommand)))
			for i, cmd := range originalCommand {
				Expect(capturedCommand[i].(string)).To(Equal(cmd))
			}

			capturedArgs, exists := capturedEntrypoint["args"].([]interface{})
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

		// Test case 3: Grace period handling
		It("should respect StatefulSingleton grace period settings", func() {
			// Update the StatefulSingleton to have specific grace period settings
			statefulSingleton.Spec.TerminationGracePeriod = 120
			statefulSingleton.Spec.RespectPodGracePeriod = false
			Expect(k8sClient.Update(ctx, statefulSingleton)).To(Succeed())

			By("creating a pod with its own grace period")
			pod := createBasicPodSpec("test-pod-grace", testNamespace, podLabels)
			podGracePeriod := int64(60) // Pod has 60s, StatefulSingleton has 120s
			pod.Spec.TerminationGracePeriodSeconds = &podGracePeriod

			// Create the pod
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			// Verify the grace period was overridden
			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Since RespectPodGracePeriod is false, it should use StatefulSingleton's value
			Expect(*createdPod.Spec.TerminationGracePeriodSeconds).To(Equal(int64(120)))
		})

		// Test case 4: Grace period respecting pod's longer period
		It("should use pod's grace period when it's longer and RespectPodGracePeriod is true", func() {
			// StatefulSingleton has RespectPodGracePeriod=true by default
			statefulSingleton.Spec.TerminationGracePeriod = 60
			Expect(k8sClient.Update(ctx, statefulSingleton)).To(Succeed())

			By("creating a pod with longer grace period")
			pod := createBasicPodSpec("test-pod-longer-grace", testNamespace, podLabels)
			podGracePeriod := int64(180) // Pod has 180s, StatefulSingleton has 60s
			pod.Spec.TerminationGracePeriodSeconds = &podGracePeriod

			// Create the pod
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			// Verify the pod's longer grace period was kept
			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Should keep pod's longer grace period
			Expect(*createdPod.Spec.TerminationGracePeriodSeconds).To(Equal(int64(180)))
		})

		// Test case 5: Sidecar resource limits
		It("should add sidecar container with appropriate resource limits", func() {
			By("creating a pod that will have a sidecar added")
			pod := createBasicPodSpec("test-pod-resources", testNamespace, podLabels)

			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Find the sidecar container
			var sidecar *corev1.Container
			for i := range createdPod.Spec.Containers {
				if createdPod.Spec.Containers[i].Name == "status-sidecar" {
					sidecar = &createdPod.Spec.Containers[i]
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

			// Create the pod
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			// Verify the pod was NOT modified by the webhook
			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Should not have readiness gates
			Expect(createdPod.Spec.ReadinessGates).To(HaveLen(0))

			// Should not have our volumes
			for _, volume := range createdPod.Spec.Volumes {
				Expect(volume.Name).NotTo(Equal("signal-volume"))
				Expect(volume.Name).NotTo(Equal("wrapper-scripts"))
			}

			// Should not have sidecar container
			for _, container := range createdPod.Spec.Containers {
				Expect(container.Name).NotTo(Equal("status-sidecar"))
			}

			// Should not have our annotation
			Expect(createdPod.Annotations).NotTo(HaveKey("openshift.yourdomain.com/statefulsingleton-managed"))

			// Original command should be unchanged
			Expect(createdPod.Spec.Containers[0].Command).To(Equal([]string{"/usr/sbin/nginx"}))
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

			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Should still get wrapper script as command
			Expect(createdPod.Spec.Containers[0].Command).To(Equal([]string{"/opt/wrapper/entrypoint-wrapper.sh"}))

			// Check that empty command/args were captured
			var originalEntrypointEnv *corev1.EnvVar
			for _, env := range createdPod.Spec.Containers[0].Env {
				if env.Name == "ORIGINAL_ENTRYPOINT" {
					originalEntrypointEnv = &env
					break
				}
			}

			Expect(originalEntrypointEnv).NotTo(BeNil())

			var captured map[string]interface{}
			err := json.Unmarshal([]byte(originalEntrypointEnv.Value), &captured)
			Expect(err).NotTo(HaveOccurred())

			// Should have empty arrays for command and args
			Expect(captured["command"]).To(BeEmpty())
			Expect(captured["args"]).To(BeEmpty())
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

			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Should still have only 2 containers (main + existing sidecar)
			Expect(createdPod.Spec.Containers).To(HaveLen(2))

			// Find the sidecar
			var sidecar *corev1.Container
			for i := range createdPod.Spec.Containers {
				if createdPod.Spec.Containers[i].Name == "status-sidecar" {
					sidecar = &createdPod.Spec.Containers[i]
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

			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			createdPod := &corev1.Pod{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: pod.Name, Namespace: pod.Namespace,
				}, createdPod)
			}, 10*time.Second).Should(Succeed())

			// Should still have the existing volume (webhook shouldn't add duplicate)
			var signalVolume *corev1.Volume
			volumeCount := 0
			for i := range createdPod.Spec.Volumes {
				if createdPod.Spec.Volumes[i].Name == "signal-volume" {
					signalVolume = &createdPod.Spec.Volumes[i]
					volumeCount++
				}
			}

			Expect(volumeCount).To(Equal(1), "Should have only one signal-volume")
			Expect(signalVolume.HostPath).NotTo(BeNil(), "Should keep existing HostPath volume")
			Expect(signalVolume.HostPath.Path).To(Equal("/tmp/existing"))
		})
	})
})
