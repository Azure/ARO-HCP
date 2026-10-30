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

package acrauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Registry, "registry", opts.Registry, "Fully-qualified registry login server (e.g. myregistry.azurecr.io).")
	cmd.Flags().StringVar(&opts.AuthFile, "auth-file", opts.AuthFile, "Path to the container auth file to update.")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", opts.ClientID, "Client ID of the user-assigned managed identity to authenticate with. Defaults to the ambient credential.")

	if err := cmd.MarkFlagFilename("auth-file"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "auth-file", err)
	}
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	Registry string
	AuthFile string
	ClientID string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before Login can be invoked.
type completedOptions struct {
	Registry   string
	AuthFile   string
	Credential azcore.TokenCredential
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.Registry == "" {
		return nil, errors.New("--registry is required")
	}
	if o.AuthFile == "" {
		return nil, errors.New("--auth-file is required")
	}
	return &ValidatedOptions{validatedOptions: &validatedOptions{RawOptions: o}}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	credential, err := newCredential(o.ClientID)
	if err != nil {
		return nil, err
	}
	return &Options{completedOptions: &completedOptions{
		Registry:   o.Registry,
		AuthFile:   o.AuthFile,
		Credential: credential,
	}}, nil
}

func newCredential(clientID string) (azcore.TokenCredential, error) {
	if clientID == "" {
		credential, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create default Azure credential: %w", err)
		}
		return credential, nil
	}

	credential, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
		ID: azidentity.ClientID(clientID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create managed identity credential for client %q: %w", clientID, err)
	}
	return credential, nil
}

// Login exchanges an Entra token for an ACR refresh token and records it in the auth file.
func (o *Options) Login(ctx context.Context) error {
	token, err := ExchangeForRefreshTokenWithRetry(ctx, o.Credential, o.Registry)
	if err != nil {
		return err
	}
	if err := UpsertCredential(o.AuthFile, o.Registry, NullGUIDUsername, token); err != nil {
		return err
	}
	return nil
}
