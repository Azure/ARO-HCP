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

package prowjobexecutor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/prow-job-executor/internal/retry"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// errPermanentKeyVaultLookup marks a prow-token lookup failure as a deterministic,
// local misconfiguration (e.g. an unparseable Key Vault URI or a secret with no
// value). Errors wrapping it are classified non-retryable so they fail fast instead
// of consuming the full ~31-minute backoff budget reserved for transient failures.
var errPermanentKeyVaultLookup = errors.New("permanent Key Vault lookup error")

// prowTokenLookupBackoff controls retries of the Key Vault prow-token lookup.
//
// The lookup can fail with transient errors that are unrelated to configuration:
// the managed-identity token acquisition reads the instance metadata endpoint
// (169.254.169.254), which intermittently returns connection resets / EOFs on EV2
// runners, and Key Vault itself can return 429/5xx. Without retries such a momentary
// blip fails the whole EV2 gating step and forces the entire deployment job to be
// restarted from scratch.
//
// Delays between attempts grow 30s, 1, 2, 4, 8, 16 minutes over up to 7 attempts,
// for a worst-case cumulative wait of ~31.5 minutes before giving up. The parent
// context still bounds the total runtime.
//
// Note: apimachinery's Backoff.Cap not only clamps an individual delay but also
// stops all further retries once the cap is reached, so the schedule is deliberately
// bounded by Steps rather than Cap.
var prowTokenLookupBackoff = wait.Backoff{
	Duration: 30 * time.Second, // Initial delay
	Factor:   2.0,              // Exponential factor
	Jitter:   0.1,              // 10% jitter to de-sync concurrent runners
	Steps:    7,                // Maximum attempts (~31.5m worst-case cumulative wait)
}

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

// lookupProwTokenInKeyVault fetches the prow token from Key Vault, retrying on
// transient failures (managed-identity/IMDS or network blips and Key Vault 429/5xx)
// with exponential backoff. Permanent failures (Key Vault 4xx other than 429, e.g.
// 401/403/404) fail fast.
func lookupProwTokenInKeyVault(ctx context.Context, keyVaultURI string, secretName string) (string, error) {
	return retry.WithValue(ctx, prowTokenLookupBackoff, isRetryableKeyVaultError, func(ctx context.Context) (string, error) {
		return lookupProwTokenInKeyVaultOnce(ctx, keyVaultURI, secretName)
	})
}

// isRetryableKeyVaultError reports whether a prow-token lookup error is transient
// and worth retrying. A cancelled or expired context is never retryable, nor is a
// deterministic local failure marked with errPermanentKeyVaultLookup. Key Vault
// HTTP responses are retried only on 429 and 5xx; other 4xx (401/403/404/400/409)
// are permanent and fail fast. Any remaining error without an HTTP response status
// is a credential-acquisition or transport failure (e.g. an IMDS connection
// reset/EOF while minting the managed-identity token) and is treated as transient.
func isRetryableKeyVaultError(err error) bool {
	// A cancelled/expired parent context must fail fast, never retry.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Deterministic local misconfiguration (bad Key Vault URI, empty secret) is
	// permanent; fail fast instead of backing off for ~31 minutes.
	if errors.Is(err, errPermanentKeyVaultLookup) {
		return false
	}

	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		code := respErr.StatusCode
		return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
	}

	// No HTTP response status: credential acquisition (IMDS/managed identity) or
	// transport failure. These are transient on EV2 runners; retry.
	return true
}

// lookupProwTokenInKeyVaultOnce performs a single Key Vault secret lookup without
// retry logic.
func lookupProwTokenInKeyVaultOnce(ctx context.Context, keyVaultURI string, secretName string) (string, error) {
	// Get Azure credentials using ARO-Tools
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	// Create Key Vault secrets client
	client, err := azsecrets.NewClient(keyVaultURI, cred, nil)
	if err != nil {
		// Bad/unparseable Key Vault URI is a deterministic misconfiguration; fail fast.
		return "", fmt.Errorf("failed to create Key Vault client: %w: %w", err, errPermanentKeyVaultLookup)
	}

	// Get the secret from Key Vault
	secret, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %q from Key Vault %q: %w", secretName, keyVaultURI, err)
	}

	if secret.Value == nil {
		// A present-but-empty secret is a deterministic misconfiguration; fail fast.
		return "", fmt.Errorf("secret %q in Key Vault %q has no value: %w", secretName, keyVaultURI, errPermanentKeyVaultLookup)
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
