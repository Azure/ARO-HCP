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
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

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

func TestDefaultIngressServingCertificateSecretWiring(test *testing.T) {
	test.Setenv("AROHCP_ENV", "development")
	valid := ingressSecretCertificatePEM(test, pkix.Name{CommonName: "delivered"}, pkix.Name{CommonName: "issuer"})
	development := ingressSecretCertificatePEM(test, pkix.Name{CommonName: "reserved.hcp.osadev.cloud"}, pkix.Name{CommonName: "reserved.hcp.osadev.cloud"})
	fallback := ingressSecretCertificatePEM(test,
		pkix.Name{CommonName: "openshift-ingress", Organization: []string{"openshift"}},
		pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"openshift"}})
	for _, testCase := range []struct {
		name           string
		secretType     corev1.SecretType
		secretMissing  bool
		certMissing    bool
		keyMissing     bool
		reference      string
		emptyReference bool
		certificate    []byte
		wantError      string
	}{
		{name: "wired", reference: "default-ingress-cert"},
		{name: "Opaque secret", secretType: corev1.SecretTypeOpaque, reference: "default-ingress-cert"},
		{name: "TLS secret", secretType: corev1.SecretTypeTLS, reference: "default-ingress-cert"},
		{name: "development certificate", certificate: development, reference: "default-ingress-cert"},
		{name: "fallback in development", certificate: fallback, reference: "default-ingress-cert", wantError: "internal fallback identity"},
		{name: "Opaque fallback", secretType: corev1.SecretTypeOpaque, certificate: fallback, reference: "default-ingress-cert", wantError: "internal fallback identity"},
		{name: "malformed certificate", certificate: []byte("invalid-certificate-must-not-be-logged"), reference: "default-ingress-cert", wantError: "not certificate PEM"},
		{name: "secret missing", secretMissing: true, wantError: "getting default ingress serving certificate secret"},
		{name: "certificate missing", certMissing: true, wantError: "must contain non-empty tls.crt and tls.key data"},
		{name: "key missing", keyMissing: true, wantError: "must contain non-empty tls.crt and tls.key data"},
		{name: "reference missing", wantError: "expected secret"},
		{name: "empty reference", emptyReference: true, wantError: "expected secret"},
		{name: "ACM reference", reference: "cluster-ingress-cert", wantError: "expected secret"},
		{name: "customer reference", reference: "customer-cert", wantError: "expected secret"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			if !testCase.secretMissing {
				certificate := valid
				if testCase.certificate != nil {
					certificate = testCase.certificate
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "default-ingress-cert", Namespace: "openshift-ingress"}, Type: testCase.secretType, Data: map[string][]byte{corev1.TLSCertKey: certificate, corev1.TLSPrivateKeyKey: []byte("private-key-must-not-be-logged")}}
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
			if len(testCase.reference) > 0 || testCase.emptyReference {
				controller.Spec.DefaultCertificate = &corev1.LocalObjectReference{Name: testCase.reference}
			}
			operatorClient := operatorfake.NewClientset(controller)
			err := checkDefaultIngressServingCertificateSecretWiring(test.Context(), kubeClient, operatorClient)
			if len(testCase.wantError) == 0 {
				require.NoError(test, err)
			} else {
				require.ErrorContains(test, err, testCase.wantError)
				require.NotContains(test, err.Error(), "invalid-certificate-must-not-be-logged")
				require.NotContains(test, err.Error(), "private-key-must-not-be-logged")
			}
		})
	}
}

func ingressSecretCertificatePEM(test *testing.T, subject, issuer pkix.Name) []byte {
	test.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(test, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      subject,
		DNSNames:     []string{"*.apps.cluster.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	parent := &x509.Certificate{Subject: issuer}
	der, err := x509.CreateCertificate(rand.Reader, certificate, parent, publicKey, privateKey)
	require.NoError(test, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestVerifyIngressSecretCertificateIdentity(test *testing.T) {
	for _, environment := range []string{"development", "integration"} {
		test.Run(environment, func(test *testing.T) {
			test.Setenv("AROHCP_ENV", environment)
			for _, testCase := range []struct {
				name      string
				subject   pkix.Name
				issuer    pkix.Name
				wantError bool
			}{
				{
					name:    "delivered public certificate",
					subject: pkix.Name{CommonName: "*.apps.cluster.example.com"},
					issuer:  pkix.Name{CommonName: "public-issuer"},
				},
				{
					name:    "legitimate development self-issued certificate",
					subject: pkix.Name{CommonName: "reserved.hcp.osadev.cloud"},
					issuer:  pkix.Name{CommonName: "reserved.hcp.osadev.cloud"},
				},
				{
					name:      "internal fallback",
					subject:   pkix.Name{CommonName: "openshift-ingress", Organization: []string{"openshift"}},
					issuer:    pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"openshift"}},
					wantError: true,
				},
				{
					name:      "internal fallback with additional DN attributes",
					subject:   pkix.Name{CommonName: "openshift-ingress", Organization: []string{"other", "openshift"}, Country: []string{"US"}},
					issuer:    pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"other", "openshift"}},
					wantError: true,
				},
				{
					name:    "subject CN alone does not identify fallback",
					subject: pkix.Name{CommonName: "openshift-ingress"},
					issuer:  pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"openshift"}},
				},
				{
					name:    "issuer CN alone does not identify fallback",
					subject: pkix.Name{CommonName: "openshift-ingress", Organization: []string{"openshift"}},
					issuer:  pkix.Name{CommonName: "root-ca"},
				},
				{
					name:    "different subject CN",
					subject: pkix.Name{CommonName: "delivered", Organization: []string{"openshift"}},
					issuer:  pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"openshift"}},
				},
				{
					name:    "different issuer CN",
					subject: pkix.Name{CommonName: "openshift-ingress", Organization: []string{"openshift"}},
					issuer:  pkix.Name{CommonName: "public-issuer", OrganizationalUnit: []string{"openshift"}},
				},
			} {
				test.Run(testCase.name, func(test *testing.T) {
					secret := &corev1.Secret{Type: corev1.SecretTypeTLS, Data: map[string][]byte{
						corev1.TLSCertKey: ingressSecretCertificatePEM(test, testCase.subject, testCase.issuer),
					}}
					err := verifyIngressSecretCertificate(secret)
					if testCase.wantError {
						require.ErrorContains(test, err, "internal fallback identity")
					} else {
						require.NoError(test, err)
					}
				})
			}
		})
	}
}

