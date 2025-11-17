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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
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
	SecretKeyPrivateKey  = "privateKey"
	SecretKeyCertificate = "certificate"
	SecretKeyKASURL      = "kasURL"

	// RSAKeyBits is the size in bits for generated RSA private keys
	RSAKeyBits = 2048
)

// CredentialRequest contains the minimal fields needed to provision credentials for a session.
// This is extracted from Session to avoid passing the full object to credential provisioning logic.
type CredentialRequest struct {
	// SessionName is the name of the Session resource (used for CSR naming and owner references)
	SessionName string

	// SessionUID is the UID of the Session resource (used for owner references)
	SessionUID types.UID

	// ManagementClusterID is the Azure resource ID of the management cluster
	ManagementClusterID string

	// HCPNamespace is the namespace where the HostedControlPlane CR exists
	HCPNamespace string

	// UserPrincipalName is the user principal name (e.g., user@domain.com)
	UserPrincipalName string

	// AccessLevelGroup is the name of the access group for RBAC
	AccessLevelGroup string
}

// CredentialReference contains the minimal fields needed to retrieve credentials from a secret.
// This is extracted from Session.Status to avoid passing the full object to credential retrieval logic.
type CredentialReference struct {
	// SecretName is the name of the Secret containing the credentials
	SecretName string

	// BackendKASURL is the Kubernetes API server URL for the backend cluster
	BackendKASURL string
}

// CredentialStatus represents the state of credential provisioning
type CredentialStatus int

const (
	// CredentialStatusError indicates an error occurred
	CredentialStatusError CredentialStatus = iota
	// CredentialStatusHostedControlPlaneNotFound indicates the target HCP doesn't exist yet
	CredentialStatusHostedControlPlaneNotFound
	// CredentialStatusPrivateKeyCreated indicates the private key was created
	CredentialStatusPrivateKeyCreated
	// CredentialStatusCertificatePending indicates CSR submitted, waiting for certificate
	CredentialStatusCertificatePending
	// CredentialStatusReady indicates credentials are fully ready
	CredentialStatusReady
)

// CredentialMintingStatus contains the result of credential provisioning
type CredentialMintingStatus struct {
	// Status is the current phase of credential provisioning
	Status CredentialStatus
	// SecretName is the name of the Secret containing credentials
	SecretName string
}

// SecretStore defines the interface for storing and retrieving credential data.
// This abstraction separates Kubernetes Secret operations from credential minting logic.
type SecretStore interface {
	// StoreCredential stores credential components in a Secret with the specified owner reference.
	// privateKey must be non-nil. certificate may be nil for the initial private key storage phase.
	StoreCredential(ctx context.Context, secretName, ownerName string, ownerUID types.UID, privateKey *rsa.PrivateKey, certificate []byte) error

	// GetPrivateKey retrieves and decodes the private key from the specified Secret.
	// Returns an error if the Secret doesn't exist or doesn't contain a valid private key.
	GetPrivateKey(secretName string) (*rsa.PrivateKey, error)

	// GetCertificate retrieves the certificate from the specified Secret.
	// Returns an error if the Secret doesn't exist or doesn't contain a certificate.
	GetCertificate(secretName string) ([]byte, error)
}

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
	// The controller should requeue based on the result status:
	// - PrivateKeyCreated: requeue immediately to submit CSR
	// - CertificatePending: requeue after delay to poll for certificate
	// - Ready: proceed to session registration
	// - Error status in result or non-nil error: retry with backoff
	EnsureCredentials(ctx context.Context, req CredentialRequest) (*CredentialMintingStatus, error)

	// GetCredentialsFromSecret retrieves cluster credentials from a Secret.
	// This method is called by all controller pods (leader and followers) when they
	// detect a new or updated session credentials Secret via their Secret informer.
	//
	// Returns the REST config for the cluster and the target cluster resource ID.
	GetCredentialsFromSecret(ctx context.Context, ref CredentialReference) (*rest.Config, string, error)
}

