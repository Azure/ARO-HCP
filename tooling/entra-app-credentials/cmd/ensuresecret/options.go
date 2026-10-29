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

package ensuresecret

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/secrets"
)

const defaultTimeout = 5 * time.Minute

// RawOptions contains the ensure-secret command flags before validation.
type RawOptions struct {
	VaultURL     string
	Mappings     []string
	GrantConsent bool
	DryRun       bool
	Timeout      time.Duration
}

// ValidatedOptions contains normalized inputs.
type ValidatedOptions struct {
	vaultURL     string
	bindings     []secrets.Binding
	grantConsent bool
	dryRun       bool
	timeout      time.Duration
}

// Options contains completed runtime dependencies.
type Options struct {
	*ValidatedOptions
	ensurer *secrets.Ensurer
}

// DefaultOptions returns the default ensure-secret command options.
func DefaultOptions() *RawOptions {
	return &RawOptions{Timeout: defaultTimeout}
}

// BindOptions binds command-line flags to raw options.
func BindOptions(opts *RawOptions, cmd *cobra.Command) {
	cmd.Flags().StringVar(&opts.VaultURL, "vault-url", opts.VaultURL, "Key Vault data-plane URL, for example https://example.vault.azure.net.")
	cmd.Flags().StringArrayVar(&opts.Mappings, "mapping", opts.Mappings, "Application and client secret name as quoted 'application;<prefix>-client-secret' (repeatable). The client ID and tenant ID are stored as <prefix>-client-id and <prefix>-tenant-id.")
	cmd.Flags().BoolVar(&opts.GrantConsent, "grant-consent", opts.GrantConsent, "Grant admin consent for each application's declared API permissions.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Report what would change without modifying Entra or Key Vault.")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout per application, including Graph propagation retries.")
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(o.VaultURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.Path != "" {
		return nil, fmt.Errorf("--vault-url must be an HTTPS Key Vault base URL without a path")
	}
	if o.Timeout <= 0 {
		return nil, fmt.Errorf("--timeout must be greater than zero")
	}
	if len(o.Mappings) == 0 {
		return nil, fmt.Errorf("at least one --mapping is required")
	}

	bindings := make([]secrets.Binding, 0, len(o.Mappings))
	seenApplications := map[string]struct{}{}
	seenSecrets := map[string]struct{}{}
	for _, mapping := range o.Mappings {
		binding, err := parseMapping(mapping)
		if err != nil {
			return nil, err
		}
		if _, found := seenApplications[binding.ApplicationDisplayName]; found {
			return nil, fmt.Errorf("application %q is specified more than once", binding.ApplicationDisplayName)
		}
		if _, found := seenSecrets[binding.ClientSecretName]; found {
			return nil, fmt.Errorf("secret %q is specified more than once", binding.ClientSecretName)
		}
		seenApplications[binding.ApplicationDisplayName] = struct{}{}
		seenSecrets[binding.ClientSecretName] = struct{}{}
		bindings = append(bindings, binding)
	}

	return &ValidatedOptions{
		vaultURL:     parsedURL.String(),
		bindings:     bindings,
		grantConsent: o.GrantConsent,
		dryRun:       o.DryRun,
		timeout:      o.Timeout,
	}, nil
}

func parseMapping(raw string) (secrets.Binding, error) {
	parts := strings.Split(raw, ";")
	if len(parts) != 2 {
		return secrets.Binding{}, fmt.Errorf("invalid --mapping %q: expected application;<prefix>-client-secret", raw)
	}
	binding := secrets.Binding{
		ApplicationDisplayName: strings.TrimSpace(parts[0]),
		ClientSecretName:       strings.TrimSpace(parts[1]),
	}
	if binding.ApplicationDisplayName == "" || binding.ClientSecretName == "" {
		return secrets.Binding{}, fmt.Errorf("invalid --mapping %q: expected application;<prefix>-client-secret", raw)
	}
	if !strings.HasSuffix(binding.ClientSecretName, secrets.ClientSecretSuffix) || binding.Prefix() == "" {
		return secrets.Binding{}, fmt.Errorf("invalid --mapping %q: secret name must be <prefix>%s", raw, secrets.ClientSecretSuffix)
	}
	return binding, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	secretClient, err := azsecrets.NewClient(o.vaultURL, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create Key Vault secret client: %w", err)
	}
	graphClient, err := secrets.NewGraphClient(credential)
	if err != nil {
		return nil, err
	}
	return &Options{
		ValidatedOptions: o,
		ensurer:          secrets.NewEnsurer(graphClient, secrets.NewKeyVaultStore(secretClient)),
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	return o.ensurer.Ensure(ctx, o.bindings, secrets.Options{
		GrantConsent: o.grantConsent,
		DryRun:       o.dryRun,
		Timeout:      o.timeout,
	})
}
