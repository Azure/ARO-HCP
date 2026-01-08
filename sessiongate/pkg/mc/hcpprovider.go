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

package mc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	certificatesv1alpha1apply "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
	hypershiftclientset "github.com/openshift/hypershift/client/clientset/clientset"
	certificatesclientv1alpha1 "github.com/openshift/hypershift/client/clientset/clientset/typed/certificates/v1alpha1"
)

const (
	defaultExpirationSeconds = int32(86353) // ~24 hours

	// AnnotationCSRDigest is used to track the digest of the CSR inputs (private key + subject)
	AnnotationCSRDigest = "sessiongate.aro-hcp.azure.com/csr-digest"

	// AnnotationClusterReference is used to track the cluster cluster backref for a HostedControlPlane CR
	AnnotationClusterReference = "hypershift.openshift.io/cluster"
)

type HostedClusterProvider interface {
	GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error)

	// MintCertificate mints break-glass credentials for a hosted control plane.
	// This function is idempotent - safe to call repeatedly until certificate is returned.
	MintCertificate(ctx context.Context, sessionName string, user string, accessGroup string, hostedCluster *hypershiftv1beta1.HostedCluster, privateKey *rsa.PrivateKey) ([]byte, error)
}

// HostedClusterProvider handles hosted cluster discovery operations.
type hostedClusterProvider struct {
	hypershiftClient   hypershiftclientset.Interface
	certificatesClient certificatesclientv1alpha1.CertificatesV1alpha1Interface
	kubeClient         kubernetes.Interface
}

// NewHostedClusterProvider creates a new hosted cluster provider instance.
func NewHCPProvider(hypershiftClient hypershiftclientset.Interface, certificatesClient certificatesclientv1alpha1.CertificatesV1alpha1Interface, kubeClient kubernetes.Interface) HostedClusterProvider {
	return &hostedClusterProvider{
		hypershiftClient:   hypershiftClient,
		certificatesClient: certificatesClient,
		kubeClient:         kubeClient,
	}
}

func (d *hostedClusterProvider) getHostedControlPlane(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
	hcpList, err := d.hypershiftClient.HypershiftV1beta1().HostedControlPlanes(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list HostedControlPlanes: %w", err)
	}
	if len(hcpList.Items) == 0 {
		return nil, apierrors.NewNotFound(
			schema.GroupResource{Group: "hypershift.openshift.io", Resource: "hostedcontrolplanes"},
			namespace,
		)
	}
	if len(hcpList.Items) > 1 {
		return nil, fmt.Errorf("multiple HostedControlPlane found for namespace %s", namespace)
	}
	hcp := hcpList.Items[0]
	return &hcp, nil
}

// GetHostedClusterForHCPNamespace finds the HostedCluster CR for a given HostedControlPlane namespace.
func (d *hostedClusterProvider) GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error) {
	hcp, err := d.getHostedControlPlane(ctx, namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}
	hcRef := hcp.Annotations[AnnotationClusterReference]
	hcNamespace, hcName, err := parseClusterRef(hcRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cluster reference: %w", err)
	}
	hc, err := d.hypershiftClient.HypershiftV1beta1().HostedClusters(hcNamespace).Get(ctx, hcName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get HostedCluster: %w", err)
	}
	return hc, nil
}

func parseClusterRef(clusterRef string) (string, string, error) {
	parts := strings.Split(clusterRef, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid cluster reference: %s", clusterRef)
	}
	return parts[0], parts[1], nil
}

