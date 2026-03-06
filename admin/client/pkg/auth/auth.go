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

package auth

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/pkcs12"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// CertificateAuthConfig holds configuration for certificate-based authentication
type CertificateAuthConfig struct {
	ClientID              string
	KeyVaultURL           string
	CertificateSecretName string
	TenantID              string
	Scopes                []string
	SendX5C               bool
}

// AzureADClaims contains Azure AD-specific JWT claims
type AzureADClaims struct {
	jwt.RegisteredClaims
	AppID          string   `json:"appid"`
	AppDisplayName string   `json:"app_displayname"`
	TenantID       string   `json:"tid"`
	ObjectID       string   `json:"oid"`
	IdentityType   string   `json:"idtyp"`
	Roles          []string `json:"roles,omitempty"`
	DirectoryRoles []string `json:"wids,omitempty"`
}

// GetCertificateCredential downloads a certificate from Azure Key Vault and creates a credential
func GetCertificateCredential(ctx context.Context, config CertificateAuthConfig) (azcore.TokenCredential, error) {
	creds, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create Key Vault credential: %w", err)
	}

	secretClient, err := azsecrets.NewClient(config.KeyVaultURL, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Key Vault client: %w", err)
	}

	secretResponse, err := secretClient.GetSecret(ctx, config.CertificateSecretName, "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate secret from Key Vault: %w", err)
	}

	if secretResponse.Value == nil {
		return nil, fmt.Errorf("%s certificate secret value from Key Vault %s is nil", config.CertificateSecretName, config.KeyVaultURL)
	}
	pfxData, err := base64.StdEncoding.DecodeString(*secretResponse.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to decode certificate data: %w", err)
	}

	privateKey, certificate, err := pkcs12.Decode(pfxData, "")
	if err != nil {
		return nil, fmt.Errorf("failed to decode PKCS12 certificate: %w", err)
	}

	if config.TenantID == "" {
		return nil, fmt.Errorf("tenant ID is required")
	}

	var privKey crypto.PrivateKey = privateKey

	certCredential, err := azidentity.NewClientCertificateCredential(
		config.TenantID,
		config.ClientID,
		[]*x509.Certificate{certificate},
		privKey,
		&azidentity.ClientCertificateCredentialOptions{
			SendCertificateChain: config.SendX5C,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate credential: %w", err)
	}

	return certCredential, nil
}

// GetBearerToken acquires an Azure AD bearer token using certificate-based authentication
func GetBearerToken(ctx context.Context, config CertificateAuthConfig) (string, error) {
	// Get certificate credential
	credential, err := GetCertificateCredential(ctx, config)
	if err != nil {
		return "", err
	}

	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: config.Scopes,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get bearer token: %w", err)
	}

	return token.Token, nil
}