// secretNameFromRequest generates the secret name from a CredentialRequest.
func secretNameFromRequest(req CredentialRequest) string {
	return fmt.Sprintf("%s%s%s", SessionSecretPrefix, req.SessionName, SessionSecretSuffix)
}

// DefaultSecretStore implements SecretStore using Kubernetes Secrets.
type DefaultSecretStore struct {
	kubeClient   kubernetes.Interface
	namespace    string
	secretLister corev1listers.SecretLister
}

// NewDefaultSecretStore creates a new default secret store.
func NewDefaultSecretStore(
	kubeClient kubernetes.Interface,
	namespace string,
	secretLister corev1listers.SecretLister,
) *DefaultSecretStore {
	return &DefaultSecretStore{
		kubeClient:   kubeClient,
		namespace:    namespace,
		secretLister: secretLister,
	}
}

// StoreCredential stores credential components in a Secret using Server-Side Apply.
func (s *DefaultSecretStore) StoreCredential(ctx context.Context, secretName, ownerName string, ownerUID types.UID, privateKey *rsa.PrivateKey, certificate []byte) error {
	data := map[string][]byte{
		SecretKeyPrivateKey: pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		}),
		SecretKeyCertificate: certificate,
	}

	applyConfig := corev1apply.Secret(secretName, s.namespace).
		WithLabels(map[string]string{
			LabelManagedBy: ControllerAgentName,
		}).
		WithOwnerReferences(
			metav1apply.OwnerReference().
				WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
				WithKind("Session").
				WithName(ownerName).
				WithUID(ownerUID).
				WithController(true).
				WithBlockOwnerDeletion(true),
		).
		WithType(corev1.SecretTypeOpaque).
		WithData(data)

	_, err := s.kubeClient.CoreV1().Secrets(s.namespace).Apply(
		ctx,
		applyConfig,
		metav1.ApplyOptions{
			FieldManager: ControllerAgentName,
			Force:        true,
		},
	)
	return err
}

