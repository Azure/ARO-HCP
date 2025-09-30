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

package main

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-Tools/pkg/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/spf13/cobra"
)

func NewDefaultRawProwTokenOptions() *RawProwTokenOptions {
	return &RawProwTokenOptions{}
}

type RawProwTokenOptions struct {
	KeyVaultURI string
	Secret      string
}

func (o *RawProwTokenOptions) BindFlags(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&o.KeyVaultURI, "prow-token-keyvault-uri", o.KeyVaultURI, "Keyvault URI to use for the Prow token")
	cmd.Flags().StringVar(&o.Secret, "prow-token-keyvault-secret", o.Secret, "Keyvault secret to use for the Prow token")

	// Mark required flags
	for _, flag := range []string{
		"prow-token-keyvault-uri",
		"prow-token-keyvault-secret",
	} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as required: %w", flag, err)
		}
	}

	return nil
}

func (o *RawProwTokenOptions) Validate(ctx context.Context) (*ValidatedProwTokenOptions, error) {
	if o.KeyVaultURI == "" {
		return nil, fmt.Errorf("prow-token-keyvault-uri cannot be empty")
	}

	if o.Secret == "" {
		return nil, fmt.Errorf("prow-token-keyvault-secret cannot be empty")
	}

	return &ValidatedProwTokenOptions{
		validatedProwTokenOptions: &validatedProwTokenOptions{
			RawProwTokenOptions: o,
		},
	}, nil
}

type validatedProwTokenOptions struct {
	*RawProwTokenOptions
}

type ValidatedProwTokenOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedProwTokenOptions
}

func (o *validatedProwTokenOptions) Complete(ctx context.Context) (*ProwTokenOptions, error) {
	// Lookup Prow token in Key Vault
	prowToken, err := lookupProwTokenInKeyVault(ctx, o.KeyVaultURI, o.Secret)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup prow token in Key Vault: %w", err)
	}

	return &ProwTokenOptions{
		completedProwTokenOptions: &completedProwTokenOptions{
			ProwToken: prowToken,
		},
	}, nil
}

func lookupProwTokenInKeyVault(ctx context.Context, keyVaultURI string, secretName string) (string, error) {
	// Get Azure credentials using ARO-Tools
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	// Create Key Vault secrets client
	client, err := azsecrets.NewClient(keyVaultURI, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Key Vault client: %w", err)
	}

	// Get the secret from Key Vault
	secret, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %q from Key Vault %q: %w", secretName, keyVaultURI, err)
	}

	if secret.Value == nil {
		return "", fmt.Errorf("secret %q in Key Vault %q has no value", secretName, keyVaultURI)
	}

	return *secret.Value, nil
}

type completedProwTokenOptions struct {
	ProwToken string
}

type ProwTokenOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedProwTokenOptions
}
