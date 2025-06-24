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
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	appsv1 "github.com/Jonatanitsko/StatefulSingleton.git/api/v1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	testMgr   manager.Manager
)

// TestControllers is the entry point for running the controller test suite
// This function is called by "go test" and sets up Ginkgo to run our BDD-style tests
func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

// BeforeSuite runs once before all tests in this package
// It sets up the test environment, including a fake Kubernetes API server
var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	// Register our custom API types with the scheme
	// This tells the Kubernetes client how to serialize/deserialize our StatefulSingleton resources
	var err error
	err = appsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// Also register core Kubernetes types (Pod, etc.)
	err = corev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	// envtest creates a real Kubernetes API server for testing
	// but without the full cluster (no kubelet, scheduler, etc.)
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// Start the test environment (starts etcd and kube-apiserver)
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Create a Kubernetes client that talks to our test API server
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	metricsServerOptions := metricsserver.Options{
		BindAddress: "0",
	}

	// Create a manager that will run our controller
	// We don't start it here - individual tests can choose whether to start it
	testMgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsServerOptions, // Disable metrics server in tests
	})
	Expect(err).NotTo(HaveOccurred())

	// Register our controller with the manager
	err = (&StatefulSingletonReconciler{
		Client:   testMgr.GetClient(),
		Scheme:   testMgr.GetScheme(),
		Recorder: testMgr.GetEventRecorderFor("statefulsingleton-controller"),
	}).SetupWithManager(testMgr)
	Expect(err).NotTo(HaveOccurred())
})

// AfterSuite runs once after all tests in this package
// It cleans up the test environment
var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// Helper function to create a test namespace
func createTestNamespace(name string) *corev1.Namespace {
	ns := &corev1.Namespace{}
	ns.Name = name
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns
}

// Helper function to create a basic StatefulSingleton resource for testing
func createBasicStatefulSingleton(name, namespace string, selector map[string]string) *appsv1.StatefulSingleton {
	stss := &appsv1.StatefulSingleton{}
	stss.Name = name
	stss.Namespace = namespace
	stss.Spec.Selector.MatchLabels = selector
	stss.Spec.MaxTransitionTime = 300
	stss.Spec.TerminationGracePeriod = 30
	stss.Spec.RespectPodGracePeriod = true

	Expect(k8sClient.Create(ctx, stss)).To(Succeed())
	return stss
}

// Helper function to create a test pod with specific labels
func createTestPod(name, namespace string, labels map[string]string, hasReadinessGate bool) *corev1.Pod {
	pod := &corev1.Pod{}
	pod.Name = name
	pod.Namespace = namespace
	pod.Labels = labels

	// Basic pod spec
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "test-container",
			Image: "nginx:latest",
		},
	}

	// Add readiness gate if requested (simulates webhook injection)
	if hasReadinessGate {
		pod.Spec.ReadinessGates = []corev1.PodReadinessGate{
			{
				ConditionType: "apps.statefulsingleton.com/singleton-ready",
			},
		}
	}

	Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	// Set pod to Running status
	pod.Status.Phase = corev1.PodRunning
	// Initialize conditions slice
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionFalse, // Start as not ready
		},
	}

	// Add the singleton readiness condition if we have the gate
	if hasReadinessGate {
		pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
			Type:    "apps.statefulsingleton.com/singleton-ready",
			Status:  corev1.ConditionFalse,
			Message: "Waiting for singleton controller",
		})
	}

	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
	return pod
}

// Helper function to eventually check a condition with proper timeout
func eventuallyWithTimeout(check func() error, timeout time.Duration) {
	Eventually(check, timeout, 100*time.Millisecond).Should(Succeed())
}

// Helper function to start the manager in a goroutine for tests that need it
func startTestManager() {
	go func() {
		defer GinkgoRecover()
		err := testMgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()
}
