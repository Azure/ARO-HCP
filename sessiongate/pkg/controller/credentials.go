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

package controller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hypershiftclientset "github.com/openshift/hypershift/client/clientset/clientset"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/hcp"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

const (
	// SessionSecretPrefix is the prefix for session credential secrets
	SessionSecretPrefix = "session-"
	// SessionSecretSuffix is the suffix for session credential secrets
	SessionSecretSuffix = "-credentials"

	// AnnotationTargetCluster annotates secrets with the target cluster resource ID
	AnnotationTargetCluster = "sessiongate.aro-hcp.azure.com/target-cluster"

	// Secret data keys
	SecretKeyPrivateKey    = "privateKey"
	SecretKeyCertificate   = "certificate"
	SecretKeyCACertificate = "caCertificate"
	SecretKeyKASURL        = "kasURL"
)

// CredentialStatus represents the state of credential provisioning
type CredentialStatus int

const (
	// CredentialStatusError indicates an error occurred
	CredentialStatusError CredentialStatus = iota
	// CredentialStatusPrivateKeyCreated indicates the private key was created
	CredentialStatusPrivateKeyCreated
	// CredentialStatusCertificatePending indicates CSR submitted, waiting for certificate
	CredentialStatusCertificatePending
	// CredentialStatusReady indicates credentials are fully ready
	CredentialStatusReady
)

// CredentialProvider defines the interface for managing session credentials.
// Implementations are responsible for minting cluster credentials and storing
// them in Kubernetes Secrets for consumption by all controller pods.
type CredentialProvider interface {
	// EnsureCredentials ensures credentials are being provisioned for the session.
	// This method implements a phased approach:
	// 1. Create Secret with private key (if missing)
	// 2. Submit CSR for certificate (if certificate missing)
	// 3. Poll for certificate readiness (non-blocking)
	//
	// Returns:
	// - CredentialStatus: current phase of credential provisioning
	// - secretName: name of the Secret containing credentials
	// - backendKASURL: Kubernetes API server URL for the backend cluster
	// - csrName: name of the CSR (if submitted), empty otherwise
	// - error: any error encountered
	//
	// The controller should requeue based on the status:
	// - PrivateKeyCreated: requeue immediately to submit CSR
	// - CertificatePending: requeue after delay to poll for certificate
	// - Ready: proceed to session registration
	// - Error: return error for retry with backoff
	EnsureCredentials(ctx context.Context, session *sessiongatev1alpha1.Session) (CredentialStatus, string, string, string, error)

	// GetCredentialsFromSecret retrieves cluster credentials from a Secret by namespace and name.
	// This method is called by all controller pods (leader and followers) when they
	// detect a new or updated session credentials Secret via their Secret informer.
	// The implementation should use the cached lister to fetch the Secret.
	//
	// Returns the REST config for the cluster and the target cluster resource ID.
	GetCredentialsFromSecret(ctx context.Context, namespace, name string) (*rest.Config, string, error)
}

// BuildSecretName builds the secret name for a session's credentials.
//
// Example: session-my-debug-session-credentials
func BuildSecretName(sessionID string) string {
	return fmt.Sprintf("%s%s%s", SessionSecretPrefix, sessionID, SessionSecretSuffix)
}

// DefaultCredentialProvider implements CredentialProvider for Kubernetes clusters.
// It handles minting credentials for both AKS management clusters and HCP hosted clusters.
type DefaultCredentialProvider struct {
	kubeClient   kubernetes.Interface
	credential   azcore.TokenCredential
	namespace    string
	secretLister corev1listers.SecretLister
}

// NewDefaultCredentialProvider creates a new default credential provider.
func NewDefaultCredentialProvider(
	kubeClient kubernetes.Interface,
	credential azcore.TokenCredential,
	namespace string,
	secretLister corev1listers.SecretLister,
) *DefaultCredentialProvider {
	return &DefaultCredentialProvider{
		kubeClient:   kubeClient,
		credential:   credential,
		namespace:    namespace,
		secretLister: secretLister,
	}
}