func TestVerifyIngressSecretCertificateData(test *testing.T) {
	valid := ingressSecretCertificatePEM(test, pkix.Name{CommonName: "delivered"}, pkix.Name{CommonName: "issuer"})
	issuer := ingressSecretCertificatePEM(test, pkix.Name{CommonName: "issuer"}, pkix.Name{CommonName: "root"})
	invalid := []byte("-----BEGIN CERTIFICATE-----\nsensitive-invalid-data!\n-----END CERTIFICATE-----\n")
	for _, testCase := range []struct {
		name      string
		data      []byte
		wantError string
	}{
		{name: "missing", wantError: "tls.crt is missing or empty"},
		{name: "empty", data: []byte(" \n"), wantError: "tls.crt is missing or empty"},
		{name: "not PEM", data: []byte("sensitive-invalid-data"), wantError: "not certificate PEM"},
		{name: "broken PEM", data: []byte("-----BEGIN CERTIFICATE-----\nsensitive-invalid-data"), wantError: "invalid certificate PEM"},
		{name: "wrong block", data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("sensitive-invalid-data")}), wantError: "not certificate PEM"},
		{name: "invalid DER", data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("sensitive-invalid-data")}), wantError: "not a valid X.509 certificate"},
		{name: "leaf", data: valid},
		{name: "chain", data: append(append([]byte{}, valid...), valid...)},
		{name: "distinct certificate chain", data: bytes.Join([][]byte{valid, issuer}, nil)},
		{name: "chain with whitespace and CRLF", data: bytes.Join([][]byte{[]byte(" \t\r\n"), bytes.ReplaceAll(valid, []byte("\n"), []byte(" \t\r\n")), issuer, []byte(" \t\r\n")}, []byte("\r\n \t\r\n"))},
		{name: "invalid base64 before valid certificate", data: bytes.Join([][]byte{invalid, valid}, nil), wantError: "certificate 1 has invalid certificate PEM"},
		{name: "invalid base64 between valid certificates", data: bytes.Join([][]byte{valid, invalid, issuer}, nil), wantError: "certificate 2 has invalid certificate PEM"},
		{name: "unterminated block before valid certificate", data: bytes.Join([][]byte{[]byte("-----BEGIN CERTIFICATE-----\nsensitive-invalid-data!\n"), valid}, nil), wantError: "certificate 1 has invalid certificate PEM"},
		{name: "missing newline between certificates", data: bytes.Join([][]byte{bytes.TrimSpace(valid), issuer}, nil), wantError: "certificate 1 has invalid certificate PEM"},
		{name: "trailing malformed data", data: append(append([]byte{}, valid...), []byte("sensitive-invalid-data")...), wantError: "certificate 2 is not certificate PEM"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			secret := &corev1.Secret{Type: corev1.SecretTypeTLS, Data: map[string][]byte{
				corev1.TLSCertKey:       testCase.data,
				corev1.TLSPrivateKeyKey: []byte("private-key-must-not-be-logged"),
			}}
			err := verifyIngressSecretCertificate(secret)
			if len(testCase.wantError) == 0 {
				require.NoError(test, err)
			} else {
				require.ErrorContains(test, err, testCase.wantError)
				require.NotContains(test, err.Error(), "sensitive-invalid-data")
				require.NotContains(test, err.Error(), "private-key-must-not-be-logged")
			}
		})
	}
	require.NoError(test, verifyIngressSecretCertificate(&corev1.Secret{Type: corev1.SecretTypeOpaque, Data: map[string][]byte{corev1.TLSCertKey: valid}}))
	require.ErrorContains(test, verifyIngressSecretCertificate(&corev1.Secret{Type: corev1.SecretTypeOpaque}), "tls.crt is missing or empty")
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
