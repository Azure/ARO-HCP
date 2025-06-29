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

package minting

import (
	"context"
	"errors"
	"testing"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func TestNewDefaultManager(t *testing.T) {
	restConfig := &rest.Config{Host: "https://test-server"}

	manager, err := NewDefaultManager(restConfig)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if manager == nil {
		t.Error("NewDefaultManager returned nil")
		return
	}

	if manager.kubeClient == nil {
		t.Error("kubeClient not set")
	}

	if manager.dynamicClient == nil {
		t.Error("dynamicClient not set")
	}
}

func TestCreateCSR(t *testing.T) {
	tests := []struct {
		name              string
		csrName           string
		csrPEM            []byte
		signerName        string
		labels            map[string]string
		expirationSeconds int32
		expectError       bool
		setupClient       func(*kubefake.Clientset)
	}{
		{
			name:              "successful CSR creation",
			csrName:           "test-csr",
			csrPEM:            []byte("fake-csr-pem"),
			signerName:        "test-signer",
			labels:            map[string]string{"test": "label"},
			expirationSeconds: 3600,
			expectError:       false,
			setupClient:       func(client *kubefake.Clientset) {},
		},
		{
			name:              "CSR creation failure",
			csrName:           "test-csr",
			csrPEM:            []byte("fake-csr-pem"),
			signerName:        "test-signer",
			labels:            map[string]string{"test": "label"},
			expirationSeconds: 3600,
			expectError:       true,
			setupClient: func(client *kubefake.Clientset) {
				client.PrependReactor("create", "certificatesigningrequests", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("creation failed")
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
			tt.setupClient(kubeClient)

			manager := &DefaultManager{
				kubeClient:    kubeClient,
				dynamicClient: dynamicClient,
			}
			ctx := context.Background()

			err := manager.createCSRLowLevel(ctx, tt.csrName, tt.csrPEM, tt.signerName, tt.labels, tt.expirationSeconds)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !tt.expectError {
				// Verify CSR was created with correct properties
				csrs, err := kubeClient.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
				if err != nil {
					t.Fatalf("failed to list CSRs: %v", err)
				}

				if len(csrs.Items) != 1 {
					t.Fatalf("expected 1 CSR, got %d", len(csrs.Items))
				}

				csr := csrs.Items[0]
				if csr.Name != tt.csrName {
					t.Errorf("expected CSR name %s, got %s", tt.csrName, csr.Name)
				}

				if string(csr.Spec.Request) != string(tt.csrPEM) {
					t.Errorf("expected CSR PEM %s, got %s", string(tt.csrPEM), string(csr.Spec.Request))
				}

				if csr.Spec.SignerName != tt.signerName {
					t.Errorf("expected signer name %s, got %s", tt.signerName, csr.Spec.SignerName)
				}

				if *csr.Spec.ExpirationSeconds != tt.expirationSeconds {
					t.Errorf("expected expiration %d, got %d", tt.expirationSeconds, *csr.Spec.ExpirationSeconds)
				}
			}
		})
	}
}

func TestCreateCSRApproval(t *testing.T) {
	tests := []struct {
		name        string
		csrName     string
		namespace   string
		labels      map[string]string
		expectError bool
		setupClient func(*fake.FakeDynamicClient)
	}{
		{
			name:        "successful approval creation",
			csrName:     "test-csr",
			namespace:   "test-namespace",
			labels:      map[string]string{"test": "label"},
			expectError: false,
			setupClient: func(client *fake.FakeDynamicClient) {},
		},
		{
			name:        "approval creation failure",
			csrName:     "test-csr",
			namespace:   "test-namespace",
			labels:      map[string]string{"test": "label"},
			expectError: true,
			setupClient: func(client *fake.FakeDynamicClient) {
				client.PrependReactor("create", "certificatesigningrequestapprovals", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("creation failed")
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
			tt.setupClient(dynamicClient)

			manager := &DefaultManager{
				kubeClient:    kubeClient,
				dynamicClient: dynamicClient,
			}
			ctx := context.Background()

			err := manager.createCSRApprovalLowLevel(ctx, tt.csrName, tt.namespace, tt.labels)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestWaitForCSRApproval(t *testing.T) {
	tests := []struct {
		name         string
		csrName      string
		timeout      time.Duration
		pollInterval time.Duration
		expectError  bool
		setupCSR     func(kubernetes.Interface) error
	}{
		{
			name:         "CSR gets approved",
			csrName:      "test-csr",
			timeout:      5 * time.Second,
			pollInterval: 100 * time.Millisecond,
			expectError:  false,
			setupCSR: func(client kubernetes.Interface) error {
				csr := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{Name: "test-csr"},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{
							{
								Type:   certificatesv1.CertificateApproved,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				_, err := client.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csr, metav1.CreateOptions{})
				return err
			},
		},
		{
			name:         "CSR gets denied",
			csrName:      "test-csr",
			timeout:      5 * time.Second,
			pollInterval: 100 * time.Millisecond,
			expectError:  true,
			setupCSR: func(client kubernetes.Interface) error {
				csr := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{Name: "test-csr"},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{
							{
								Type:   certificatesv1.CertificateDenied,
								Status: corev1.ConditionTrue,
								Reason: "TestDenial",
							},
						},
					},
				}
				_, err := client.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csr, metav1.CreateOptions{})
				return err
			},
		},
		{
			name:         "timeout waiting for approval",
			csrName:      "test-csr",
			timeout:      100 * time.Millisecond,
			pollInterval: 50 * time.Millisecond,
			expectError:  true,
			setupCSR: func(client kubernetes.Interface) error {
				csr := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{Name: "test-csr"},
					Status:     certificatesv1.CertificateSigningRequestStatus{},
				}
				_, err := client.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csr, metav1.CreateOptions{})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())

			if err := tt.setupCSR(kubeClient); err != nil {
				t.Fatalf("failed to setup CSR: %v", err)
			}

			manager := &DefaultManager{
				kubeClient:    kubeClient,
				dynamicClient: dynamicClient,
			}
			ctx := context.Background()

			err := manager.WaitForCSRApproval(ctx, tt.csrName, tt.timeout, tt.pollInterval)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestWaitForCertificate(t *testing.T) {
	tests := []struct {
		name         string
		csrName      string
		timeout      time.Duration
		pollInterval time.Duration
		expectError  bool
		setupCSR     func(kubernetes.Interface) error
		expectedCert []byte
	}{
		{
			name:         "certificate is available",
			csrName:      "test-csr",
			timeout:      5 * time.Second,
			pollInterval: 100 * time.Millisecond,
			expectError:  false,
			expectedCert: []byte("fake-certificate"),
			setupCSR: func(client kubernetes.Interface) error {
				csr := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{Name: "test-csr"},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Certificate: []byte("fake-certificate"),
					},
				}
				_, err := client.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csr, metav1.CreateOptions{})
				return err
			},
		},
		{
			name:         "timeout waiting for certificate",
			csrName:      "test-csr",
			timeout:      100 * time.Millisecond,
			pollInterval: 50 * time.Millisecond,
			expectError:  true,
			setupCSR: func(client kubernetes.Interface) error {
				csr := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{Name: "test-csr"},
					Status:     certificatesv1.CertificateSigningRequestStatus{},
				}
				_, err := client.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csr, metav1.CreateOptions{})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())

			if err := tt.setupCSR(kubeClient); err != nil {
				t.Fatalf("failed to setup CSR: %v", err)
			}

			manager := &DefaultManager{
				kubeClient:    kubeClient,
				dynamicClient: dynamicClient,
			}
			ctx := context.Background()

			cert, err := manager.WaitForCertificate(ctx, tt.csrName, tt.timeout, tt.pollInterval)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !tt.expectError && string(cert) != string(tt.expectedCert) {
				t.Errorf("expected certificate %s, got %s", string(tt.expectedCert), string(cert))
			}
		})
	}
}