func (d *hostedClusterProvider) MintCertificate(ctx context.Context, sessionName string, user string, accessGroup string, hostedCluster *hypershiftv1beta1.HostedCluster, privateKey *rsa.PrivateKey) ([]byte, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "sessionName", sessionName)

	csrApprovalNamespace := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)

	csr, err := d.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, sessionName, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		csr = nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to check for existing CSR: %w", err)
	}

	subject := buildSubject(user, accessGroup)

	digest := calculateCSRDigest(privateKey, subject)
	if csr != nil {
		if existingDigest, ok := csr.Annotations[AnnotationCSRDigest]; ok && existingDigest != digest {
			logger.V(2).Info("Deleting outdated CertificateSigningRequest", "sessionName", sessionName, "csrApprovalNamespace", csrApprovalNamespace, "existingDigest", existingDigest, "digest", digest)
			if err := d.deleteCSR(ctx, csrApprovalNamespace, sessionName); err != nil {
				return nil, fmt.Errorf("failed to delete outdated CSR: %w", err)
			}
			csr = nil
		}
	}

	// create CertificateSigningRequest resource
	if csr == nil {
		logger.V(2).Info("Creating CertificateSigningRequest", "csrName", sessionName)
		csrPEM, err := generateCertificateSigningRequestPEM(rand.Reader, privateKey, subject)
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificate signing request PEM: %w", err)
		}
		csrResource := d.buildCertificateSigningRequest(csrApprovalNamespace, sessionName, csrPEM, digest)
		_, err = d.kubeClient.CertificatesV1().CertificateSigningRequests().Create(ctx, csrResource, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create certificatesigningrequest.certificates.k8s.io: %w", err)
		}
	}

	// create Hypershift CertificateSigningRequestApproval resource
	if err := d.ensureApproval(ctx, csrApprovalNamespace, sessionName); err != nil {
		return nil, fmt.Errorf("failed to create certificatesigningrequestapprovals.certificates.hypershift.openshift.io: %w", err)
	}

	if csr != nil && len(csr.Status.Certificate) > 0 {
		return csr.Status.Certificate, nil
	}
	return nil, nil
}

func (d *hostedClusterProvider) buildCertificateSigningRequest(csrApprovalNamespace, name string, csrPEM []byte, digest string) *certificatesv1.CertificateSigningRequest {
	return &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"api.openshift.com/type": "break-glass-credential",
			},
			Annotations: map[string]string{
				AnnotationCSRDigest: digest,
			},
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        fmt.Sprintf("hypershift.openshift.io/%s.sre-break-glass", csrApprovalNamespace),
			ExpirationSeconds: func() *int32 { v := defaultExpirationSeconds; return &v }(),
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			},
		},
	}
}

func buildSubject(user string, organization string) pkix.Name {
	return pkix.Name{
		CommonName:   fmt.Sprintf("system:sre-break-glass:%s", user),
		Organization: []string{organization},
	}
}

func (d *hostedClusterProvider) ensureApproval(ctx context.Context, namespace, name string) error {
	approvalApplyConfig := certificatesv1alpha1apply.CertificateSigningRequestApproval(name, namespace).
		WithLabels(map[string]string{
			"api.openshift.com/type": "break-glass-credential",
		})

	_, err := d.certificatesClient.CertificateSigningRequestApprovals(namespace).Apply(
		ctx,
		approvalApplyConfig,
		metav1.ApplyOptions{
			FieldManager: "sessiongate-controller",
			Force:        true,
		},
	)
	return err
}

func (d *hostedClusterProvider) deleteCSR(ctx context.Context, namespace string, name string) error {
	csrErr := d.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, name, metav1.DeleteOptions{})
	approvalErr := d.certificatesClient.CertificateSigningRequestApprovals(namespace).Delete(ctx, name, metav1.DeleteOptions{})

	var errs []error
	if csrErr != nil && !apierrors.IsNotFound(csrErr) {
		errs = append(errs, fmt.Errorf("failed to delete CSR: %w", csrErr))
	}
	if approvalErr != nil && !apierrors.IsNotFound(approvalErr) {
		errs = append(errs, fmt.Errorf("failed to delete approval: %w", approvalErr))
	}

	return errors.Join(errs...)
}

func (d *hostedClusterProvider) GetCSRApproval(ctx context.Context, namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	return d.certificatesClient.CertificateSigningRequestApprovals(namespace).Get(ctx, name, metav1.GetOptions{})
}

func calculateCSRDigest(privateKey *rsa.PrivateKey, subject pkix.Name) string {
	h := sha256.New()
	h.Write(x509.MarshalPKCS1PrivateKey(privateKey))
	h.Write([]byte(subject.String()))
	return hex.EncodeToString(h.Sum(nil))
}

