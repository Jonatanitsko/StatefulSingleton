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

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	statefulsingleton "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
)

// E2E tests for StatefulSingleton operator
// These tests run against a real cluster (Kind) with the operator deployed
var _ = Describe("StatefulSingleton E2E Tests", Ordered, func() {
	var (
		testNamespace = "e2e-test-statefulsingleton"
		ctx           = context.Background()
		k8sClient     client.Client
	)

	// BeforeAll runs once before all tests in this Describe block
	BeforeAll(func() {
		By("setting up k8s client")
		cfg, err := config.GetConfig()
		Expect(err).NotTo(HaveOccurred())

		k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		By("creating test namespace")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
		err = k8sClient.Create(ctx, ns)
		if err != nil {
			// Namespace might already exist
			Expect(err).NotTo(HaveOccurred())
		}
	})

	// AfterAll runs once after all tests in this Describe block
	AfterAll(func() {
		By("cleaning up test namespace")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		err := k8sClient.Delete(ctx, ns)
		if err != nil && !errors.IsNotFound(err) {
			GinkgoLogr.Error(err, "Failed to delete test namespace")
		}
	})

	Context("Basic StatefulSingleton functionality", func() {
		var (
			singletonName  = "test-singleton"
			deploymentName = "test-deployment"
		)

		AfterEach(func() {
			// Clean up resources after each test
			By("cleaning up test resources")

			// Delete StatefulSingleton
			ss := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      singletonName,
					Namespace: testNamespace,
				},
			}
			_ = k8sClient.Delete(ctx, ss)

			// Delete Deployment
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
			}
			_ = k8sClient.Delete(ctx, deployment)

			// Delete any remaining pods
			podList := &corev1.PodList{}
			_ = k8sClient.List(ctx, podList, client.InNamespace(testNamespace))
			for _, pod := range podList.Items {
				_ = k8sClient.Delete(ctx, &pod)
			}

			// Wait for cleanup to complete
			time.Sleep(5 * time.Second)
		})

		It("should create and manage a simple StatefulSingleton with deployment", func() {
			labels := map[string]string{
				"app":        "nginx",
				"managed-by": "statefulsingleton",
			}

			By("creating a StatefulSingleton resource")
			singleton := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      singletonName,
					Namespace: testNamespace,
				},
				Spec: statefulsingleton.StatefulSingletonSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: labels,
					},
					TerminationGracePeriod: 30,
					MaxTransitionTime:      120,
					RespectPodGracePeriod:  true,
				},
			}
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating a deployment with matching labels")
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "nginx",
									Image: "nginx:1.21",
									Ports: []corev1.ContainerPort{
										{ContainerPort: 80},
									},
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceMemory: resource.MustParse("64Mi"),
											corev1.ResourceCPU:    resource.MustParse("50m"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceMemory: resource.MustParse("128Mi"),
											corev1.ResourceCPU:    resource.MustParse("100m"),
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for the StatefulSingleton to show Running status")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 2*time.Minute, 5*time.Second).Should(Equal("Running"))

			By("verifying the pod was created and managed")
			Eventually(func() int {
				var podList corev1.PodList
				err := k8sClient.List(ctx, &podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels(labels))
				if err != nil {
					return 0
				}
				return len(podList.Items)
			}, 1*time.Minute, 5*time.Second).Should(Equal(1))

			By("verifying the pod has the required readiness gate")
			var pod corev1.Pod
			Eventually(func() error {
				var podList corev1.PodList
				err := k8sClient.List(ctx, &podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels(labels))
				if err != nil || len(podList.Items) == 0 {
					return fmt.Errorf("pod not found")
				}
				pod = podList.Items[0]
				return nil
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			// Check readiness gate
			var hasReadinessGate bool
			for _, gate := range pod.Spec.ReadinessGates {
				if string(gate.ConditionType) == "statefulsingleton.com/singleton-ready" {
					hasReadinessGate = true
					break
				}
			}
			Expect(hasReadinessGate).To(BeTrue(), "Pod should have StatefulSingleton readiness gate")

			By("verifying the pod has the wrapper script volume")
			var hasWrapperVolume bool
			for _, volume := range pod.Spec.Volumes {
				if volume.Name == "wrapper-scripts" {
					hasWrapperVolume = true
					break
				}
			}
			Expect(hasWrapperVolume).To(BeTrue(), "Pod should have wrapper-scripts volume")

			By("verifying the pod has the status sidecar container")
			var hasSidecar bool
			for _, container := range pod.Spec.Containers {
				if container.Name == "status-sidecar" {
					hasSidecar = true
					break
				}
			}
			Expect(hasSidecar).To(BeTrue(), "Pod should have status-sidecar container")

			By("verifying the StatefulSingleton status shows the active pod")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.ActivePod
			}, 30*time.Second, 2*time.Second).Should(Equal(pod.Name))
		})

		It("should handle rolling updates correctly", func() {
			labels := map[string]string{
				"app": "nginx-rolling",
			}

			By("creating initial StatefulSingleton and deployment")
			// Create StatefulSingleton
			singleton := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      singletonName,
					Namespace: testNamespace,
				},
				Spec: statefulsingleton.StatefulSingletonSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: labels,
					},
					TerminationGracePeriod: 10,
					MaxTransitionTime:      60,
				},
			}
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			// Create deployment with initial image
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Strategy: appsv1.DeploymentStrategy{
						Type: appsv1.RecreateDeploymentStrategyType,
					},
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "nginx",
									Image: "nginx:1.20", // Initial image
									Ports: []corev1.ContainerPort{
										{ContainerPort: 80},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for initial deployment to be ready")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 2*time.Minute, 5*time.Second).Should(Equal("Running"))

			// Get the initial pod name
			var initialPodName string
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				initialPodName = ss.Status.ActivePod
				return initialPodName
			}, 30*time.Second, 2*time.Second).ShouldNot(BeEmpty())

			By("triggering a rolling update")
			// Update the deployment image
			Eventually(func() error {
				var currentDeployment appsv1.Deployment
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: deploymentName, Namespace: testNamespace,
				}, &currentDeployment)
				if err != nil {
					return err
				}

				// Update the image
				currentDeployment.Spec.Template.Spec.Containers[0].Image = "nginx:1.21"
				return k8sClient.Update(ctx, &currentDeployment)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying transition phase is entered")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 1*time.Minute, 2*time.Second).Should(Equal("Transitioning"))

			By("verifying transition completes successfully")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 3*time.Minute, 5*time.Second).Should(Equal("Running"))

			By("verifying the new pod is different from the initial pod")
			var newPodName string
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				newPodName = ss.Status.ActivePod
				return newPodName
			}, 30*time.Second, 2*time.Second).ShouldNot(Equal(initialPodName))

			By("verifying the new pod is running the updated image")
			Eventually(func() string {
				var pod corev1.Pod
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: newPodName, Namespace: testNamespace,
				}, &pod)
				if err != nil {
					return ""
				}
				if len(pod.Spec.Containers) == 0 {
					return ""
				}
				return pod.Spec.Containers[0].Image
			}, 30*time.Second, 2*time.Second).Should(Equal("nginx:1.21"))
		})

		It("should handle pod failures and replacements", func() {
			labels := map[string]string{
				"app": "nginx-failure",
			}

			By("creating StatefulSingleton and deployment")
			singleton := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      singletonName,
					Namespace: testNamespace,
				},
				Spec: statefulsingleton.StatefulSingletonSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: labels,
					},
					TerminationGracePeriod: 5,
					MaxTransitionTime:      30,
				},
			}
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "nginx",
									Image: "nginx:1.21",
									Ports: []corev1.ContainerPort{
										{ContainerPort: 80},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for initial pod to be ready")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 2*time.Minute, 5*time.Second).Should(Equal("Running"))

			// Get initial pod name
			var initialPodName string
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				initialPodName = ss.Status.ActivePod
				return initialPodName
			}, 30*time.Second, 2*time.Second).ShouldNot(BeEmpty())

			By("deleting the active pod to simulate failure")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      initialPodName,
					Namespace: testNamespace,
				},
			}
			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())

			By("verifying a new pod is created and becomes active")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.ActivePod
			}, 2*time.Minute, 5*time.Second).ShouldNot(Equal(initialPodName))

			By("verifying the StatefulSingleton returns to Running state")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 1*time.Minute, 5*time.Second).Should(Equal("Running"))
		})
	})

	Context("ConfigMap management", func() {
		var singletonName = "configmap-test-singleton"

		AfterEach(func() {
			By("cleaning up configmap test resources")
			// Delete StatefulSingleton
			ss := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      singletonName,
					Namespace: testNamespace,
				},
			}
			_ = k8sClient.Delete(ctx, ss)

			// Delete ConfigMap
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "statefulsingleton-wrapper",
					Namespace: testNamespace,
				},
			}
			_ = k8sClient.Delete(ctx, cm)
		})

		It("should create and manage the wrapper ConfigMap", func() {
			By("creating a StatefulSingleton")
			singleton := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      singletonName,
					Namespace: testNamespace,
				},
				Spec: statefulsingleton.StatefulSingletonSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "configmap-test",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("waiting for the controller to process the StatefulSingleton")
			time.Sleep(10 * time.Second)

			By("verifying the wrapper ConfigMap was created")
			var configMap corev1.ConfigMap
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "statefulsingleton-wrapper", Namespace: testNamespace,
				}, &configMap)
			}, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the ConfigMap contains the wrapper script")
			Expect(configMap.Data).To(HaveKey("entrypoint-wrapper.sh"))
			wrapperScript := configMap.Data["entrypoint-wrapper.sh"]
			Expect(wrapperScript).To(ContainSubstring("StatefulSingleton"))
			Expect(wrapperScript).To(ContainSubstring("start-signal"))
			Expect(wrapperScript).To(ContainSubstring("ORIGINAL_ENTRYPOINT"))
		})
	})

	Context("Error handling and edge cases", func() {
		It("should handle StatefulSingleton with minimal selector gracefully", func() {
			By("creating StatefulSingleton with minimal selector")
			singleton := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "minimal-singleton",
					Namespace: testNamespace,
				},
				Spec: statefulsingleton.StatefulSingletonSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							// Minimal selector
							"test": "minimal",
						},
					},
					TerminationGracePeriod: 30,
				},
			}
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("verifying the StatefulSingleton is created but shows no active pods")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: "minimal-singleton", Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 30*time.Second, 5*time.Second).Should(Equal("Running"))

			var ss statefulsingleton.StatefulSingleton
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "minimal-singleton", Namespace: testNamespace,
			}, &ss)).To(Succeed())
			Expect(ss.Status.Message).To(Equal("No pods found"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, &ss)).To(Succeed())
		})

		It("should respect custom termination grace periods", func() {
			labels := map[string]string{
				"app": "custom-grace-test",
			}

			By("creating StatefulSingleton with custom grace period")
			singleton := &statefulsingleton.StatefulSingleton{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-grace-singleton",
					Namespace: testNamespace,
				},
				Spec: statefulsingleton.StatefulSingletonSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: labels,
					},
					TerminationGracePeriod: 60,
					RespectPodGracePeriod:  false,
				},
			}
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating deployment with different grace period")
			gracePeriod := int64(30)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-grace-deployment",
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							TerminationGracePeriodSeconds: &gracePeriod, // 30 seconds
							Containers: []corev1.Container{
								{
									Name:  "nginx",
									Image: "nginx:1.21",
									Ports: []corev1.ContainerPort{
										{ContainerPort: 80},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("verifying the pod gets the StatefulSingleton's grace period")
			Eventually(func() *int64 {
				var podList corev1.PodList
				err := k8sClient.List(ctx, &podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels(labels))
				if err != nil || len(podList.Items) == 0 {
					return nil
				}
				return podList.Items[0].Spec.TerminationGracePeriodSeconds
			}, 1*time.Minute, 5*time.Second).Should(Equal(int64Ptr(60)))

			// Cleanup
			Expect(k8sClient.Delete(ctx, singleton)).To(Succeed())
			Expect(k8sClient.Delete(ctx, deployment)).To(Succeed())
		})
	})
})

// Helper functions
func int32Ptr(i int32) *int32 {
	return &i
}

func int64Ptr(i int64) *int64 {
	return &i
}
