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
	"os"
	"strings"

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
	// Create Azure credential for accessing Key Vault
	kvCredential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Key Vault credential: %w", err)
	}

	// Create Key Vault client
	secretClient, err := azsecrets.NewClient(config.KeyVaultURL, kvCredential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Key Vault client: %w", err)
	}

	// Download certificate secret
	secretResponse, err := secretClient.GetSecret(ctx, config.CertificateSecretName, "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate secret from Key Vault: %w", err)
	}

	// The secret is stored as base64-encoded PKCS12/PFX
	pfxData, err := base64.StdEncoding.DecodeString(*secretResponse.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to decode certificate data: %w", err)
	}

	// Parse PKCS12 data to extract private key and certificate
	privateKey, certificate, err := pkcs12.Decode(pfxData, "")
	if err != nil {
		return nil, fmt.Errorf("failed to decode PKCS12 certificate: %w", err)
	}

	// Build certificate chain (single certificate for now)
	certs := []*x509.Certificate{certificate}

	// Tenant ID must be provided
	if config.TenantID == "" {
		return nil, fmt.Errorf("tenant ID is required")
	}

	// Type assert to crypto.PrivateKey as expected by azidentity
	var privKey crypto.PrivateKey = privateKey

	certCredential, err := azidentity.NewClientCertificateCredential(
		config.TenantID,
		config.ClientID,
		certs,
		privKey,
		&azidentity.ClientCertificateCredentialOptions{
			SendCertificateChain: config.SendX5C, // Enable SendX5C only if required by app registration
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

	// Default scopes if not provided
	scopes := config.Scopes
	if len(scopes) == 0 {
		// Check for custom audience from environment
		customAudience := os.Getenv("AZURE_AUDIENCE")
		if customAudience != "" {
			scopes = []string{fmt.Sprintf("%s/.default", customAudience)}
		} else {
			scopes = []string{"https://management.azure.com/.default"}
		}
	}

	// Get token for the specified scopes
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: scopes,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get bearer token: %w", err)
	}

	// Decode and print token information
	if err := PrintTokenInfo(token.Token); err != nil {
		fmt.Printf("[WARNING] Failed to decode token info: %v\n", err)
	}

	return token.Token, nil
}

// PrintTokenInfo decodes and prints information about the JWT token
func PrintTokenInfo(tokenString string) error {
	// Parse the token without validation (we just want to read the claims)
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())

	var claims AzureADClaims
	token, _, err := parser.ParseUnverified(tokenString, &claims)
	if err != nil {
		return fmt.Errorf("failed to parse JWT token: %w", err)
	}

	// Print header information
	fmt.Println("\n[JWT Header Information]")
	if alg, ok := token.Header["alg"].(string); ok {
		fmt.Printf("  Algorithm (alg): %s\n", alg)
	}
	if typ, ok := token.Header["typ"].(string); ok {
		fmt.Printf("  Type (typ): %s\n", typ)
	}
	if kid, ok := token.Header["kid"].(string); ok {
		fmt.Printf("  Key ID (kid): %s\n", kid)
	}

	// Check for X5C certificate chain
	if x5c, ok := token.Header["x5c"].([]interface{}); ok && len(x5c) > 0 {
		fmt.Printf("  X5C Certificate Chain: %d certificate(s) present ✅\n", len(x5c))
		for i, cert := range x5c {
			certStr := fmt.Sprintf("%v", cert)
			if len(certStr) > 50 {
				fmt.Printf("    Certificate %d: %s...\n", i+1, certStr[:50])
			} else {
				fmt.Printf("    Certificate %d: %s\n", i+1, certStr)
			}
		}
	} else {
		fmt.Println("  X5C Certificate Chain: Not present (standard auth)")
	}

	// Print claims information
	fmt.Println("\n[Identity Information]")
	fmt.Printf("  App ID (appid): %s\n", claims.AppID)
	fmt.Printf("  App Display Name: %s\n", claims.AppDisplayName)
	fmt.Printf("  Tenant ID (tid): %s\n", claims.TenantID)
	fmt.Printf("  Object ID (oid): %s\n", claims.ObjectID)
	fmt.Printf("  Identity Type (idtyp): %s\n", claims.IdentityType)

	if len(claims.Audience) > 0 {
		fmt.Printf("  Audience (aud): %s\n", strings.Join(claims.Audience, ", "))
	}
	if claims.Issuer != "" {
		fmt.Printf("  Issuer (iss): %s\n", claims.Issuer)
	}
	if claims.ExpiresAt != nil {
		fmt.Printf("  Expires (exp): %d\n", claims.ExpiresAt.Unix())
	}

	if len(claims.Roles) > 0 {
		fmt.Printf("  Roles: %s\n", strings.Join(claims.Roles, ", "))
	}
	if len(claims.DirectoryRoles) > 0 {
		fmt.Printf("  Directory Roles (wids): %s\n", strings.Join(claims.DirectoryRoles, ", "))
	}

	fmt.Println()
	return nil
}