// EnsureCredentials ensures credentials are being provisioned for the session.
// This implements a phased, non-blocking approach to credential creation:
//
// Phase 1: Private Key Creation
// - If Secret doesn't exist, create it with just the private key
// - Returns CredentialStatusPrivateKeyCreated
//
// Phase 2: Certificate Signing Request (CSR)
// - If privateKey exists but certificate doesn't, submit CSR
// - Returns CredentialStatusCertificatePending
//
// Phase 3: Certificate Ready
// - If both privateKey and certificate exist, credentials are ready
// - Returns CredentialStatusReady
//
// The controller should requeue based on the returned status to advance through phases.
func (p *DefaultCredentialProvider) EnsureCredentials(ctx context.Context, session *sessiongatev1alpha1.Session) (CredentialStatus, string, string, string, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))
	secretName := BuildSecretName(session.Name)

	// Check if Secret exists using lister (fast path - cached lookup)
	existingSecret, err := p.secretLister.Secrets(p.namespace).Get(secretName)

	var backendKASURL string
	var secretData map[string][]byte

	if err != nil && errors.IsNotFound(err) {
		// Phase 1: Secret doesn't exist - create with private key only
		logger.Info("Creating credentials secret with private key", "sessionID", session.Name)

		// Determine backend KAS URL from spec
		backendKASURL, err = p.determineBackendKASURL(ctx, session)
		if err != nil {
			return CredentialStatusError, "", "", "", fmt.Errorf("failed to determine backend KAS URL: %w", err)
		}

		// Generate private key
		privateKey, err := p.generatePrivateKeyForSession(ctx, session)
		if err != nil {
			return CredentialStatusError, "", "", "", fmt.Errorf("failed to generate private key: %w", err)
		}

		secretData = map[string][]byte{
			SecretKeyPrivateKey: privateKey,
			SecretKeyKASURL:     []byte(backendKASURL),
		}

		if err := p.applySecret(ctx, session, secretName, backendKASURL, secretData); err != nil {
			return CredentialStatusError, "", "", "", fmt.Errorf("failed to create secret: %w", err)
		}

		logger.V(2).Info("Created secret with private key", "secretName", secretName)
		return CredentialStatusPrivateKeyCreated, secretName, backendKASURL, "", nil

	} else if err != nil {
		return CredentialStatusError, "", "", "", fmt.Errorf("failed to check for existing secret: %w", err)
	}

	// Secret exists - check what phase we're in
	backendKASURL = string(existingSecret.Data[SecretKeyKASURL])
	if backendKASURL == "" {
		// Fallback to annotation for backward compatibility
		backendKASURL = existingSecret.Annotations[AnnotationTargetCluster]
	}

	privateKey := existingSecret.Data[SecretKeyPrivateKey]
	certificate := existingSecret.Data[SecretKeyCertificate]

	if len(privateKey) == 0 {
		return CredentialStatusError, "", "", "", fmt.Errorf("secret exists but privateKey is missing")
	}

	if len(certificate) == 0 {
		// Phase 2: Private key exists, but no certificate - submit CSR or poll
		// Check Session status for existing CSR name
		csrName := session.Status.CSRName

		if csrName == "" {
			// No CSR submitted yet - submit one
			logger.Info("Submitting CSR for certificate", "sessionID", session.Name)
			csrName, err := p.submitCSR(ctx, session, privateKey, backendKASURL)
			if err != nil {
				return CredentialStatusError, "", "", "", fmt.Errorf("failed to submit CSR: %w", err)
			}

			logger.Info("CSR submitted", "sessionID", session.Name, "csrName", csrName)
			// Return the CSR name so controller can store it in status
			return CredentialStatusCertificatePending, secretName, backendKASURL, csrName, nil
		}

		// CSR already submitted - poll for certificate
		logger.V(4).Info("Polling for certificate", "sessionID", session.Name, "csrName", csrName)
		certificate, caCert, err := p.pollForCertificate(ctx, session, csrName, backendKASURL)
		if err != nil {
			// Check if this is a "pending" error (CSR not ready yet)
			if errors.IsNotFound(err) {
				// Certificate not ready yet
				logger.V(4).Info("Certificate not ready yet, will retry", "sessionID", session.Name)
				return CredentialStatusCertificatePending, secretName, backendKASURL, csrName, nil
			}
			return CredentialStatusError, "", "", "", fmt.Errorf("failed to poll for certificate: %w", err)
		}

		// Certificate is ready - update Secret
		secretData = map[string][]byte{
			SecretKeyPrivateKey:    privateKey,
			SecretKeyCertificate:   certificate,
			SecretKeyCACertificate: caCert,
			SecretKeyKASURL:        []byte(backendKASURL),
		}

		if err := p.applySecret(ctx, session, secretName, backendKASURL, secretData); err != nil {
			return CredentialStatusError, "", "", "", fmt.Errorf("failed to update secret with certificate: %w", err)
		}

		logger.Info("Certificate obtained and stored", "secretName", secretName)
		// Return empty CSR name since certificate is ready (can be cleared from status)
		return CredentialStatusReady, secretName, backendKASURL, "", nil
	}

	// Phase 3: Both private key and certificate exist - credentials are ready
	logger.V(2).Info("Credentials are ready", "secretName", secretName)
	return CredentialStatusReady, secretName, backendKASURL, "", nil
}

