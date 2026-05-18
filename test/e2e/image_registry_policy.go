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
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	disallowedImage           = "quay.io/library/nginx:latest"
	allowedImage              = "mcr.microsoft.com/mirror/pause:3.9"
	allowedRegistryMCR        = "mcr.microsoft.com"
	allowedRegistryKubeShared = "kubernetesshared.azurecr.io"
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

			By("checking the policy validation action")
			binding, err := kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings().Get(
				ctx, "image-registry-allowlist-policy-binding", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "Failed to get VAP binding")

			isAuditMode := false
			for _, action := range binding.Spec.ValidationActions {
				if action == admissionregistrationv1.Audit {
					isAuditMode = true
					break
				}
			}

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

			if isAuditMode {
				if err != nil && apierrors.IsForbidden(err) {
					GinkgoLogr.Info("Pod was denied even in Audit mode — policy may have been switched to Deny")
				} else {
					GinkgoLogr.Info("Pod with disallowed image was allowed (policy in Audit mode) — violation logged but not blocked")
				}
				if err == nil {
					_ = kubeClient.CoreV1().Pods(testNS).Delete(ctx, pod.Name, metav1.DeleteOptions{})
				}
			} else {
				Expect(err).To(HaveOccurred(), "Pod with disallowed image should be denied")
				Expect(apierrors.IsForbidden(err)).To(BeTrue(),
					"Expected Forbidden error for disallowed image, got: %v", err)
			}
		})

	It("should allow pods with images from allowed registries and have a valid allowlist",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		func(ctx context.Context) {
			By("verifying the ValidatingAdmissionPolicy exists")
			_, err := kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicies().Get(
				ctx, "image-registry-allowlist-policy", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "ValidatingAdmissionPolicy should exist")

			By("verifying the ValidatingAdmissionPolicyBinding exists")
			_, err = kubeClient.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings().Get(
				ctx, "image-registry-allowlist-policy-binding", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "ValidatingAdmissionPolicyBinding should exist")

			By("verifying the ConfigMap allowlist is non-empty and contains required registries")
			cm, err := kubeClient.CoreV1().ConfigMaps("image-registry-policy").Get(
				ctx, "image-registry-allowlist-config", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "ConfigMap image-registry-allowlist-config should exist")
			Expect(cm.Data["allowedRegistries"]).NotTo(BeEmpty(), "allowedRegistries should not be empty")
			Expect(cm.Data["allowedRegistries"]).To(ContainSubstring(allowedRegistryMCR),
				"%s should be in the allowlist", allowedRegistryMCR)
			Expect(cm.Data["allowedRegistries"]).To(ContainSubstring(allowedRegistryKubeShared),
				"%s should be in the allowlist", allowedRegistryKubeShared)

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

			By(fmt.Sprintf("creating a pod with an image from an allowed registry %q", allowedImage))
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "policy-test-allowed",
					Namespace: testNS,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: allowedImage,
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}
			_, err = kubeClient.CoreV1().Pods(testNS).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(),
				"Pod with allowed image %q should be admitted by the policy", allowedImage)
			DeferCleanup(func(ctx context.Context) {
				_ = kubeClient.CoreV1().Pods(testNS).Delete(ctx, pod.Name, metav1.DeleteOptions{})
			})
		})
})
