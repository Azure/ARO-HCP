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

package breakglass

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	certificatesv1client "k8s.io/client-go/kubernetes/typed/certificates/v1"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass/portforward"
)

// MockKubernetesClient is a mock implementation of KubernetesClient for testing.
type MockKubernetesClient struct {
	kubernetes.Interface
	CertificatesV1Func func() certificatesv1client.CertificatesV1Interface
}

func (m *MockKubernetesClient) CertificatesV1() certificatesv1client.CertificatesV1Interface {
	if m.CertificatesV1Func != nil {
		return m.CertificatesV1Func()
	}
	return &MockCertificatesV1Interface{}
}

// MockCertificatesV1Interface is a mock implementation
type MockCertificatesV1Interface struct {
	certificatesv1client.CertificatesV1Interface
	CertificateSigningRequestsFunc func() certificatesv1client.CertificateSigningRequestInterface
}

func (m *MockCertificatesV1Interface) CertificateSigningRequests() certificatesv1client.CertificateSigningRequestInterface {
	if m.CertificateSigningRequestsFunc != nil {
		return m.CertificateSigningRequestsFunc()
	}
	return &MockCertificateSigningRequestInterface{}
}

// MockCertificateSigningRequestInterface is a mock implementation
type MockCertificateSigningRequestInterface struct {
	certificatesv1client.CertificateSigningRequestInterface
	DeleteFunc func(ctx context.Context, name string, opts metav1.DeleteOptions) error
}

func (m *MockCertificateSigningRequestInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, name, opts)
	}
	return nil
}

// MockDynamicClient is a mock implementation of DynamicClient for testing.
type MockDynamicClient struct {
	dynamic.Interface
	ResourceFunc func(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface
}

func (m *MockDynamicClient) Resource(resource schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	if m.ResourceFunc != nil {
		return m.ResourceFunc(resource)
	}
	return &MockNamespaceableResourceInterface{}
}

// MockNamespaceableResourceInterface is a mock implementation
type MockNamespaceableResourceInterface struct {
	dynamic.NamespaceableResourceInterface
	NamespaceFunc func(string) dynamic.ResourceInterface
	DeleteFunc    func(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error
}

func (m *MockNamespaceableResourceInterface) Namespace(namespace string) dynamic.ResourceInterface {
	if m.NamespaceFunc != nil {
		return m.NamespaceFunc(namespace)
	}
	return &MockResourceInterface{}
}

func (m *MockNamespaceableResourceInterface) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, name, options, subresources...)
	}
	return nil
}

// MockResourceInterface is a mock implementation
type MockResourceInterface struct {
	dynamic.ResourceInterface
	DeleteFunc func(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error
}

func (m *MockResourceInterface) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	if m.DeleteFunc != nil {
		return m.DeleteFunc(ctx, name, options, subresources...)
	}
	return nil
}

// MockPortForwarder is a mock implementation of portforward.Forwarder for testing.
type MockPortForwarder struct {
	FindFreePortFunc func() (int, error)
	ForwardPortsFunc func(ctx context.Context, stopCh <-chan struct{}, readyCh chan struct{}) error
}

func (m *MockPortForwarder) FindFreePort() (int, error) {
	if m.FindFreePortFunc != nil {
		return m.FindFreePortFunc()
	}
	return 8080, nil
}

func (m *MockPortForwarder) ForwardPorts(ctx context.Context, stopCh <-chan struct{}, readyCh chan struct{}) error {
	if m.ForwardPortsFunc != nil {
		return m.ForwardPortsFunc(ctx, stopCh, readyCh)
	}
	// Simulate immediate readiness
	close(readyCh)
	<-stopCh
	return nil
}

// MockPortForwarderFactory is a mock implementation of PortForwarderFactory for testing.
type MockPortForwarderFactory struct {
	NewPortForwarderFunc func(config *rest.Config, namespace, resource string, localPort, remotePort int) (portforward.Forwarder, error)
}

func (m *MockPortForwarderFactory) NewPortForwarder(config *rest.Config, namespace, resource string, localPort, remotePort int) (portforward.Forwarder, error) {
	if m.NewPortForwarderFunc != nil {
		return m.NewPortForwarderFunc(config, namespace, resource, localPort, remotePort)
	}
	return &MockPortForwarder{}, nil
}

// MockSecretManager is a mock implementation of SecretManager for testing.
type MockSecretManager struct {
	GetSecretFunc    func(ctx context.Context, namespace, name string) (map[string][]byte, error)
	GetSecretKeyFunc func(ctx context.Context, namespace, name, key string) ([]byte, error)
}

func (m *MockSecretManager) GetSecret(ctx context.Context, namespace, name string) (map[string][]byte, error) {
	if m.GetSecretFunc != nil {
		return m.GetSecretFunc(ctx, namespace, name)
	}
	return map[string][]byte{"tls.crt": []byte("mock-ca-cert")}, nil
}