// GetPrivateKey retrieves and decodes the private key from the specified Secret.
func (s *DefaultSecretStore) GetPrivateKey(secretName string) (*rsa.PrivateKey, error) {
	privateKeyBytes, err := s.getPrivateKeyBytes(secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key from secret: %w", err)
	}
	block, _ := pem.Decode(privateKeyBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode private key PEM")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// getPrivateKeyBytes retrieves the raw private key bytes from the Secret.
func (s *DefaultSecretStore) getPrivateKeyBytes(secretName string) ([]byte, error) {
	secret, err := s.secretLister.Secrets(s.namespace).Get(secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	if len(secret.Data[SecretKeyPrivateKey]) == 0 {
		return nil, fmt.Errorf("private key not found in secret")
	}
	return secret.Data[SecretKeyPrivateKey], nil
}

// GetCertificate retrieves the certificate from the specified Secret.
func (s *DefaultSecretStore) GetCertificate(secretName string) ([]byte, error) {
	secret, err := s.secretLister.Secrets(s.namespace).Get(secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}
	if len(secret.Data[SecretKeyCertificate]) == 0 {
		return nil, fmt.Errorf("certificate not found in secret")
	}
	return secret.Data[SecretKeyCertificate], nil
}

// DefaultCredentialProvider implements CredentialProvider for Kubernetes clusters.
type DefaultCredentialProvider struct {
	secretStore        SecretStore
	hcpProviderBuilder mc.HCPProviderBuilder
}

// NewDefaultCredentialProvider creates a new default credential provider.
func NewDefaultCredentialProvider(
	secretStore SecretStore,
	hcpProviderBuilder mc.HCPProviderBuilder,
) *DefaultCredentialProvider {
	return &DefaultCredentialProvider{
		secretStore:        secretStore,
		hcpProviderBuilder: hcpProviderBuilder,
	}
}

// EnsureCredentials ensures credentials are being provisioned for the session.
// This implements a phased, non-blocking approach to credential creation:
//
// Phase 1: Private Key Creation
// - If Secret doesn't exist, create it with just the private key
// - Returns CredentialStatusPrivateKeyCreated
//
// Phase 2: Certificate Signing Request (CSR) + Hypershift Approval
// - If privateKey exists but certificate doesn't, submit CSR
// - Returns CredentialStatusCertificatePending
//
// Phase 3: Certificate Ready
// - If both privateKey and certificate exist, credentials are ready
// - Returns CredentialStatusReady
//
// The controller should requeue based on the returned status to advance through phases.
func (p *DefaultCredentialProvider) EnsureCredentials(ctx context.Context, req CredentialRequest) (*CredentialMintingStatus, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", req.SessionName)
	ctx = klog.NewContext(ctx, logger)

	hcpprovider, err := p.hcpProviderBuilder(ctx, req.ManagementClusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get hosted cluster provider: %w", err)
	}
	hostedCluster, err := hcpprovider.GetHostedCluster(ctx, req.HCPNamespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &CredentialMintingStatus{
				Status: CredentialStatusHostedControlPlaneNotFound,
			}, nil
		}
		return nil, fmt.Errorf("failed to get hosted cluster: %w", err)
	}

	secretName := secretNameFromRequest(req)

	// private key generation
	privateKey, err := p.secretStore.GetPrivateKey(secretName)
	if err != nil {
		privateKey, err = generatePrivateKeyWithReader(rand.Reader, RSAKeyBits)
		if err != nil {
			return nil, fmt.Errorf("failed to generate private key: %w", err)
		}
		err := p.secretStore.StoreCredential(ctx, secretName, req.SessionName, req.SessionUID, privateKey, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to store credentials secret: %w", err)
		}
		return &CredentialMintingStatus{
			Status:     CredentialStatusPrivateKeyCreated,
			SecretName: secretName,
		}, nil
	}

	// certificate minting
	_, err = p.secretStore.GetCertificate(secretName)
	// todo - handle different user types
	if err != nil {
		logger.V(2).Info("Minting certificate", "user", req.UserPrincipalName, "accessGroup", req.AccessLevelGroup)
		certificate, err := hcpprovider.MintCertificate(
			ctx,
			req.SessionName,
			req.UserPrincipalName,
			req.AccessLevelGroup,
			hostedCluster,
			privateKey,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to mint credentials: %w", err)
		}

		if len(certificate) == 0 {
			logger.V(4).Info("Certificate still pending")
			return &CredentialMintingStatus{
				Status:     CredentialStatusCertificatePending,
				SecretName: secretName,
			}, nil
		}

		logger.V(4).Info("Certificate ready, storing in secret", "secretName", secretName)
		if err := p.secretStore.StoreCredential(ctx, secretName, req.SessionName, req.SessionUID, privateKey, certificate); err != nil {
			return nil, fmt.Errorf("failed to update secret with certificate: %w", err)
		}
	}

	return &CredentialMintingStatus{
		Status:     CredentialStatusReady,
		SecretName: secretName,
	}, nil
}

// GetCredentialsFromSecret builds a REST config from the credentials found in the secret.
func (p *DefaultCredentialProvider) GetCredentialsFromSecret(ctx context.Context, ref CredentialReference) (*rest.Config, string, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "secretName", ref.SecretName)

	if ref.SecretName == "" {
		return nil, "", fmt.Errorf("secret name is empty")
	}

	if ref.BackendKASURL == "" {
		return nil, "", fmt.Errorf("backend KAS URL is empty")
	}

	privateKey, err := p.secretStore.GetPrivateKey(ref.SecretName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get private key: %w", err)
	}

	certificate, err := p.secretStore.GetCertificate(ref.SecretName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get certificate: %w", err)
	}

	// Encode private key to PEM format for REST config
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	config := &rest.Config{
		Host: ref.BackendKASURL,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
			CertData: certificate,
			KeyData:  privateKeyPEM,
		},
	}

	logger.V(2).Info("Retrieved credentials", "secretName", ref.SecretName)
	return config, ref.BackendKASURL, nil
}

func generatePrivateKeyWithReader(reader io.Reader, bits int) (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(reader, bits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return key, nil
}