// applySecret applies a Secret using Server-Side Apply
func (p *DefaultCredentialProvider) applySecret(ctx context.Context, session *sessiongatev1alpha1.Session, secretName, backendKASURL string, data map[string][]byte) error {
	applyConfig := corev1apply.Secret(secretName, p.namespace).
		WithLabels(map[string]string{
			LabelManagedBy: controllerAgentName,
		}).
		WithAnnotations(map[string]string{
			AnnotationTargetCluster: backendKASURL,
		}).
		WithOwnerReferences(
			metav1apply.OwnerReference().
				WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
				WithKind("Session").
				WithName(session.Name).
				WithUID(session.UID).
				WithController(true).
				WithBlockOwnerDeletion(true),
		).
		WithType(corev1.SecretTypeOpaque).
		WithData(data)

	_, err := p.kubeClient.CoreV1().Secrets(p.namespace).Apply(
		ctx,
		applyConfig,
		metav1.ApplyOptions{
			FieldManager: FieldManager,
			Force:        true,
		},
	)
	return err
}

// GetCredentialsFromSecret retrieves credentials from a Secret using the lister.
// This builds a REST config from the individual credential components in the Secret.
// This implements the CredentialProvider interface.
func (p *DefaultCredentialProvider) GetCredentialsFromSecret(ctx context.Context, namespace, name string) (*rest.Config, string, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "secretName", name, "namespace", namespace)

	// Fetch Secret from lister (cached)
	secret, err := p.secretLister.Secrets(namespace).Get(name)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get secret from lister: %w", err)
	}

	// Get KAS URL
	kasURL := string(secret.Data[SecretKeyKASURL])
	if kasURL == "" {
		// Fallback to annotation for backward compatibility
		kasURL = secret.Annotations[AnnotationTargetCluster]
		// Also try old data key
		if kasURL == "" {
			kasURL = string(secret.Data["targetCluster"])
		}
	}
	if kasURL == "" {
		return nil, "", fmt.Errorf("KAS URL not found in secret %s", name)
	}

	// Get credential components
	privateKey := secret.Data[SecretKeyPrivateKey]
	certificate := secret.Data[SecretKeyCertificate]
	caCertificate := secret.Data[SecretKeyCACertificate]

	// Check for legacy kubeconfig format (backward compatibility)
	if kubeconfigBytes, ok := secret.Data["kubeconfig"]; ok {
		logger.V(4).Info("Using legacy kubeconfig format from secret")
		config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse kubeconfig from secret: %w", err)
		}
		return config, kasURL, nil
	}

	// Build REST config from components
	config := &rest.Config{
		Host: kasURL,
		TLSClientConfig: rest.TLSClientConfig{
			CertData: certificate,
			KeyData:  privateKey,
			CAData:   caCertificate,
		},
	}

	logger.V(2).Info("Retrieved credentials from secret", "kasURL", kasURL)
	return config, kasURL, nil
}

