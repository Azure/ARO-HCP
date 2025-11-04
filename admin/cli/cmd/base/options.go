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

package base

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/admin/cli/pkg/cli/auth"
	"github.com/Azure/ARO-HCP/admin/cli/pkg/cli/request"
)

func DefaultAuthOptions() *RawAuthOptions {
	return &RawAuthOptions{}
}

type RawAuthOptions struct {
	TenantID          string
	ClientID          string
	Scopes            []string
	KeyVaultURI       string
	CertificateSecret string
	Endpoint          string
	Host              string
	Insecure          bool
}

func (o *RawAuthOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.TenantID, "ga-auth-tenant-id", o.TenantID, "Tenant ID to use for the Geneva Action auth certificate")
	cmd.Flags().StringVar(&o.ClientID, "ga-auth-client-id", o.ClientID, "Client ID to use for the Geneva Action auth certificate")
	cmd.Flags().StringSliceVar(&o.Scopes, "ga-auth-scopes", o.Scopes, "Scopes to use for the Geneva Action auth certificate")
	cmd.Flags().StringVar(&o.KeyVaultURI, "ga-auth-cert-kv", o.KeyVaultURI, "Keyvault URL that contains the Geneva Action auth certificate")
	cmd.Flags().StringVar(&o.CertificateSecret, "ga-auth-cert-secret", o.CertificateSecret, "Keyvault secret that contains the Geneva Action auth certificate")
	cmd.Flags().StringVar(&o.Endpoint, "admin-api-endpoint", o.Endpoint, "Admin API endpoint")
	cmd.Flags().StringVar(&o.Host, "host", o.Host, "Host header to set in the request (useful for port-forward scenarios)")
	cmd.Flags().BoolVar(&o.Insecure, "insecure-skip-verify", o.Insecure, "Skip TLS certificate verification")

	// Mark required flags
	for _, flag := range []string{
		"ga-auth-cert-kv",
		"ga-auth-cert-secret",
		"ga-auth-tenant-id",
		"ga-auth-client-id",
		"ga-auth-scopes",
		"admin-api-endpoint",
	} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as required: %w", flag, err)
		}
	}

	return nil
}

func (o *RawAuthOptions) Validate(ctx context.Context) (*ValidatedAuthTokenOptions, error) {
	if o.KeyVaultURI == "" {
		return nil, fmt.Errorf("ga-auth-cert-kv cannot be empty")
	}

	if o.CertificateSecret == "" {
		return nil, fmt.Errorf("ga-auth-cert-secret cannot be empty")
	}

	if o.TenantID == "" {
		return nil, fmt.Errorf("ga-auth-tenant-id cannot be empty")
	}

	if o.ClientID == "" {
		return nil, fmt.Errorf("ga-auth-client-id cannot be empty")
	}

	if len(o.Scopes) == 0 {
		return nil, fmt.Errorf("ga-auth-scopes cannot be empty")
	}

	if o.Endpoint == "" {
		return nil, fmt.Errorf("admin-api-endpoint cannot be empty")
	}

	return &ValidatedAuthTokenOptions{
		validatedAuthOptions: &validatedAuthOptions{
			RawAuthOptions: o,
		},
	}, nil
}

type validatedAuthOptions struct {
	*RawAuthOptions
}

type ValidatedAuthTokenOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedAuthOptions
}

type completedAuthOptions struct {
	Token    string
	Endpoint string
	Host     string
	Insecure bool
}

type AuthOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedAuthOptions
}

func (o *validatedAuthOptions) Complete(ctx context.Context) (*AuthOptions, error) {

	// Login with the certificate
	token, err := auth.GetBearerToken(ctx, auth.CertificateAuthConfig{
		KeyVaultURL:           o.KeyVaultURI,
		CertificateSecretName: o.CertificateSecret,
		TenantID:              o.TenantID,
		ClientID:              o.ClientID,
		Scopes:                o.Scopes,
		SendX5C:               true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get bearer token: %w", err)
	}

	return &AuthOptions{
		completedAuthOptions: &completedAuthOptions{
			Token:    token,
			Endpoint: o.Endpoint,
			Host:     o.Host,
			Insecure: o.Insecure,
		},
	}, nil
}

func (o *AuthOptions) Execute(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("Executing auth test", "endpoint", o.Endpoint)

	// Create request client
	client := request.NewClient(o.Token, o.Host, o.Insecure)

	// Send request to the admin API endpoint
	responseBytes, err := client.SendRequest(ctx, o.Endpoint, "GET", nil)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	logger.Info("Request successful", "response", string(responseBytes))

	return nil
}