func (m *MockSecretManager) GetSecretKey(ctx context.Context, namespace, name, key string) ([]byte, error) {
	if m.GetSecretKeyFunc != nil {
		return m.GetSecretKeyFunc(ctx, namespace, name, key)
	}
	return []byte("mock-ca-cert"), nil
}

// MockCSRManager is a mock implementation of CSRManager for testing.
type MockCSRManager struct {
	CreateCSRFunc          func(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error)
	CreateCSRApprovalFunc  func(ctx context.Context, csrName, namespace, clusterID, user string) error
	WaitForCSRApprovalFunc func(ctx context.Context, name string, timeout, pollInterval time.Duration) error
	WaitForCertificateFunc func(ctx context.Context, name string, timeout, pollInterval time.Duration) ([]byte, error)
}

func (m *MockCSRManager) CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error) {
	if m.CreateCSRFunc != nil {
		return m.CreateCSRFunc(ctx, csrPEM, clusterID, user, namespace)
	}
	return fmt.Sprintf("sre-breakglass-%s-%d", user, time.Now().Unix()), nil
}

func (m *MockCSRManager) CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error {
	if m.CreateCSRApprovalFunc != nil {
		return m.CreateCSRApprovalFunc(ctx, csrName, namespace, clusterID, user)
	}
	return nil
}

func (m *MockCSRManager) WaitForCSRApproval(ctx context.Context, name string, timeout, pollInterval time.Duration) error {
	if m.WaitForCSRApprovalFunc != nil {
		return m.WaitForCSRApprovalFunc(ctx, name, timeout, pollInterval)
	}
	return nil
}

func (m *MockCSRManager) WaitForCertificate(ctx context.Context, name string, timeout, pollInterval time.Duration) ([]byte, error) {
	if m.WaitForCertificateFunc != nil {
		return m.WaitForCertificateFunc(ctx, name, timeout, pollInterval)
	}
	return []byte("mock-certificate"), nil
}

// TestGenerateCSRNameWithTimestamp tests the CSR name generation with timestamp.
func TestGenerateCSRNameWithTimestamp(t *testing.T) {
	config := DefaultConfig()
	config.Naming.AddTimestamp = true
	config.Naming.AddRandomSuffix = false

	name1, err := config.GenerateCSRName("test-cluster", "test-user")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Wait a moment and generate another name (timestamp format is to the second)
	time.Sleep(time.Second * 1)
	name2, err := config.GenerateCSRName("test-cluster", "test-user")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Names should be different due to timestamp
	if name1 == name2 {
		t.Errorf("Expected different names due to timestamp, got %s and %s", name1, name2)
	}

	// Both should start with the base name
	expectedBase := "sre-breakglass-test-cluster-test-user"
	if !contains(name1, expectedBase) || !contains(name2, expectedBase) {
		t.Errorf("Expected names to contain base %s, got %s and %s", expectedBase, name1, name2)
	}
}

// TestGenerateCSRNameWithRandomSuffix tests the CSR name generation with random suffix.
func TestGenerateCSRNameWithRandomSuffix(t *testing.T) {
	config := DefaultConfig()
	config.Naming.AddTimestamp = false
	config.Naming.AddRandomSuffix = true
	config.Naming.RandomSuffixLength = 6

	name1, err := config.GenerateCSRName("test-cluster", "test-user")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	name2, err := config.GenerateCSRName("test-cluster", "test-user")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Names should be different due to random suffix
	if name1 == name2 {
		t.Errorf("Expected different names due to random suffix, got %s and %s", name1, name2)
	}

	// Both should start with the base name
	expectedBase := "sre-breakglass-test-cluster-test-user"
	if !contains(name1, expectedBase) || !contains(name2, expectedBase) {
		t.Errorf("Expected names to contain base %s, got %s and %s", expectedBase, name1, name2)
	}
}

// TestGenerateCSRNameLengthValidation tests CSR name length validation.
func TestGenerateCSRNameLengthValidation(t *testing.T) {
	config := DefaultConfig()
	config.Naming.ValidateLengths = true
	config.Naming.AddTimestamp = true
	config.Naming.AddRandomSuffix = true
	config.Naming.RandomSuffixLength = 8

	// Use very long cluster ID and user to trigger length validation
	longClusterID := "very-long-cluster-id-that-exceeds-normal-length-limits-and-should-cause-validation-to-fail-when-combined-with-other-parts-of-the-name-template-especially-when-we-add-timestamp-and-random-suffixes"
	longUser := "very-long-user-name-that-also-exceeds-normal-length-limits"

	_, err := config.GenerateCSRName(longClusterID, longUser)
	if err == nil {
		t.Error("Expected length validation error, got nil")
	}

	// Check that it's specifically a validation error
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("Expected ValidationError, got %T: %v", err, err)
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}

// Test cleanup functionality

// Note: Cleanup-related tests have been removed as we've migrated to defer-based cleanup
// The defer statements in Execute() provide the same functionality with simpler code