func generateCertificateSigningRequestPEM(rngSource io.Reader, privateKey *rsa.PrivateKey, subject pkix.Name) ([]byte, error) {
	template := x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rngSource, &template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	// Encode to PEM
	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return csrPEM, nil
}

type HCPProviderBuilder func(ctx context.Context, resourceId string) (HostedClusterProvider, error)

func NewAKSHCPProviderBuilder(azureCredentials azcore.TokenCredential) HCPProviderBuilder {
	return func(ctx context.Context, resourceId string) (HostedClusterProvider, error) {
		kubeConfig, err := GetAKSRESTConfig(ctx, resourceId, azureCredentials)
		if err != nil {
			return nil, fmt.Errorf("failed to get AKS REST config: %w", err)
		}
		kubeClient, err := kubernetes.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		hypershiftClientset, err := hypershiftclientset.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create hypershift clientset: %w", err)
		}
		certificatesClientset, err := certificatesclientv1alpha1.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create certificates clientset: %w", err)
		}
		return NewHCPProvider(hypershiftClientset, certificatesClientset, kubeClient), nil
	}
}

// ManagementClusterProvider provides access to management cluster resources
type ManagementClusterQuerier interface {
	GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error)
	GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error)
	GetCSRApproval(ctx context.Context, namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error)
}

type ManagementClusterProviderBuilder func(ctx context.Context, resourceId string) (*ManagementClusterProvider, error)

func NewAKSManagermentClusterBuilder(azureCredentials azcore.TokenCredential) ManagementClusterProviderBuilder {
	// todo: informers and instance caching
	return func(ctx context.Context, resourceId string) (*ManagementClusterProvider, error) {
		kubeConfig, err := GetAKSRESTConfig(ctx, resourceId, azureCredentials)
		if err != nil {
			return nil, fmt.Errorf("failed to get AKS REST config: %w", err)
		}
		kubeClient, err := kubernetes.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		hypershiftClientset, err := hypershiftclientset.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create hypershift clientset: %w", err)
		}
		certificatesClientset, err := certificatesclientv1alpha1.NewForConfig(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create certificates clientset: %w", err)
		}
		return &ManagementClusterProvider{
			HypershiftClient:   hypershiftClientset,
			CertificatesClient: certificatesClientset,
			KubeClient:         kubeClient,
		}, nil
	}
}

// managementClusterProvider implements ManagementClusterProvider
type ManagementClusterProvider struct {
	HypershiftClient   hypershiftclientset.Interface
	CertificatesClient certificatesclientv1alpha1.CertificatesV1alpha1Interface
	KubeClient         kubernetes.Interface
}

func (d *ManagementClusterProvider) getHostedControlPlane(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
	hcpList, err := d.HypershiftClient.HypershiftV1beta1().HostedControlPlanes(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list HostedControlPlanes: %w", err)
	}
	if len(hcpList.Items) == 0 {
		return nil, apierrors.NewNotFound(
			schema.GroupResource{Group: "hypershift.openshift.io", Resource: "hostedcontrolplanes"},
			namespace,
		)
	}
	if len(hcpList.Items) > 1 {
		return nil, fmt.Errorf("multiple HostedControlPlane found for namespace %s", namespace)
	}
	hcp := hcpList.Items[0]
	return &hcp, nil
}

// GetHostedCluster finds the HostedCluster CR for a given HostedControlPlane namespace.
func (d *ManagementClusterProvider) GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error) {
	hcp, err := d.getHostedControlPlane(ctx, namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}
	hcRef := hcp.Annotations[AnnotationClusterReference]
	hcNamespace, hcName, err := parseClusterRef(hcRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cluster reference: %w", err)
	}
	hc, err := d.HypershiftClient.HypershiftV1beta1().HostedClusters(hcNamespace).Get(ctx, hcName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get HostedCluster: %w", err)
	}
	return hc, nil
}

func (d *ManagementClusterProvider) GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error) {
	return d.KubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, name, metav1.GetOptions{})
}

func (d *ManagementClusterProvider) GetCSRApproval(ctx context.Context, hostedControlPlaneNamespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	return d.CertificatesClient.CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Get(ctx, name, metav1.GetOptions{})
}
