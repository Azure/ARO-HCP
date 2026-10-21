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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	kubetesting "k8s.io/client-go/testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorfake "github.com/openshift/client-go/operator/clientset/versioned/fake"
)

func TestHyperShiftIngressCertificateWiring(test *testing.T) {
	for _, testCase := range []struct {
		name          string
		secretType    corev1.SecretType
		secretMissing bool
		certMissing   bool
		keyMissing    bool
		reference     string
		wantError     string
	}{
		{name: "wired", reference: "default-ingress-cert"},
		{name: "Opaque secret", secretType: corev1.SecretTypeOpaque, reference: "default-ingress-cert"},
		{name: "TLS secret", secretType: corev1.SecretTypeTLS, reference: "default-ingress-cert"},
		{name: "secret missing", secretMissing: true, wantError: "getting HyperShift ingress secret"},
		{name: "certificate missing", certMissing: true, wantError: "must contain non-empty tls.crt and tls.key data"},
		{name: "key missing", keyMissing: true, wantError: "must contain non-empty tls.crt and tls.key data"},
		{name: "reference missing", wantError: "expected secret"},
		{name: "ACM reference", reference: "cluster-ingress-cert", wantError: "expected secret"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			if !testCase.secretMissing {
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "default-ingress-cert", Namespace: "openshift-ingress"}, Type: testCase.secretType, Data: map[string][]byte{corev1.TLSCertKey: []byte("certificate"), corev1.TLSPrivateKeyKey: []byte("key")}}
				if testCase.certMissing {
					delete(secret.Data, corev1.TLSCertKey)
				}
				if testCase.keyMissing {
					delete(secret.Data, corev1.TLSPrivateKeyKey)
				}
				_, err := kubeClient.CoreV1().Secrets(secret.Namespace).Create(test.Context(), secret, metav1.CreateOptions{})
				require.NoError(test, err)
			}
			controller := &operatorv1.IngressController{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "openshift-ingress-operator"}}
			if testCase.reference != "" {
				controller.Spec.DefaultCertificate = &corev1.LocalObjectReference{Name: testCase.reference}
			}
			operatorClient := operatorfake.NewClientset(controller)
			err := verifyHyperShiftIngressCertificateWiring(test.Context(), kubeClient, operatorClient)
			if testCase.wantError == "" {
				require.NoError(test, err)
			} else {
				require.ErrorContains(test, err, testCase.wantError)
			}
		})
	}
}

func TestACMIngressCertificateAbsent(test *testing.T) {
	for _, testCase := range []struct {
		name      string
		exists    bool
		apiError  error
		wantError string
	}{
		{name: "absent"},
		{name: "present", exists: true, wantError: "expected ACM certificate delivery to be suppressed"},
		{name: "forbidden is not absence", apiError: apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "cluster-ingress-cert", fmt.Errorf("denied")), wantError: "forbidden"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			if testCase.exists {
				_, err := kubeClient.CoreV1().Secrets("openshift-ingress").Create(test.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cluster-ingress-cert"}}, metav1.CreateOptions{})
				require.NoError(test, err)
			}
			if testCase.apiError != nil {
				kubeClient.PrependReactor("get", "secrets", func(kubetesting.Action) (bool, runtime.Object, error) { return true, nil, testCase.apiError })
			}
			err := verifyACMIngressSecretAbsent(test.Context(), kubeClient)
			if testCase.wantError == "" {
				require.NoError(test, err)
			} else {
				require.ErrorContains(test, err, testCase.wantError)
			}
		})
	}
}
