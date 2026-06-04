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

package csrminting

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

	client "sigs.k8s.io/controller-runtime/pkg/client"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
)

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

type CSRManager interface {
	CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error)
	CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error
	WaitForCSRApproval(ctx context.Context, name string, timeout time.Duration) error
	WaitForCertificate(ctx context.Context, name string, timeout time.Duration) ([]byte, error)
	CleanupCSR(ctx context.Context, name string) error
	CleanupCSRApproval(ctx context.Context, name, namespace string) error
}

const (
	DefaultExpirationSeconds = int32(86353) // ~24 hours
	SignerNameTemplate       = "hypershift.openshift.io/%s.sre-break-glass"
	CSRNamePrefix            = "sre-breakglass"
)

type DefaultManager struct {
	kubeClient kubernetes.Interface
	ctrlClient client.Client
}

func NewDefaultManager(kubeClient kubernetes.Interface, ctrlClient client.Client) *DefaultManager {
	return &DefaultManager{kubeClient: kubeClient, ctrlClient: ctrlClient}
}

func generateCSRName(prefix string) string {
	max := big.NewInt(9999999999999999)
	randomNum, err := rand.Int(rand.Reader, max)
	if err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, randomNum.String())
}

func (mgr *DefaultManager) CreateCSR(ctx context.Context, csrPEM []byte, clusterID, user, namespace string) (string, error) {
	csrName := generateCSRName(CSRNamePrefix + "-" + user)
	signerName := fmt.Sprintf(SignerNameTemplate, namespace)
	labels := map[string]string{
		"api.openshift.com/id":   clusterID,
		"api.openshift.com/name": user,
		"api.openshift.com/type": "break-glass-credential",
	}

	csr := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:   csrName,
			Labels: labels,
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        signerName,
			ExpirationSeconds: func() *int32 { v := DefaultExpirationSeconds; return &v }(),
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

func (mgr *DefaultManager) CreateCSRApproval(ctx context.Context, csrName, namespace, clusterID, user string) error {
	labels := map[string]string{
		"api.openshift.com/id":   clusterID,
		"api.openshift.com/name": user,
		"api.openshift.com/type": "break-glass-credential",
	}

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

func (mgr *DefaultManager) WaitForCSRApproval(ctx context.Context, name string, timeout time.Duration) error {
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
		return fmt.Errorf("failed to create CSR watcher: %w", err)
	}
	defer watcher.Stop()

	csr, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CSR: %w", err)
	}

	if approved, denied, reason := checkCSRApprovalStatus(csr); approved {
		return nil
	} else if denied {
		return fmt.Errorf("CSR was denied: %s", reason)
	}

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

	csr, err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get CSR: %w", err)
	}

	if len(csr.Status.Certificate) > 0 {
		return csr.Status.Certificate, nil
	}

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

func (mgr *DefaultManager) CleanupCSR(ctx context.Context, name string) error {
	err := mgr.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete CertificateSigningRequests: %w", err)
	}
	return nil
}

func (mgr *DefaultManager) CleanupCSRApproval(ctx context.Context, name, namespace string) error {
	approval := &certificatesv1alpha1.CertificateSigningRequestApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err := mgr.ctrlClient.Delete(ctx, approval)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete CertificateSigningRequestApproval: %w", err)
	}
	return nil
}
