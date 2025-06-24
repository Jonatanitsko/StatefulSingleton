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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
	"github.com/Jonatanitsko/StatefulSingleton.git/internal/podutil"
)

// This test suite uses Ginkgo's "Describe" and "Context" to organize tests in a BDD style
// "Describe" groups related tests together
// "Context" provides more specific scenarios within a Describe block
// "It" defines individual test cases
// "BeforeEach" runs before each "It" test
// "AfterEach" runs after each "It" test

var _ = Describe("StatefulSingleton Controller", func() {
	var (
		testNamespace     string
		reconciler        *StatefulSingletonReconciler
		statefulSingleton *appsv1.StatefulSingleton
		testPodLabels     map[string]string
	)

	// BeforeEach runs before each individual test case
	BeforeEach(func() {
		// Create a unique namespace for each test to avoid conflicts
		testNamespace = "test-" + time.Now().Format("150405") // HHMMSS format
		createTestNamespace(testNamespace)

		// Set up common test data
		testPodLabels = map[string]string{
			"app":  "test-app",
			"tier": "database",
		}

		// Create the reconciler that we'll test
		reconciler = &StatefulSingletonReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: testMgr.GetEventRecorderFor("test-controller"),
		}
	})

	// AfterEach runs after each individual test case
	AfterEach(func() {
		// Clean up: delete the namespace and all resources in it
		ns := &corev1.Namespace{}
		ns.Name = testNamespace
		_ = k8sClient.Delete(ctx, ns)
	})

	// Context groups related test scenarios
	Context("When reconciling a StatefulSingleton resource", func() {
		// BeforeEach within a Context runs before each test in this Context
		BeforeEach(func() {
			// Create a StatefulSingleton resource for testing
			statefulSingleton = createBasicStatefulSingleton(
				"test-singleton",
				testNamespace,
				testPodLabels,
			)
		})

		// Test case 1: Basic reconciliation
		It("should successfully reconcile the resource when created", func() {
			// Create a NamespacedName for the reconciliation request
			namespacedName := types.NamespacedName{
				Name:      statefulSingleton.Name,
				Namespace: statefulSingleton.Namespace,
			}

			// Perform the reconciliation
			By("calling Reconcile on the StatefulSingleton")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})

			// Verify the reconciliation was successful
			Expect(err).NotTo(HaveOccurred(), "Reconcile should not return an error")
			Expect(result.Requeue).To(BeFalse(), "Should not request immediate requeue")

			// The result should have a RequeueAfter set (controller schedules next reconciliation)
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "Should schedule a future reconciliation")

			// Verify the StatefulSingleton status was updated
			By("checking that the StatefulSingleton status is updated")
			updatedSingleton := &appsv1.StatefulSingleton{}
			Eventually(func() error {
				return k8sClient.Get(ctx, namespacedName, updatedSingleton)
			}, 5*time.Second).Should(Succeed())

			// Since no pods exist yet, status should reflect that
			Expect(updatedSingleton.Status.Phase).To(Equal("Running"))
			Expect(updatedSingleton.Status.Message).To(Equal("No pods found"))
			Expect(updatedSingleton.Status.ActivePod).To(BeEmpty())
		})

		// Test case 2: Reconciliation with non-existent resource
		It("should handle reconciling a non-existent resource gracefully", func() {
			// Try to reconcile a resource that doesn't exist
			namespacedName := types.NamespacedName{
				Name:      "non-existent",
				Namespace: testNamespace,
			}

			By("calling Reconcile on a non-existent StatefulSingleton")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})

			// Should not error - controller should handle this gracefully
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	// Context for testing with pods
	Context("When managing pods", func() {
		BeforeEach(func() {
			statefulSingleton = createBasicStatefulSingleton(
				"test-singleton",
				testNamespace,
				testPodLabels,
			)
		})

		// Test case 3: Single pod management
		It("should signal a single pod to start when it's the only pod", func() {
			// Create a pod that matches our StatefulSingleton selector
			By("creating a single pod with the matching labels")
			pod := createTestPod("test-pod-1", testNamespace, testPodLabels, true)

			// Reconcile the StatefulSingleton
			namespacedName := types.NamespacedName{
				Name:      statefulSingleton.Name,
				Namespace: statefulSingleton.Namespace,
			}

			By("reconciling the StatefulSingleton")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "Should schedule future reconciliation")

			// Check that the StatefulSingleton status reflects the active pod
			By("verifying the StatefulSingleton status shows the active pod")
			Eventually(func() string {
				updatedSingleton := &appsv1.StatefulSingleton{}
				err := k8sClient.Get(ctx, namespacedName, updatedSingleton)
				if err != nil {
					return ""
				}
				return updatedSingleton.Status.ActivePod
			}, 10*time.Second, 500*time.Millisecond).Should(Equal(pod.Name))

			// Note: In a real environment, the pod would have the signal file created
			// by the controller. In our test environment, we can't easily test the
			// signal file creation since it requires exec into containers.
			// We focus on testing the controller logic instead.
		})

		// Test case 4: Pod transition scenario
		It("should handle pod transitions correctly", func() {
			// Create an "old" pod first
			By("creating the first pod")
			oldPod := createTestPod("old-pod", testNamespace, testPodLabels, true)

			// Reconcile to establish the old pod as active
			namespacedName := types.NamespacedName{
				Name:      statefulSingleton.Name,
				Namespace: statefulSingleton.Namespace,
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Wait for the old pod to be recognized as active
			Eventually(func() string {
				updatedSingleton := &appsv1.StatefulSingleton{}
				k8sClient.Get(ctx, namespacedName, updatedSingleton)
				return updatedSingleton.Status.ActivePod
			}, 5*time.Second).Should(Equal(oldPod.Name))

			// Now create a "new" pod (simulating a rolling update)
			By("creating a second pod to trigger a transition")
			newPod := createTestPod("new-pod", testNamespace, testPodLabels, true)
			// Make the new pod newer by updating its creation timestamp
			newPod.CreationTimestamp = metav1.Time{Time: time.Now().Add(1 * time.Minute)}
			Expect(k8sClient.Update(ctx, newPod)).To(Succeed())

			// Reconcile again to handle the transition
			By("reconciling to handle the pod transition")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// The controller should detect there are multiple pods and enter transitioning state
			By("verifying the controller enters transitioning state")
			Eventually(func() string {
				updatedSingleton := &appsv1.StatefulSingleton{}
				k8sClient.Get(ctx, namespacedName, updatedSingleton)
				return updatedSingleton.Status.Phase
			}, 5*time.Second).Should(Equal("Transitioning"))

			// Simulate the old pod being deleted (what would happen in a real rollout)
			By("deleting the old pod to complete the transition")
			Expect(k8sClient.Delete(ctx, oldPod)).To(Succeed())

			// Reconcile again to complete the transition
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Eventually, the new pod should become active
			By("verifying the new pod becomes active")
			Eventually(func() string {
				updatedSingleton := &appsv1.StatefulSingleton{}
				k8sClient.Get(ctx, namespacedName, updatedSingleton)
				return updatedSingleton.Status.ActivePod
			}, 10*time.Second).Should(Equal(newPod.Name))

			// And the phase should return to Running
			Eventually(func() string {
				updatedSingleton := &appsv1.StatefulSingleton{}
				k8sClient.Get(ctx, namespacedName, updatedSingleton)
				return updatedSingleton.Status.Phase
			}, 5*time.Second).Should(Equal("Running"))
		})

		// Test case 5: Testing ConfigMap creation
		It("should create the wrapper ConfigMap in the namespace", func() {
			// Trigger reconciliation which should create the ConfigMap
			namespacedName := types.NamespacedName{
				Name:      statefulSingleton.Name,
				Namespace: statefulSingleton.Namespace,
			}

			By("reconciling to trigger ConfigMap creation")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Check that the ConfigMap was created
			By("verifying the wrapper ConfigMap was created")
			configMap := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      "statefulsingleton-wrapper",
					Namespace: testNamespace,
				}, configMap)
			}, 5*time.Second).Should(Succeed())

			// Verify the ConfigMap has the expected content
			Expect(configMap.Data).To(HaveKey("entrypoint-wrapper.sh"))
			Expect(configMap.Data["entrypoint-wrapper.sh"]).To(ContainSubstring("StatefulSingleton"))
			Expect(configMap.Labels).To(HaveKeyWithValue("app.kubernetes.io/part-of", "statefulsingleton-operator"))
		})
	})

	// Context for testing utility functions
	Context("When testing pod utility functions", func() {
		It("should correctly identify pods with singleton readiness gates", func() {
			// Create a pod without readiness gate
			podWithoutGate := createTestPod("pod-without-gate", testNamespace, testPodLabels, false)

			// Create a pod with readiness gate
			podWithGate := createTestPod("pod-with-gate", testNamespace, testPodLabels, true)

			// Test the utility function
			Expect(podutil.HasSingletonReadinessGate(podWithoutGate)).To(BeFalse())
			Expect(podutil.HasSingletonReadinessGate(podWithGate)).To(BeTrue())
		})

		It("should correctly identify pod states", func() {
			pod := createTestPod("test-pod", testNamespace, testPodLabels, true)

			// Test running state
			Expect(podutil.IsPodRunning(pod)).To(BeTrue())

			// Test terminating state
			Expect(podutil.IsPodTerminating(pod)).To(BeFalse())

			// Simulate pod deletion (sets DeletionTimestamp)
			now := metav1.Time{Time: time.Now()}
			pod.DeletionTimestamp = &now
			Expect(k8sClient.Update(ctx, pod)).To(Succeed())

			// Refresh pod from API
			updatedPod := &corev1.Pod{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: pod.Name, Namespace: pod.Namespace,
			}, updatedPod)).To(Succeed())

			Expect(podutil.IsPodTerminating(updatedPod)).To(BeTrue())
		})
	})

	// Context for error scenarios
	Context("When handling error scenarios", func() {
		It("should handle errors when StatefulSingleton is deleted during reconciliation", func() {
			statefulSingleton = createBasicStatefulSingleton(
				"test-singleton",
				testNamespace,
				testPodLabels,
			)

			namespacedName := types.NamespacedName{
				Name:      statefulSingleton.Name,
				Namespace: statefulSingleton.Namespace,
			}

			// Delete the StatefulSingleton
			By("deleting the StatefulSingleton")
			Expect(k8sClient.Delete(ctx, statefulSingleton)).To(Succeed())

			// Try to reconcile the deleted resource
			By("reconciling the deleted resource")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})

			// Should handle gracefully (no error, no requeue)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("should handle transition timeout scenarios", func() {
			// Create a StatefulSingleton with a very short transition timeout
			shortTimeoutSingleton := createBasicStatefulSingleton(
				"timeout-test",
				testNamespace,
				testPodLabels,
			)
			shortTimeoutSingleton.Spec.MaxTransitionTime = 1 // 1 second timeout
			Expect(k8sClient.Update(ctx, shortTimeoutSingleton)).To(Succeed())

			// Create two pods to trigger a transition
			oldPod := createTestPod("old-pod", testNamespace, testPodLabels, true)
			newPod := createTestPod("new-pod", testNamespace, testPodLabels, true)

			// Make new pod newer
			newPod.CreationTimestamp = metav1.Time{Time: time.Now().Add(1 * time.Minute)}
			Expect(k8sClient.Update(ctx, newPod)).To(Succeed())

			namespacedName := types.NamespacedName{
				Name:      shortTimeoutSingleton.Name,
				Namespace: shortTimeoutSingleton.Namespace,
			}

			// First reconcile to establish old pod as active
			By("establishing the old pod as active")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify old pod is active
			Eventually(func() string {
				updatedSingleton := &appsv1.StatefulSingleton{}
				k8sClient.Get(ctx, namespacedName, updatedSingleton)
				return updatedSingleton.Status.ActivePod
			}, 5*time.Second).Should(Equal(oldPod.Name))

			// Second reconcile to start transition (both pods exist now)
			By("triggering transition with both pods present")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Wait longer than the timeout period
			time.Sleep(2 * time.Second)

			// Reconcile again - should handle timeout
			By("reconciling after timeout period")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: namespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// The controller should still be in transitioning state but handle timeout gracefully
			// (In a real scenario, this might generate warning events)
			updatedSingleton := &appsv1.StatefulSingleton{}
			Expect(k8sClient.Get(ctx, namespacedName, updatedSingleton)).To(Succeed())
			// Verify the controller is still functioning (no panic, etc.)
			Expect(updatedSingleton.Status.Phase).To(BeElementOf("Running", "Transitioning"))
		})
	})
})
