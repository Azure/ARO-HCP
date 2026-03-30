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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	imageRegistryPolicyName        = "image-registry-allowlist-policy"
	imageRegistryPolicyBindingName = "image-registry-allowlist-policy-binding"
	imageRegistryPolicyNamespace   = "image-registry-policy"
	imageRegistryPolicyConfigMap   = "image-registry-allowlist-config"
	disallowedImage                = "docker.io/library/nginx:latest"
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

	It("should have the ValidatingAdmissionPolicy deployed and active",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		func(ctx context.Context) {
			By("verifying the ValidatingAdmissionPolicy exists")
			vap, err := kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicies().Get(
				ctx, imageRegistryPolicyName, metav1.GetOptions{},
			)
			Expect(err).NotTo(HaveOccurred(), "ValidatingAdmissionPolicy %q not found", imageRegistryPolicyName)
			Expect(vap.Spec.FailurePolicy).To(
				Equal(to.Ptr(admissionregistrationv1.Fail)),
				"FailurePolicy should be Fail for FSI compliance",
			)

			By("verifying the ValidatingAdmissionPolicyBinding exists")
			vapb, err := kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings().Get(
				ctx, imageRegistryPolicyBindingName, metav1.GetOptions{},
			)
			Expect(err).NotTo(HaveOccurred(), "ValidatingAdmissionPolicyBinding %q not found", imageRegistryPolicyBindingName)
			Expect(vapb.Spec.PolicyName).To(Equal(imageRegistryPolicyName))
			Expect(vapb.Spec.ValidationActions).To(ContainElement(admissionregistrationv1.Deny))

			By("verifying the allowlist ConfigMap exists and is non-empty")
			cm, err := kubeClient.CoreV1().ConfigMaps(imageRegistryPolicyNamespace).Get(
				ctx, imageRegistryPolicyConfigMap, metav1.GetOptions{},
			)
			Expect(err).NotTo(HaveOccurred(), "ConfigMap %q not found", imageRegistryPolicyConfigMap)
			Expect(cm.Data["allowedRegistries"]).NotTo(BeEmpty(), "allowedRegistries should not be empty")
		})

	It("should deny pods with images from disallowed registries",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.CoreInfraService,
		func(ctx context.Context) {
			testNS := "image-policy-test"

			By("creating a test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNS,
				},
			}
			_, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")
			}
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

	It("should allow pods with images from allowed registries",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		func(ctx context.Context) {
			testNS := "image-policy-test"

			By("creating a test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNS,
				},
			}
			_, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")
			}
			DeferCleanup(func(ctx context.Context) {
				_ = kubeClient.CoreV1().Namespaces().Delete(ctx, testNS, metav1.DeleteOptions{})
			})

			By("creating a pod with an allowed MCR image")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "policy-test-allowed",
					Namespace: testNS,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   "mcr.microsoft.com/cbl-mariner/base/core:2.0",
							Command: []string{"/bin/sh", "-c", "sleep 1"},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}
			created, err := kubeClient.CoreV1().Pods(testNS).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "Pod with allowed image should be accepted")
			DeferCleanup(func(ctx context.Context) {
				_ = kubeClient.CoreV1().Pods(testNS).Delete(ctx, created.Name, metav1.DeleteOptions{})
			})
		})
})
