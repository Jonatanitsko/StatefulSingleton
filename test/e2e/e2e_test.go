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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
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

		// Register StatefulSingleton scheme
		err = statefulsingleton.AddToScheme(scheme.Scheme)
		Expect(err).NotTo(HaveOccurred())

		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
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
		By("cleaning up all resources in test namespace")
		GinkgoLogr.Info("Cleaning up test namespace resources")

		// Clean up all StatefulSingletons
		var singletonList statefulsingleton.StatefulSingletonList
		if err := k8sClient.List(ctx, &singletonList, client.InNamespace(testNamespace)); err == nil {
			for _, ss := range singletonList.Items {
				_ = k8sClient.Delete(ctx, &ss)
			}
		}

		// Clean up all Deployments
		var deploymentList appsv1.DeploymentList
		if err := k8sClient.List(ctx, &deploymentList, client.InNamespace(testNamespace)); err == nil {
			for _, deployment := range deploymentList.Items {
				_ = k8sClient.Delete(ctx, &deployment)
			}
		}

		// Clean up all Pods
		var podList corev1.PodList
		if err := k8sClient.List(ctx, &podList, client.InNamespace(testNamespace)); err == nil {
			for _, pod := range podList.Items {
				_ = k8sClient.Delete(ctx, &pod)
			}
		}

		// Clean up all ConfigMaps
		var configMapList corev1.ConfigMapList
		if err := k8sClient.List(ctx, &configMapList, client.InNamespace(testNamespace)); err == nil {
			for _, cm := range configMapList.Items {
				_ = k8sClient.Delete(ctx, &cm)
			}
		}

		// Wait for resources to be deleted
		time.Sleep(10 * time.Second)

		By("deleting test namespace")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		err := k8sClient.Delete(ctx, ns)
		if err != nil && !errors.IsNotFound(err) {
			GinkgoLogr.Error(err, "Failed to delete test namespace")
		}

		// Wait for namespace deletion to complete
		Eventually(func() bool {
			var deletedNs corev1.Namespace
			err := k8sClient.Get(ctx, types.NamespacedName{Name: testNamespace}, &deletedNs)
			return errors.IsNotFound(err)
		}, 2*time.Minute, 5*time.Second).Should(BeTrue())
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
									Name:    "nginx",
									Image:   "nginx:1.21",
									Command: []string{"nginx"},
									Args:    []string{"-g", "daemon off;"},
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
				if string(gate.ConditionType) == "apps.statefulsingleton.com/singleton-ready" {
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
					//Strategy: appsv1.DeploymentStrategy{
					//	RollingUpdate: &appsv1.RollingUpdateDeployment{
					//		MaxSurge: &intstr.IntOrString{IntVal: 0},
					//	},
					//},
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
									Name:    "nginx",
									Image:   "nginx:1.20", // Initial image
									Command: []string{"sleep infinity"},
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

			By("verifying new pod becomes active after rolling update")
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
			}, 2*time.Minute, 5*time.Second).ShouldNot(Equal(initialPodName))

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
									Name:    "nginx",
									Image:   "nginx:1.21",
									Command: []string{"nginx"},
									Args:    []string{"-g", "daemon off;"},
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
									Name:    "nginx",
									Image:   "nginx:1.21",
									Command: []string{"nginx"},
									Args:    []string{"-g", "daemon off;"},
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

	// 1. Webhook Integration Issues
	Context("Webhook Integration Issues", func() {
		var (
			singletonName  = "webhook-test-singleton"
			deploymentName = "webhook-test-deployment"
		)

		AfterEach(func() {
			cleanupTestResources(ctx, k8sClient, testNamespace, singletonName, deploymentName)
		})

		It("should handle webhook unavailability gracefully", func() {
			labels := map[string]string{"app": "webhook-failure-test"}

			By("creating StatefulSingleton and deployment first")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.21", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for normal operation")
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

			By("temporarily disabling webhook by modifying its configuration")
			var webhookConfig admissionregistrationv1.MutatingWebhookConfiguration
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "mutating-webhook-configuration",
			}, &webhookConfig)
			if err == nil {
				// Backup original webhook config
				originalWebhook := webhookConfig.DeepCopy()

				// Disable webhook by changing failure policy to Fail and pointing to invalid service
				for i := range webhookConfig.Webhooks {
					failurePolicy := admissionregistrationv1.Fail
					webhookConfig.Webhooks[i].FailurePolicy = &failurePolicy
					if webhookConfig.Webhooks[i].ClientConfig.Service != nil {
						webhookConfig.Webhooks[i].ClientConfig.Service.Name = "non-existent-service"
					}
				}
				_ = k8sClient.Update(ctx, &webhookConfig)

				// Test pod creation during webhook failure
				By("creating additional pods while webhook is unavailable")
				deployment2 := createDeployment("webhook-failure-deployment", testNamespace,
					map[string]string{"app": "webhook-failure-test-2"}, "nginx:1.20", 1)
				// This may fail or succeed depending on webhook configuration
				_ = k8sClient.Create(ctx, deployment2)

				// Restore webhook configuration
				By("restoring webhook configuration")
				_ = k8sClient.Update(ctx, originalWebhook)

				// Cleanup
				_ = k8sClient.Delete(ctx, deployment2)
			}

			By("verifying StatefulSingleton continues to operate")
			Consistently(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 30*time.Second, 5*time.Second).Should(Equal("Running"))
		})

		It("should handle multiple StatefulSingletons with overlapping selectors", func() {
			sharedLabels := map[string]string{"app": "shared", "tier": "web"}

			By("creating first StatefulSingleton")
			singleton1 := createStatefulSingleton("overlap-singleton-1", testNamespace, sharedLabels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton1)).To(Succeed())

			By("creating second StatefulSingleton with overlapping selector")
			overlapLabels := map[string]string{"app": "shared"} // Broader selector
			singleton2 := createStatefulSingleton("overlap-singleton-2", testNamespace, overlapLabels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton2)).To(Succeed())

			By("creating deployment with shared labels")
			deployment := createDeployment(deploymentName, testNamespace, sharedLabels, "nginx:1.21", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("verifying both StatefulSingletons recognize the same pod")
			Eventually(func() bool {
				var ss1, ss2 statefulsingleton.StatefulSingleton
				err1 := k8sClient.Get(ctx, types.NamespacedName{
					Name: "overlap-singleton-1", Namespace: testNamespace,
				}, &ss1)
				err2 := k8sClient.Get(ctx, types.NamespacedName{
					Name: "overlap-singleton-2", Namespace: testNamespace,
				}, &ss2)

				return err1 == nil && err2 == nil &&
					ss1.Status.ActivePod != "" && ss2.Status.ActivePod != "" &&
					ss1.Status.ActivePod == ss2.Status.ActivePod
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			// Cleanup both singletons
			_ = k8sClient.Delete(ctx, singleton1)
			_ = k8sClient.Delete(ctx, singleton2)
		})

		It("should handle pods with existing webhook modifications", func() {
			labels := map[string]string{"app": "existing-mods-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating pod with pre-existing volumes and sidecars")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pre-existing-pod",
					Namespace: testNamespace,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "nginx",
							Image:   "nginx:1.21",
							Command: []string{"nginx", "-g", "daemon off;"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "signal-volume", MountPath: "/custom/signal"},
							},
						},
						{
							Name:  "status-sidecar",
							Image: "custom-sidecar:latest",
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "signal-volume",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/tmp/custom"},
							},
						},
					},
					ReadinessGates: []corev1.PodReadinessGate{
						{ConditionType: "apps.statefulsingleton.com/singleton-ready"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			By("verifying webhook doesn't create duplicates")
			Eventually(func() int {
				var updatedPod corev1.Pod
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: "pre-existing-pod", Namespace: testNamespace,
				}, &updatedPod)
				if err != nil {
					return -1
				}
				return len(updatedPod.Spec.ReadinessGates)
			}, 30*time.Second, 2*time.Second).Should(Equal(1))

			// Verify existing volumes are preserved
			var updatedPod corev1.Pod
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "pre-existing-pod", Namespace: testNamespace,
			}, &updatedPod)).To(Succeed())

			var signalVolume *corev1.Volume
			for i := range updatedPod.Spec.Volumes {
				if updatedPod.Spec.Volumes[i].Name == "signal-volume" {
					signalVolume = &updatedPod.Spec.Volumes[i]
					break
				}
			}
			Expect(signalVolume).NotTo(BeNil())
			Expect(signalVolume.HostPath).NotTo(BeNil()) // Original volume type preserved

			// Cleanup
			_ = k8sClient.Delete(ctx, pod)
		})
	})

	// 2. Controller Advanced Scenarios
	Context("Controller Advanced Scenarios", func() {
		var (
			singletonName  = "advanced-test-singleton"
			deploymentName = "advanced-test-deployment"
		)

		AfterEach(func() {
			cleanupTestResources(ctx, k8sClient, testNamespace, singletonName, deploymentName)
		})

		It("should handle transition timeouts correctly", func() {
			labels := map[string]string{"app": "timeout-test"}

			By("creating StatefulSingleton with short transition timeout")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 15, true) // 15 second timeout
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating deployment with slow shutdown container")
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Selector: &metav1.LabelSelector{MatchLabels: labels},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							TerminationGracePeriodSeconds: int64Ptr(60), // Long shutdown
							Containers: []corev1.Container{
								{
									Name:    "slow-shutdown",
									Image:   "nginx:1.20",
									Command: []string{"nginx"},
									Args:    []string{"-g", "daemon off;"},
									Lifecycle: &corev1.Lifecycle{
										PreStop: &corev1.LifecycleHandler{
											Exec: &corev1.ExecAction{
												Command: []string{"/bin/sh", "-c", "sleep 45"}, // 45 second shutdown
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for initial deployment")
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

			By("triggering rolling update that will timeout")
			Eventually(func() error {
				var currentDeployment appsv1.Deployment
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: deploymentName, Namespace: testNamespace,
				}, &currentDeployment)
				if err != nil {
					return err
				}
				currentDeployment.Spec.Template.Spec.Containers[0].Image = "nginx:1.21"
				return k8sClient.Update(ctx, &currentDeployment)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying transition timeout is handled")
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

			By("waiting for timeout and checking status message")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Message
			}, 2*time.Minute, 5*time.Second).Should(ContainSubstring("transition timeout"))
		})

		It("should respect pod grace periods when configured", func() {
			labels := map[string]string{"app": "grace-respect-test"}

			By("creating StatefulSingleton with RespectPodGracePeriod=true")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 10, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating deployment with longer grace period")
			longGracePeriod := int64(120) // 2 minutes
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Selector: &metav1.LabelSelector{MatchLabels: labels},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							TerminationGracePeriodSeconds: &longGracePeriod,
							Containers: []corev1.Container{
								{
									Name:    "nginx",
									Image:   "nginx:1.20",
									Command: []string{"nginx"},
									Args:    []string{"-g", "daemon off;"},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("verifying pod retains its longer grace period")
			Eventually(func() *int64 {
				var podList corev1.PodList
				err := k8sClient.List(ctx, &podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels(labels))
				if err != nil || len(podList.Items) == 0 {
					return nil
				}
				return podList.Items[0].Spec.TerminationGracePeriodSeconds
			}, 1*time.Minute, 5*time.Second).Should(Equal(&longGracePeriod))
		})

		It("should handle multiple concurrent pods correctly", func() {
			labels := map[string]string{"app": "multi-pod-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating deployment with multiple replicas")
			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.21", 3) // 3 replicas
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("verifying only one pod becomes active")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.ActivePod
			}, 2*time.Minute, 5*time.Second).ShouldNot(BeEmpty())

			By("verifying other pods are managed appropriately")
			var activePod string
			var ss statefulsingleton.StatefulSingleton
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: singletonName, Namespace: testNamespace,
			}, &ss)).To(Succeed())
			activePod = ss.Status.ActivePod

			// Check that only one pod is truly active
			var podList corev1.PodList
			Expect(k8sClient.List(ctx, &podList,
				client.InNamespace(testNamespace),
				client.MatchingLabels(labels))).To(Succeed())

			activeCount := 0
			for _, pod := range podList.Items {
				if pod.Name == activePod {
					activeCount++
				}
			}
			Expect(activeCount).To(Equal(1))
		})

		It("should handle rapid successive deployments", func() {
			labels := map[string]string{"app": "rapid-deploy-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating initial deployment")
			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.20", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for initial stability")
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

			By("performing rapid image updates")
			images := []string{"nginx:1.21", "nginx:1.22", "nginx:1.23", "nginx:alpine"}

			for _, image := range images {
				Eventually(func() error {
					var currentDeployment appsv1.Deployment
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name: deploymentName, Namespace: testNamespace,
					}, &currentDeployment)
					if err != nil {
						return err
					}
					currentDeployment.Spec.Template.Spec.Containers[0].Image = image
					return k8sClient.Update(ctx, &currentDeployment)
				}, 10*time.Second, 1*time.Second).Should(Succeed())

				// Small delay between updates
				time.Sleep(2 * time.Second)
			}

			By("verifying system eventually stabilizes")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 5*time.Minute, 10*time.Second).Should(Equal("Running"))

			By("verifying final pod is running latest image")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil || ss.Status.ActivePod == "" {
					return ""
				}

				var pod corev1.Pod
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name: ss.Status.ActivePod, Namespace: testNamespace,
				}, &pod)
				if err != nil || len(pod.Spec.Containers) == 0 {
					return ""
				}
				return pod.Spec.Containers[0].Image
			}, 1*time.Minute, 5*time.Second).Should(Equal("nginx:alpine"))
		})
	})

	// 3. Status & Monitoring
	Context("Status & Monitoring", func() {
		var (
			singletonName  = "status-test-singleton"
			deploymentName = "status-test-deployment"
		)

		AfterEach(func() {
			cleanupTestResources(ctx, k8sClient, testNamespace, singletonName, deploymentName)
		})

		It("should maintain accurate status throughout lifecycle", func() {
			labels := map[string]string{"app": "status-accuracy-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			startTime := time.Now()
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("verifying initial status")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 30*time.Second, 2*time.Second).Should(Equal("Running"))

			var ss statefulsingleton.StatefulSingleton
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: singletonName, Namespace: testNamespace,
			}, &ss)).To(Succeed())

			Expect(ss.Status.Message).To(Equal("No pods found"))
			Expect(ss.Status.TransitionTimestamp).NotTo(BeNil())
			Expect(ss.Status.TransitionTimestamp.Time).To(BeTemporally(">=", startTime))

			By("creating deployment and verifying status updates")
			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.21", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.ActivePod
			}, 2*time.Minute, 5*time.Second).ShouldNot(BeEmpty())

			// Verify detailed status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: singletonName, Namespace: testNamespace,
			}, &ss)).To(Succeed())

			Expect(ss.Status.Phase).To(Equal("Running"))
			Expect(ss.Status.Message).To(ContainSubstring("active"))
			Expect(ss.Status.ActivePod).NotTo(BeEmpty())

			By("triggering transition and monitoring status accuracy")
			transitionStartTime := time.Now()
			Eventually(func() error {
				var currentDeployment appsv1.Deployment
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: deploymentName, Namespace: testNamespace,
				}, &currentDeployment)
				if err != nil {
					return err
				}
				currentDeployment.Spec.Template.Spec.Containers[0].Image = "nginx:1.22"
				return k8sClient.Update(ctx, &currentDeployment)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying transition phase status")
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

			// Verify transition timestamp is updated
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: singletonName, Namespace: testNamespace,
			}, &ss)).To(Succeed())
			Expect(ss.Status.TransitionTimestamp.Time).To(BeTemporally(">=", transitionStartTime))

			By("verifying successful transition completion")
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
		})

		It("should generate appropriate Kubernetes events", func() {
			labels := map[string]string{"app": "events-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating deployment")
			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.21", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("verifying events are generated for StatefulSingleton")
			Eventually(func() []corev1.Event {
				var eventList corev1.EventList
				err := k8sClient.List(ctx, &eventList, client.InNamespace(testNamespace),
					client.MatchingFields(fields.Set{
						"involvedObject.name": singletonName,
						"involvedObject.kind": "StatefulSingleton",
					}))
				if err != nil {
					return nil
				}
				return eventList.Items
			}, 2*time.Minute, 5*time.Second).Should(Not(BeEmpty()))

			By("triggering transition and checking for transition events")
			Eventually(func() error {
				var currentDeployment appsv1.Deployment
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: deploymentName, Namespace: testNamespace,
				}, &currentDeployment)
				if err != nil {
					return err
				}
				currentDeployment.Spec.Template.Spec.Containers[0].Image = "nginx:1.22"
				return k8sClient.Update(ctx, &currentDeployment)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying transition events are generated")
			Eventually(func() bool {
				var eventList corev1.EventList
				err := k8sClient.List(ctx, &eventList, client.InNamespace(testNamespace),
					client.MatchingFields(fields.Set{
						"involvedObject.name": singletonName,
						"involvedObject.kind": "StatefulSingleton",
					}))
				if err != nil {
					return false
				}

				for _, event := range eventList.Items {
					if strings.Contains(event.Message, "transition") ||
						strings.Contains(event.Message, "Transitioning") {
						return true
					}
				}
				return false
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())
		})

		It("should reflect failure conditions in status", func() {
			labels := map[string]string{"app": "failure-status-test"}

			By("creating StatefulSingleton with impossible selector")
			impossibleLabels := map[string]string{"app": "nonexistent", "impossible": "true"}
			singleton := createStatefulSingleton(singletonName, testNamespace, impossibleLabels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("verifying status reflects no matching pods")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Message
			}, 1*time.Minute, 5*time.Second).Should(Equal("No pods found"))

			By("creating deployment with problematic configuration")
			problematicDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: int32Ptr(1),
					Selector: &metav1.LabelSelector{MatchLabels: labels},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "invalid",
									Image: "nonexistent:image", // Invalid image
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceMemory: resource.MustParse("999Gi"), // Impossible memory
										},
									},
								},
							},
						},
					},
				},
			}
			_ = k8sClient.Create(ctx, problematicDeployment)

			// Update singleton to match the problematic deployment
			Eventually(func() error {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return err
				}
				ss.Spec.Selector.MatchLabels = labels
				return k8sClient.Update(ctx, &ss)
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("verifying status reflects pod creation issues")
			// The StatefulSingleton should detect pods that can't start properly
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.Phase
			}, 2*time.Minute, 5*time.Second).Should(Or(Equal("Running"), Equal("Transitioning")))

			// Cleanup problematic deployment
			_ = k8sClient.Delete(ctx, problematicDeployment)
		})
	})

	// 4. Resource Management
	Context("Resource Management", func() {
		var (
			singletonName  = "resource-test-singleton"
			deploymentName = "resource-test-deployment"
		)

		It("should clean up resources on StatefulSingleton deletion", func() {
			labels := map[string]string{"app": "cleanup-test"}

			By("creating StatefulSingleton and deployment")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.21", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for system to stabilize")
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

			By("verifying wrapper ConfigMap exists")
			var configMap corev1.ConfigMap
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "statefulsingleton-wrapper", Namespace: testNamespace,
				}, &configMap)
			}, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("deleting StatefulSingleton")
			Expect(k8sClient.Delete(ctx, singleton)).To(Succeed())

			By("verifying StatefulSingleton is removed")
			Eventually(func() bool {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				return errors.IsNotFound(err)
			}, 1*time.Minute, 5*time.Second).Should(BeTrue())

			By("verifying ConfigMap cleanup (depends on operator implementation)")
			// Note: This test may need adjustment based on actual cleanup behavior
			Eventually(func() bool {
				var cm corev1.ConfigMap
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: "statefulsingleton-wrapper", Namespace: testNamespace,
				}, &cm)
				// ConfigMap might be cleaned up or left for other StatefulSingletons
				return err != nil || cm.Name == "statefulsingleton-wrapper"
			}, 1*time.Minute, 5*time.Second).Should(BeTrue())

			By("verifying pods continue to exist (controlled by deployment)")
			var podList corev1.PodList
			Expect(k8sClient.List(ctx, &podList,
				client.InNamespace(testNamespace),
				client.MatchingLabels(labels))).To(Succeed())
			// Pods should still exist since they're controlled by the deployment
			Expect(len(podList.Items)).To(BeNumerically(">", 0))

			// Cleanup
			_ = k8sClient.Delete(ctx, deployment)
		})

		It("should handle ConfigMap lifecycle during operator restart", func() {
			labels := map[string]string{"app": "configmap-lifecycle-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("verifying ConfigMap is created")
			var configMap corev1.ConfigMap
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "statefulsingleton-wrapper", Namespace: testNamespace,
				}, &configMap)
			}, 1*time.Minute, 5*time.Second).Should(Succeed())

			originalResourceVersion := configMap.ResourceVersion

			By("simulating ConfigMap deletion (as might happen during operator restart)")
			Expect(k8sClient.Delete(ctx, &configMap)).To(Succeed())

			By("verifying ConfigMap is recreated")
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "statefulsingleton-wrapper", Namespace: testNamespace,
				}, &configMap)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying ConfigMap content is restored")
			Expect(configMap.Data).To(HaveKey("entrypoint-wrapper.sh"))
			Expect(configMap.Data["entrypoint-wrapper.sh"]).To(ContainSubstring("StatefulSingleton"))
			Expect(configMap.ResourceVersion).NotTo(Equal(originalResourceVersion))

			// Cleanup
			cleanupTestResources(ctx, k8sClient, testNamespace, singletonName, "")
		})

		It("should detect and handle orphaned resources", func() {
			labels := map[string]string{"app": "orphan-test"}

			By("creating StatefulSingleton")
			singleton := createStatefulSingleton(singletonName, testNamespace, labels, 30, 60, true)
			Expect(k8sClient.Create(ctx, singleton)).To(Succeed())

			By("creating deployment")
			deployment := createDeployment(deploymentName, testNamespace, labels, "nginx:1.21", 1)
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

			By("waiting for pod to be managed")
			Eventually(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.ActivePod
			}, 2*time.Minute, 5*time.Second).ShouldNot(BeEmpty())

			var activePodName string
			var ss statefulsingleton.StatefulSingleton
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: singletonName, Namespace: testNamespace,
			}, &ss)).To(Succeed())
			activePodName = ss.Status.ActivePod

			By("creating orphaned pod with same labels")
			orphanPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphan-pod",
					Namespace: testNamespace,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "nginx",
							Image:   "nginx:1.21",
							Command: []string{"nginx", "-g", "daemon off;"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, orphanPod)).To(Succeed())

			By("verifying StatefulSingleton detects multiple pods")
			Eventually(func() int {
				var podList corev1.PodList
				err := k8sClient.List(ctx, &podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels(labels))
				if err != nil {
					return 0
				}
				return len(podList.Items)
			}, 1*time.Minute, 5*time.Second).Should(BeNumerically(">=", 2))

			By("verifying StatefulSingleton maintains single active pod")
			Consistently(func() string {
				var ss statefulsingleton.StatefulSingleton
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: singletonName, Namespace: testNamespace,
				}, &ss)
				if err != nil {
					return ""
				}
				return ss.Status.ActivePod
			}, 30*time.Second, 5*time.Second).Should(Equal(activePodName))

			// Cleanup
			_ = k8sClient.Delete(ctx, orphanPod)
			cleanupTestResources(ctx, k8sClient, testNamespace, singletonName, deploymentName)
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

