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
	"fmt"
	"time"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils"
)

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
	kubeClient kubernetes.Interface
	ctrlClient client.Client
}

// NewDefaultManager creates a new DefaultManager instance from a rest config.
func NewDefaultManager(restConfig *rest.Config) (*DefaultManager, error) {
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	scheme, err := common.NewCertificatesScheme()
	if err != nil {
		return nil, err
	}

	ctrlClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller client: %w", err)
	}

	return &DefaultManager{
		kubeClient: kubeClient,
		ctrlClient: ctrlClient,
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
func (mgr *DefaultManager) generateLabels(clusterID, sanitizedUser string) map[string]string {
	return map[string]string{
		"api.openshift.com/id":   clusterID,
		"api.openshift.com/name": sanitizedUser,
		"api.openshift.com/type": "break-glass-credential",
	}
}

// CreateCSR creates a new Certificate Signing Request using the provided parameters.
func (mgr *DefaultManager) CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error) {
	sanitizedUser, err := utils.SanitizeUsername(user)
	if err != nil {
		return "", fmt.Errorf("invalid username for CSR naming: %w", err)
	}

	csrName := mgr.generateCSRName(sanitizedUser)
	signerName := mgr.generateSignerName(namespace)
	labels := mgr.generateLabels(clusterID, sanitizedUser)

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

	_, err = mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %w", err)
	}

	return csrName, nil
}

// CreateCSRApproval creates an approval resource for the CSR using the provided parameters.
func (mgr *DefaultManager) CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error {
	sanitizedUser, err := utils.SanitizeUsername(user)
	if err != nil {
		return fmt.Errorf("invalid username for kubeconfig authInfo: %w", err)
	}
	labels := mgr.generateLabels(clusterID, sanitizedUser)

	approval := &certificatesv1alpha1.CertificateSigningRequestApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      csrName,
			Namespace: namespace,
			Labels:    labels,
		},
	}

	if err := mgr.ctrlClient.Create(ctx, approval); err != nil {
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
				return false, fmt.Errorf("CSR was denied: %s", condition.Reason)
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
	approval := &certificatesv1alpha1.CertificateSigningRequestApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := mgr.ctrlClient.Delete(ctx, approval)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete CertificateSigningRequestApproval approval: %w", err)
	}
	return nil
}
