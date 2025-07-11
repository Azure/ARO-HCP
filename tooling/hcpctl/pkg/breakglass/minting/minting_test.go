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
	"strings"
	"testing"
	"time"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
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

	if manager.ctrlClient == nil {
		t.Error("ctrlClient not set")
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
			tt.setupClient(kubeClient)

			scheme := common.NewEmptyScheme()
			ctrlClient := clientfake.NewClientBuilder().WithScheme(scheme).Build()

			manager := &DefaultManager{
				kubeClient: kubeClient,
				ctrlClient: ctrlClient,
			}
			ctx := context.Background()

			_, err := manager.CreateCSR(ctx, tt.csrPEM, "test-cluster", "test-user", "test-namespace")

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
				// CSR name is auto-generated, just verify it contains expected prefix
				if !strings.HasPrefix(csr.Name, "sre-breakglass-test-user-") {
					t.Errorf("expected CSR name to start with sre-breakglass-test-user-, got %s", csr.Name)
				}

				if string(csr.Spec.Request) != string(tt.csrPEM) {
					t.Errorf("expected CSR PEM %s, got %s", string(tt.csrPEM), string(csr.Spec.Request))
				}

				// Signer name is auto-generated based on namespace
				expectedSigner := "hypershift.openshift.io/test-namespace.sre-break-glass"
				if csr.Spec.SignerName != expectedSigner {
					t.Errorf("expected signer name %s, got %s", expectedSigner, csr.Spec.SignerName)
				}

				// Expiration is set to default value
				expectedExpiration := int32(86353)
				if *csr.Spec.ExpirationSeconds != expectedExpiration {
					t.Errorf("expected expiration %d, got %d", expectedExpiration, *csr.Spec.ExpirationSeconds)
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
	}{
		{
			name:        "successful approval creation",
			csrName:     "test-csr",
			namespace:   "test-namespace",
			labels:      map[string]string{"test": "label"},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()

			scheme, err := common.NewCertificatesScheme()
			if err != nil {
				t.Fatalf("failed to create certificates scheme: %v", err)
			}
			ctrlClient := clientfake.NewClientBuilder().WithScheme(scheme).Build()

			manager := &DefaultManager{
				kubeClient: kubeClient,
				ctrlClient: ctrlClient,
			}
			ctx := context.Background()

			err = manager.CreateCSRApproval(ctx, tt.csrName, tt.namespace, "test-cluster", "test-user")

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

			if err := tt.setupCSR(kubeClient); err != nil {
				t.Fatalf("failed to setup CSR: %v", err)
			}

			scheme := common.NewEmptyScheme()
			ctrlClient := clientfake.NewClientBuilder().WithScheme(scheme).Build()

			manager := &DefaultManager{
				kubeClient: kubeClient,
				ctrlClient: ctrlClient,
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

			if err := tt.setupCSR(kubeClient); err != nil {
				t.Fatalf("failed to setup CSR: %v", err)
			}

			scheme := common.NewEmptyScheme()
			ctrlClient := clientfake.NewClientBuilder().WithScheme(scheme).Build()

			manager := &DefaultManager{
				kubeClient: kubeClient,
				ctrlClient: ctrlClient,
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

			if err := tt.setupResources(kubeClient); err != nil {
				t.Fatalf("failed to setup resources: %v", err)
			}

			scheme := common.NewEmptyScheme()
			ctrlClient := clientfake.NewClientBuilder().WithScheme(scheme).Build()

			manager := &DefaultManager{
				kubeClient: kubeClient,
				ctrlClient: ctrlClient,
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
		setupResources func(client.Client) error
	}{
		{
			name:        "successful approval cleanup",
			csrName:     "test-csr",
			namespace:   "test-namespace",
			expectError: false,
			setupResources: func(ctrlClient client.Client) error {
				// Create approval
				approval := &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-csr",
						Namespace: "test-namespace",
					},
				}
				return ctrlClient.Create(context.Background(), approval)
			},
		},
		{
			name:        "cleanup with non-existent approval",
			csrName:     "non-existent-csr",
			namespace:   "test-namespace",
			expectError: false, // cleanup should not fail for non-existent resources
			setupResources: func(client.Client) error {
				return nil // don't create any resources
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := kubefake.NewSimpleClientset()

			scheme, err := common.NewCertificatesScheme()
			if err != nil {
				t.Fatalf("failed to create certificates scheme: %v", err)
			}
			ctrlClient := clientfake.NewClientBuilder().WithScheme(scheme).Build()

			if err := tt.setupResources(ctrlClient); err != nil {
				t.Fatalf("failed to setup resources: %v", err)
			}

			manager := &DefaultManager{
				kubeClient: kubeClient,
				ctrlClient: ctrlClient,
			}
			ctx := context.Background()

			err = manager.CleanupCSRApproval(ctx, tt.csrName, tt.namespace)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
