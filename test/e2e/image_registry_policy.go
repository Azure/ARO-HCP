// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	disallowedImage = "quay.io/library/nginx:latest"
)

var _ = Describe("Image Registry Policy", func() {
	var kubeClient kubernetes.Interface

	BeforeEach(func() {
		restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		).ClientConfig()
		Expect(err).NotTo(HaveOccurred(), "Failed to load kubeconfig")

		kubeClient, err = kubernetes.NewForConfig(restConfig)
		Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client")
	})

	It("should deny pods with images from disallowed registries",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		func(ctx context.Context) {
			By("creating a test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "image-policy-test-",
				},
			}
			createdNS, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")
			testNS := createdNS.Name
			DeferCleanup(func(ctx context.Context) {
				_ = kubeClient.CoreV1().Namespaces().Delete(ctx, testNS, metav1.DeleteOptions{})
			})

			By(fmt.Sprintf("attempting to create a pod with disallowed image %q", disallowedImage))
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "policy-test-disallowed",
					Namespace: testNS,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   disallowedImage,
							Command: []string{"/bin/sh", "-c", "sleep 1"},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}
			_, err = kubeClient.CoreV1().Pods(testNS).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).To(HaveOccurred(), "Pod with disallowed image should be denied")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(),
				"Expected Forbidden error for disallowed image, got: %v", err)
		})
})