// determineBackendKASURL determines the backend Kubernetes API server URL from the session spec.
// This extracts just the URL portion without minting credentials.
func (p *DefaultCredentialProvider) determineBackendKASURL(ctx context.Context, session *sessiongatev1alpha1.Session) (string, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))

	if session.Spec.HostedControlPlane != "" {
		// For HCP, we need to discover the hosted cluster to get the KAS URL
		logger.V(2).Info("Determining backend KAS URL for hosted control plane", "hcp", session.Spec.HostedControlPlane)

		// Get management cluster REST config
		mgmtConfig, err := mc.GetAKSRESTConfig(ctx, session.Spec.ManagementCluster, p.credential)
		if err != nil {
			return "", fmt.Errorf("failed to create REST config for management cluster: %w", err)
		}

		// Create a hypershift client for the management cluster
		hypershiftClient, err := hypershiftclientset.NewForConfig(mgmtConfig)
		if err != nil {
			return "", fmt.Errorf("failed to create hypershift client: %w", err)
		}

		// Discover the hosted cluster
		hcpDiscovery := mc.NewDiscovery(hypershiftClient)
		resourceID, err := azcorearm.ParseResourceID(session.Spec.HostedControlPlane)
		if err != nil {
			return "", fmt.Errorf("failed to parse hosted control plane resource ID: %w", err)
		}
		hcpInfo, err := hcpDiscovery.DiscoverClusterByResourceID(ctx, resourceID)
		if err != nil {
			return "", fmt.Errorf("failed to find hosted control plane: %w", err)
		}

		// Build the KAS URL for the HCP
		kasURL := fmt.Sprintf("https://%s:443", hcpInfo.APIServerDNSName)
		logger.V(2).Info("Determined backend KAS URL", "kasURL", kasURL)
		return kasURL, nil
	}

	// For management cluster, get the KAS URL from AKS credentials
	logger.V(2).Info("Determining backend KAS URL for management cluster")
	restConfig, err := mc.GetAKSRESTConfig(ctx, session.Spec.ManagementCluster, p.credential)
	if err != nil {
		return "", fmt.Errorf("failed to create REST config for management cluster: %w", err)
	}

	logger.V(2).Info("Determined backend KAS URL", "kasURL", restConfig.Host)
	return restConfig.Host, nil
}

// generatePrivateKeyForSession generates a private key for the session.
// For management clusters, this is not needed (uses Azure tokens).
// For HCPs, this generates an RSA private key for certificate-based auth.
func (p *DefaultCredentialProvider) generatePrivateKeyForSession(ctx context.Context, session *sessiongatev1alpha1.Session) ([]byte, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))

	if session.Spec.HostedControlPlane == "" {
		// Management cluster doesn't use private keys (uses Azure tokens)
		logger.V(2).Info("Management cluster - no private key needed")
		return []byte{}, nil
	}

	// HCP requires a private key for certificate-based authentication
	logger.V(2).Info("Generating private key for hosted control plane")
	privateKey, err := hcp.GeneratePrivateKey(2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	return hcp.EncodePrivateKey(privateKey), nil
}

// submitCSR submits a CSR for the session and returns the CSR name.
// This is called once when credentials need to be minted.
func (p *DefaultCredentialProvider) submitCSR(ctx context.Context, session *sessiongatev1alpha1.Session, privateKeyPEM []byte, backendKASURL string) (string, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))

	if session.Spec.HostedControlPlane == "" {
		// Management cluster doesn't use certificates (uses Azure tokens)
		logger.V(2).Info("Management cluster - no certificate needed")
		return "", nil
	}

	logger.Info("Submitting CSR for hosted control plane")

	// Step 1: Parse private key from PEM
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return "", fmt.Errorf("failed to decode private key PEM")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Step 2: Generate CSR
	user := session.Spec.AccessLevel.Group
	subject := hcp.BuildSubject(user, session.Spec.AccessLevel.Group)
	csrPEM, err := hcp.GenerateCSR(privateKey, subject)
	if err != nil {
		return "", fmt.Errorf("failed to generate CSR: %w", err)
	}

	// Step 3: Get management cluster config to access HCP namespace
	mgmtConfig, err := mc.GetAKSRESTConfig(ctx, session.Spec.ManagementCluster, p.credential)
	if err != nil {
		return "", fmt.Errorf("failed to create REST config for management cluster: %w", err)
	}

	// Step 4: Discover HCP info to get namespace and cluster ID
	hypershiftClient, err := hypershiftclientset.NewForConfig(mgmtConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create hypershift client: %w", err)
	}

	hcpDiscovery := mc.NewDiscovery(hypershiftClient)
	resourceID, err := azcorearm.ParseResourceID(session.Spec.HostedControlPlane)
	if err != nil {
		return "", fmt.Errorf("failed to parse hosted control plane resource ID: %w", err)
	}
	hcpInfo, err := hcpDiscovery.DiscoverClusterByResourceID(ctx, resourceID)
	if err != nil {
		return "", fmt.Errorf("failed to discover hosted control plane: %w", err)
	}

	// Step 5: Create CSR manager and submit CSR
	csrManager, err := hcp.NewCSRManager(mgmtConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR manager: %w", err)
	}

	csrName, err := csrManager.CreateCSR(ctx, csrPEM, session.Name, hcpInfo.ID, user, hcpInfo.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %w", err)
	}

	// Step 6: Create CSR approval
	if err := csrManager.CreateCSRApproval(ctx, csrName, hcpInfo.Namespace, hcpInfo.ID, user); err != nil {
		return "", fmt.Errorf("failed to create CSR approval: %w", err)
	}

	logger.Info("CSR submitted and approval created", "csrName", csrName, "namespace", hcpInfo.Namespace)
	return csrName, nil
}