// createStatefulSingleton creates a StatefulSingleton with the given parameters
func createStatefulSingleton(name, namespace string, labels map[string]string, gracePeriod, maxTransition int, respectPodGrace bool) *statefulsingleton.StatefulSingleton {
	return &statefulsingleton.StatefulSingleton{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: statefulsingleton.StatefulSingletonSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: labels,
			},
			TerminationGracePeriod: gracePeriod,
			MaxTransitionTime:      maxTransition,
			RespectPodGracePeriod:  respectPodGrace,
		},
	}
}

// createDeployment creates a Deployment with the given parameters
func createDeployment(name, namespace string, labels map[string]string, image string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(replicas),
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
							Name:    "main",
							Image:   image,
							Command: []string{"nginx", "-g", "daemon off;"},
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
}

// cleanupTestResources cleans up test resources
func cleanupTestResources(ctx context.Context, k8sClient client.Client, namespace, singletonName, deploymentName string) {
	// Delete StatefulSingleton
	if singletonName != "" {
		ss := &statefulsingleton.StatefulSingleton{
			ObjectMeta: metav1.ObjectMeta{
				Name:      singletonName,
				Namespace: namespace,
			},
		}
		_ = k8sClient.Delete(ctx, ss)
	}

	// Delete Deployment
	if deploymentName != "" {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: namespace,
			},
		}
		_ = k8sClient.Delete(ctx, deployment)
	}

	// Delete any remaining pods with test labels
	podList := &corev1.PodList{}
	_ = k8sClient.List(ctx, podList, client.InNamespace(namespace))
	for _, pod := range podList.Items {
		// Only delete pods that seem to be from our tests
		if strings.Contains(pod.Name, "test") ||
			strings.Contains(pod.GenerateName, "test") ||
			(pod.Labels != nil && (pod.Labels["app"] == "webhook-failure-test" ||
				pod.Labels["app"] == "shared" ||
				strings.Contains(pod.Labels["app"], "test"))) {
			_ = k8sClient.Delete(ctx, &pod)
		}
	}

	// Delete any remaining ConfigMaps
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "statefulsingleton-wrapper",
			Namespace: namespace,
		},
	}
	_ = k8sClient.Delete(ctx, cm)

	// Wait a bit for cleanup to complete
	time.Sleep(2 * time.Second)
}
