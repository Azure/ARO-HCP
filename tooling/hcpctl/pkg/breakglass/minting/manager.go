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

// Package minting provides functionality for minting (creating and managing) certificates
// through the Kubernetes Certificate Signing Request (CSR) workflow.
//
// The minting package handles the lifecycle of CSRs including creation, approval,
// certificate retrieval, and cleanup. It acts as the bridge between certificate
// generation (handled by the certs package) and the Kubernetes API.
package minting

import (
	"context"
	"fmt"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	// CSRApprovalGVR is the GroupVersionResource for CertificateSigningRequestApproval
	CSRApprovalGVR = schema.GroupVersionResource{
		Group:    "certificates.hypershift.openshift.io",
		Version:  "v1alpha1",
		Resource: "certificatesigningrequestapprovals",
	}
)

// CSRError represents errors related to Certificate Signing Request operations.
type CSRError struct {
	// Operation describes the CSR operation that failed (e.g., "creation", "approval", "signing")
	Operation string
	// CSRName is the name of the CSR that encountered the error
	CSRName string
	// Underlying is the original error that caused this CSR error
	Underlying error
}

func (e *CSRError) Error() string {
	if e.CSRName != "" {
		return fmt.Sprintf("CSR %s failed during %s: %v", e.CSRName, e.Operation, e.Underlying)
	}
	return fmt.Sprintf("CSR operation failed during %s: %v", e.Operation, e.Underlying)
}

func (e *CSRError) Unwrap() error {
	return e.Underlying
}

// NewCSRError creates a new CSRError with the specified operation and underlying error.
func NewCSRError(operation, csrName string, err error) *CSRError {
	return &CSRError{
		Operation:  operation,
		CSRName:    csrName,
		Underlying: err,
	}
}

// CSRManager is an interface for Certificate Signing Request operations.
// This interface abstracts CSR lifecycle management including creation,
// approval, and cleanup to enable testing and alternative implementations.
type CSRManager interface {
	// CreateCSR creates a CSR using the provided parameters and returns the CSR name
	CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error)

	// CreateCSRApproval creates an approval resource using the provided parameters
	CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error

	// WaitForCSRApproval waits for the CSR to be approved
	WaitForCSRApproval(ctx context.Context, name string, timeout, pollInterval time.Duration) error

	// WaitForCertificate waits for the certificate to be issued
	WaitForCertificate(ctx context.Context, name string, timeout, pollInterval time.Duration) ([]byte, error)
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
	kubeClient    kubernetes.Interface
	dynamicClient dynamic.Interface
}

// NewDefaultManager creates a new DefaultManager instance from a rest config.
func NewDefaultManager(restConfig *rest.Config) (*DefaultManager, error) {
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &DefaultManager{
		kubeClient:    kubeClient,
		dynamicClient: dynamicClient,
	}, nil
}

// generateCSRName generates a unique CSR name
func (mgr *DefaultManager) generateCSRName(user string) string {
	return fmt.Sprintf("%s-%s-%d", defaultCSRNamePrefix, user, time.Now().Unix())
}

// generateSignerName generates the signer name for the CSR
func (mgr *DefaultManager) generateSignerName(namespace string) string {
	return fmt.Sprintf(defaultSignerNameTemplate, namespace)
}

// generateLabels generates labels for the CSR and approval resources
func (mgr *DefaultManager) generateLabels(clusterID, user string) map[string]string {
	return map[string]string{
		"api.openshift.com/id":   clusterID,
		"api.openshift.com/name": user,
		"api.openshift.com/type": "break-glass-credential",
	}
}

// CreateCSR creates a new Certificate Signing Request using the provided parameters.
func (mgr *DefaultManager) CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error) {
	csrName := mgr.generateCSRName(user)
	signerName := mgr.generateSignerName(namespace)
	labels := mgr.generateLabels(clusterID, user)

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
	labels := mgr.generateLabels(clusterID, user)

	approval := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "certificates.hypershift.openshift.io/v1alpha1",
			"kind":       "CertificateSigningRequestApproval",
			"metadata": map[string]interface{}{
				"name":      csrName,
				"namespace": namespace,
				"labels":    convertLabelsToInterface(labels),
			},
		},
	}

	_, err := mgr.dynamicClient.Resource(CSRApprovalGVR).Namespace(namespace).Create(ctx, approval, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create approval: %w", err)
	}

	return nil
}

// createCSRLowLevel creates a new Certificate Signing Request in Kubernetes with explicit parameters.
func (mgr *DefaultManager) createCSRLowLevel(ctx context.Context, name string, csrPEM []byte, signerName string, labels map[string]string, expirationSeconds int32) error {
	csr := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        signerName,
			ExpirationSeconds: &expirationSeconds,
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			},
		},
	}

	_, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create CSR: %w", err)
	}

	return nil
}

// createCSRApprovalLowLevel creates an approval resource for the CSR with explicit parameters.
// This is specific to the HyperShift/OpenShift implementation.
func (mgr *DefaultManager) createCSRApprovalLowLevel(ctx context.Context, name, namespace string, labels map[string]string) error {
	approval := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "certificates.hypershift.openshift.io/v1alpha1",
			"kind":       "CertificateSigningRequestApproval",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    convertLabelsToInterface(labels),
			},
		},
	}

	_, err := mgr.dynamicClient.Resource(CSRApprovalGVR).Namespace(namespace).Create(ctx, approval, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create approval: %w", err)
	}

	return nil
}

// WaitForCSRApproval waits for the CSR to be approved by monitoring its conditions.
func (mgr *DefaultManager) WaitForCSRApproval(ctx context.Context, name string, timeout, pollInterval time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		csr, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		for _, condition := range csr.Status.Conditions {
			if condition.Type == certificatesv1.CertificateApproved && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
			if condition.Type == certificatesv1.CertificateDenied && condition.Status == corev1.ConditionTrue {
				return false, NewCSRError("approval", name, fmt.Errorf("CSR was denied: %s", condition.Reason))
			}
		}
		return false, nil
	})
}

// WaitForCertificate waits for the certificate to be issued after approval.
func (mgr *DefaultManager) WaitForCertificate(ctx context.Context, name string, timeout, pollInterval time.Duration) ([]byte, error) {
	var certificate []byte
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		csr, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if len(csr.Status.Certificate) > 0 {
			certificate = csr.Status.Certificate
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return certificate, nil
}

// CleanupCSR removes the CSR resource.
// Errors during cleanup are ignored as the resource may not exist.
func (mgr *DefaultManager) CleanupCSR(ctx context.Context, name string) error {
	// Delete CSR (ignore errors since it might not exist)
	if err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		// Log but don't fail on cleanup errors
		_ = err
	}
	return nil
}

// CleanupCSRApproval removes the CSR approval resource.
// Errors during cleanup are ignored as the resource may not exist.
func (mgr *DefaultManager) CleanupCSRApproval(ctx context.Context, name, namespace string) error {
	// Delete CSR approval (ignore errors since it might not exist)
	if err := mgr.dynamicClient.Resource(CSRApprovalGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		// Log but don't fail on cleanup errors
		_ = err
	}
	return nil
}

// convertLabelsToInterface converts a string map to interface{} map for unstructured objects
func convertLabelsToInterface(labels map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range labels {
		result[k] = v
	}
	return result
}