// pollForCertificate polls for a certificate for an existing CSR.
// Returns the certificate and CA certificate if ready, or NotFound error if still pending.
func (p *DefaultCredentialProvider) pollForCertificate(ctx context.Context, session *sessiongatev1alpha1.Session, csrName string, backendKASURL string) ([]byte, []byte, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))

	if session.Spec.HostedControlPlane == "" {
		// Management cluster doesn't use certificates (uses Azure tokens)
		logger.V(2).Info("Management cluster - no certificate needed")
		return []byte{}, []byte{}, nil
	}

	logger.V(4).Info("Polling for certificate", "csrName", csrName)

	// Step 1: Get management cluster config to access CSR
	mgmtConfig, err := mc.GetAKSRESTConfig(ctx, session.Spec.ManagementCluster, p.credential)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create REST config for management cluster: %w", err)
	}

	// Step 2: Get the CSR to check if certificate is available
	// We use a non-blocking check - if certificate isn't ready, return NotFound
	kubeClient, err := kubernetes.NewForConfig(mgmtConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	csr, err := kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get CSR: %w", err)
	}

	// Check if certificate is available
	if len(csr.Status.Certificate) == 0 {
		logger.V(4).Info("Certificate not ready yet", "csrName", csrName)
		return nil, nil, errors.NewNotFound(corev1.Resource("certificate"), "certificate-pending")
	}

	certificate := csr.Status.Certificate
	logger.V(2).Info("Certificate ready", "csrName", csrName)

	// Step 4: Get HCP info to fetch the CA certificate
	hypershiftClient, err := hypershiftclientset.NewForConfig(mgmtConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create hypershift client: %w", err)
	}

	hcpDiscovery := mc.NewDiscovery(hypershiftClient)
	resourceID, err := azcorearm.ParseResourceID(session.Spec.HostedControlPlane)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse hosted control plane resource ID: %w", err)
	}
	hcpInfo, err := hcpDiscovery.DiscoverClusterByResourceID(ctx, resourceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover hosted control plane: %w", err)
	}

	// Step 5: Get kube-apiserver TLS certificate (CA certificate)
	const kasCertSecret = "kube-apiserver-tls-cert"
	const kasCertKey = "tls.crt"

	secret, err := kubeClient.CoreV1().Secrets(hcpInfo.Namespace).Get(ctx, kasCertSecret, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get kube-apiserver certificate secret: %w", err)
	}

	serverCert, ok := secret.Data[kasCertKey]
	if !ok {
		return nil, nil, fmt.Errorf("server certificate not found in secret %s/%s", hcpInfo.Namespace, kasCertSecret)
	}

	// Step 6: Determine CA certificate to use
	// If the server cert is self-signed, use it as the CA
	var caCertToUse []byte
	if isSelfSigned, err := isCertSelfSigned(serverCert); err != nil {
		return nil, nil, fmt.Errorf("failed to check if certificate is self-signed: %w", err)
	} else if isSelfSigned {
		// Self-signed: use server cert as CA
		caCertToUse = serverCert
	}
	// Otherwise caCertToUse remains nil, system trust store will be used

	logger.Info("Certificate and CA retrieved successfully", "csrName", csrName)
	return certificate, caCertToUse, nil
}

// isCertSelfSigned checks if a PEM-encoded certificate is self-signed
func isCertSelfSigned(certPEM []byte) (bool, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, fmt.Errorf("failed to decode PEM certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// A certificate is self-signed if the issuer equals the subject
	return cert.Issuer.String() == cert.Subject.String(), nil
}
