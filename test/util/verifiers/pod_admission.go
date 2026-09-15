// Copyright 2026 Microsoft Corporation
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

package verifiers

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyProbePodDeniedByAdmissionPolicy struct {
	policyName    string
	policyMessage string
}

func (v verifyProbePodDeniedByAdmissionPolicy) Name() string {
	return fmt.Sprintf("VerifyProbePodDeniedByAdmissionPolicy(policy=%s)", v.policyName)
}

func (v verifyProbePodDeniedByAdmissionPolicy) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "dataplane-probe-"},
		Spec: corev1.PodSpec{
			Containers:    []corev1.Container{{Name: "probe", Image: "does-not-exist.invalid/probe:e2e"}},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	created, err := kubeClient.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err == nil {
		_ = kubeClient.CoreV1().Pods("default").Delete(ctx, created.Name, metav1.DeleteOptions{})
		return fmt.Errorf("probe pod CREATE succeeded; expected ValidatingAdmissionPolicy %s to reject it", v.policyName)
	}
	if !apierrors.IsForbidden(err) {
		return fmt.Errorf("probe pod CREATE failed with an unexpected error (wanted Forbidden from ValidatingAdmissionPolicy %s): %w", v.policyName, err)
	}
	if !strings.Contains(err.Error(), v.policyName) && !strings.Contains(err.Error(), v.policyMessage) {
		return fmt.Errorf("probe pod CREATE was Forbidden but not by ValidatingAdmissionPolicy %s: %w", v.policyName, err)
	}
	return nil
}

// VerifyProbePodDeniedByAdmissionPolicy returns a verifier that probe pod CREATE in the
// default namespace is Forbidden by the named ValidatingAdmissionPolicy.
func VerifyProbePodDeniedByAdmissionPolicy(policyName, policyMessage string) HostedClusterVerifier {
	return verifyProbePodDeniedByAdmissionPolicy{policyName: policyName, policyMessage: policyMessage}
}