func TestCleanupCSR(t *testing.T) {
	tests := []struct {
		name           string
		csrName        string
		expectError    bool
		setupResources func(kubernetes.Interface) error
	}{
		{
			name:        "successful CSR cleanup",
			csrName:     "test-csr",
			expectError: false,
			setupResources: func(kubeClient kubernetes.Interface) error {
				// Create CSR
				csr := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{Name: "test-csr"},
				}
				_, err := kubeClient.CertificatesV1().CertificateSigningRequests().Create(context.Background(), csr, metav1.CreateOptions{})
				return err
			},
		},
		{
			name:        "cleanup with non-existent CSR",
			csrName:     "non-existent-csr",
			expectError: false, // cleanup should not fail for non-existent resources
			setupResources: func(kubernetes.Interface) error {
				return nil // don't create any resources
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())

			if err := tt.setupResources(kubeClient); err != nil {
				t.Fatalf("failed to setup resources: %v", err)
			}

			manager := &DefaultManager{
				kubeClient:    kubeClient,
				dynamicClient: dynamicClient,
			}
			ctx := context.Background()

			err := manager.CleanupCSR(ctx, tt.csrName)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCleanupCSRApproval(t *testing.T) {
	tests := []struct {
		name           string
		csrName        string
		namespace      string
		expectError    bool
		setupResources func(dynamic.Interface) error
	}{
		{
			name:        "successful approval cleanup",
			csrName:     "test-csr",
			namespace:   "test-namespace",
			expectError: false,
			setupResources: func(dynamicClient dynamic.Interface) error {
				// Create approval
				approval := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "certificates.hypershift.openshift.io/v1alpha1",
						"kind":       "CertificateSigningRequestApproval",
						"metadata": map[string]interface{}{
							"name":      "test-csr",
							"namespace": "test-namespace",
						},
					},
				}
				_, err := dynamicClient.Resource(CSRApprovalGVR).Namespace("test-namespace").Create(context.Background(), approval, metav1.CreateOptions{})
				return err
			},
		},
		{
			name:        "cleanup with non-existent approval",
			csrName:     "non-existent-csr",
			namespace:   "test-namespace",
			expectError: false, // cleanup should not fail for non-existent resources
			setupResources: func(dynamic.Interface) error {
				return nil // don't create any resources
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()
			dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())

			if err := tt.setupResources(dynamicClient); err != nil {
				t.Fatalf("failed to setup resources: %v", err)
			}

			manager := &DefaultManager{
				kubeClient:    kubeClient,
				dynamicClient: dynamicClient,
			}
			ctx := context.Background()

			err := manager.CleanupCSRApproval(ctx, tt.csrName, tt.namespace)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestConvertLabelsToInterface(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]interface{}
	}{
		{
			name:     "empty map",
			input:    map[string]string{},
			expected: map[string]interface{}{},
		},
		{
			name: "single label",
			input: map[string]string{
				"key": "value",
			},
			expected: map[string]interface{}{
				"key": "value",
			},
		},
		{
			name: "multiple labels",
			input: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			expected: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertLabelsToInterface(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d labels, got %d", len(tt.expected), len(result))
			}

			for key, expectedValue := range tt.expected {
				if actualValue, exists := result[key]; !exists {
					t.Errorf("expected key %s not found", key)
				} else if actualValue != expectedValue {
					t.Errorf("expected value %v for key %s, got %v", expectedValue, key, actualValue)
				}
			}
		})
	}
}
