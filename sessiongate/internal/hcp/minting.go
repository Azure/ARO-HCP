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

package hcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	certificatesclientv1alpha1 "github.com/openshift/hypershift/client/clientset/clientset/typed/certificates/v1alpha1"
)

// checkCSRApprovalStatus checks the CSR conditions and returns approval status
func checkCSRApprovalStatus(csr *certificatesv1.CertificateSigningRequest) (approved, denied bool, reason string) {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1.CertificateApproved && condition.Status == corev1.ConditionTrue {
			return true, false, ""
		}
		if condition.Type == certificatesv1.CertificateDenied && condition.Status == corev1.ConditionTrue {
			return false, true, condition.Reason
		}
	}
	return false, false, ""
}

// CSRManager is an interface for Certificate Signing Request operations.
// This interface abstracts CSR lifecycle management including creation,
// approval, and cleanup to enable testing and alternative implementations.
type CSRManager interface {
	// CreateCSR creates a CSR using the provided parameters and returns the CSR name
	CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error)

	// CreateCSRApproval creates an approval resource using the provided parameters
	CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error

	// WaitForCSRApproval waits for the CSR to be approved using watch
	WaitForCSRApproval(ctx context.Context, name string, timeout time.Duration) error

	// WaitForCertificate waits for the certificate to be issued using watch
	WaitForCertificate(ctx context.Context, name string, timeout time.Duration) ([]byte, error)
}

const (
	// Default CSR configuration constants
	defaultExpirationSeconds  = int32(86353) // ~24 hours
	defaultSignerNameTemplate = "hypershift.openshift.io/%s.sre-break-glass"
	defaultCSRNamePrefix      = "sre-breakglass"
)

// DefaultManager is the default implementation of CSR minting operations.
// It handles the complete lifecycle of Certificate Signing Requests in Kubernetes.
type DefaultManager struct {
	kubeClient         kubernetes.Interface
	certificatesClient certificatesclientv1alpha1.CertificatesV1alpha1Interface
}

// NewCSRManager creates a new DefaultManager instance from a rest config.
func NewCSRManager(restConfig *rest.Config) (*DefaultManager, error) {
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	certificatesClient, err := certificatesclientv1alpha1.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificates client: %w", err)
	}

	return &DefaultManager{
		kubeClient:         kubeClient,
		certificatesClient: certificatesClient,
	}, nil
}

// generateCSRName generates a unique CSR name using cryptographically secure random numbers
func (mgr *DefaultManager) generateCSRName(user string) string {
	// Generate a cryptographically secure random number
	max := big.NewInt(9999999999999999) // 16-digit max for reasonable CSR name length
	randomNum, err := rand.Int(rand.Reader, max)
	if err != nil {
		// Fallback to nanosecond timestamp if random generation fails
		return fmt.Sprintf("%s-%s-%d", defaultCSRNamePrefix, user, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s-%s", defaultCSRNamePrefix, user, randomNum.String())
}

// generateSignerName generates the signer name for the CSR
func (mgr *DefaultManager) generateSignerName(namespace string) string {
	return fmt.Sprintf(defaultSignerNameTemplate, namespace)
}

// generateLabels generates labels for the CSR and approval resources
func (mgr *DefaultManager) generateLabels(clusterID string) map[string]string {
	return map[string]string{
		"api.openshift.com/id":   clusterID,
		"api.openshift.com/type": "break-glass-credential",
	}
}

// CreateCSR creates a new Certificate Signing Request using the provided parameters.
func (mgr *DefaultManager) CreateCSR(ctx context.Context, csrPEM []byte, name, clusterID, user, namespace string) (string, error) {
	csrName := mgr.generateCSRName(name)
	signerName := mgr.generateSignerName(namespace)
	labels := mgr.generateLabels(clusterID)

	csr := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:   csrName,
			Labels: labels,
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        signerName,
			ExpirationSeconds: func() *int32 { v := defaultExpirationSeconds; return &v }(),
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			},
		},
	}

	_, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %w", err)
	}

	return csrName, nil
}

// CreateCSRApproval creates an approval resource for the CSR using the provided parameters.
func (mgr *DefaultManager) CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error {
	labels := mgr.generateLabels(clusterID)

	approval := &certificatesv1alpha1.CertificateSigningRequestApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      csrName,
			Namespace: namespace,
			Labels:    labels,
		},
	}

	if _, err := mgr.certificatesClient.CertificateSigningRequestApprovals(namespace).Create(ctx, approval, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create approval: %w", err)
	}

	return nil
}

// WaitForCSRApproval waits for the CSR to be approved using watch instead of polling.
func (mgr *DefaultManager) WaitForCSRApproval(ctx context.Context, name string, timeout time.Duration) error {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Set up the watch with field selector for the specific CSR
	timeoutSeconds := int64(timeout.Seconds())
	watchOpts := metav1.ListOptions{
		FieldSelector:  fmt.Sprintf("metadata.name=%s", name),
		Watch:          true,
		TimeoutSeconds: &timeoutSeconds,
	}

	watcher, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Watch(ctx, watchOpts)
	if err != nil {
		return fmt.Errorf("failed to create CSR watcher: %w", err)
	}
	defer watcher.Stop()

	// Check initial state first in case CSR is already approved
	csr, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CSR: %w", err)
	}

	if approved, denied, reason := checkCSRApprovalStatus(csr); approved {
		return nil
	} else if denied {
		return fmt.Errorf("CSR was denied: %s", reason)
	}

	// Watch for changes
	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Object == nil {
				continue
			}

			csr, ok := event.Object.(*certificatesv1.CertificateSigningRequest)
			if !ok {
				continue
			}

			approved, denied, reason := checkCSRApprovalStatus(csr)
			if approved {
				return nil
			} else if denied {
				return fmt.Errorf("CSR was denied: %s", reason)
			}

		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for CSR approval: %w", ctx.Err())
		}
	}
}

// WaitForCertificate waits for the certificate to be issued using watch instead of polling.
func (mgr *DefaultManager) WaitForCertificate(ctx context.Context, name string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	timeoutSeconds := int64(timeout.Seconds())
	watchOpts := metav1.ListOptions{
		FieldSelector:  fmt.Sprintf("metadata.name=%s", name),
		Watch:          true,
		TimeoutSeconds: &timeoutSeconds,
	}

	watcher, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Watch(ctx, watchOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR watcher: %w", err)
	}
	defer watcher.Stop()

	// Check initial state first
	csr, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get CSR: %w", err)
	}

	if len(csr.Status.Certificate) > 0 {
		return csr.Status.Certificate, nil
	}

	// Watch for changes
	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Object == nil {
				continue
			}

			csr, ok := event.Object.(*certificatesv1.CertificateSigningRequest)
			if !ok {
				continue
			}

			if len(csr.Status.Certificate) > 0 {
				return csr.Status.Certificate, nil
			}

		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for certificate: %w", ctx.Err())
		}
	}
}

// CleanupCSR removes the CSR resource.
// Only NotFound errors are ignored as the resource may not exist.
func (mgr *DefaultManager) CleanupCSR(ctx context.Context, name string) error {
	err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete CertificateSigningRequests: %w", err)
	}
	return nil
}

// CleanupCSRApproval removes the CSR approval resource.
// Only NotFound errors are ignored as the resource may not exist.
func (mgr *DefaultManager) CleanupCSRApproval(ctx context.Context, name, namespace string) error {
	err := mgr.certificatesClient.CertificateSigningRequestApprovals(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete CertificateSigningRequestApproval approval: %w", err)
	}
	return nil
}
