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
	"strings"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/cmd/internal/common"
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
	vaultURL, err := common.ParseVaultURL(o.VaultURL)
	if err != nil {
		return nil, err
	}
	if o.Timeout <= 0 {
		return nil, fmt.Errorf("--timeout must be greater than zero")
	}
	if len(o.Mappings) == 0 {
		return nil, fmt.Errorf("at least one --mapping is required")
	}

	bindings, err := o.bindings()
	if err != nil {
		return nil, err
	}

	return &ValidatedOptions{
		vaultURL:     vaultURL,
		bindings:     bindings,
		grantConsent: o.GrantConsent,
		dryRun:       o.DryRun,
		timeout:      o.Timeout,
	}, nil
}

// bindings parses the mappings, rejecting applications and secrets that appear
// more than once. Key Vault secret names and Entra display name filters are
// case-insensitive, so uniqueness is checked case-insensitively.
func (o *RawOptions) bindings() ([]secrets.Binding, error) {
	bindings := make([]secrets.Binding, 0, len(o.Mappings))
	applications := sets.New[string]()
	secretNames := sets.New[string]()
	for _, mapping := range o.Mappings {
		binding, err := parseMapping(mapping)
		if err != nil {
			return nil, err
		}
		application := strings.ToLower(binding.ApplicationDisplayName)
		secretName := strings.ToLower(binding.ClientSecretName)
		if applications.Has(application) {
			return nil, fmt.Errorf("application %q is specified more than once", binding.ApplicationDisplayName)
		}
		if secretNames.Has(secretName) {
			return nil, fmt.Errorf("secret %q is specified more than once", binding.ClientSecretName)
		}
		applications.Insert(application)
		secretNames.Insert(secretName)
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func parseMapping(raw string) (secrets.Binding, error) {
	application, secretName, found := strings.Cut(raw, ";")
	binding := secrets.Binding{
		ApplicationDisplayName: strings.TrimSpace(application),
		ClientSecretName:       strings.TrimSpace(secretName),
	}

	switch {
	case !found, strings.Contains(secretName, ";"), binding.ApplicationDisplayName == "", binding.ClientSecretName == "":
		return secrets.Binding{}, fmt.Errorf("invalid --mapping %q: expected application;<prefix>-client-secret", raw)
	case !strings.HasSuffix(binding.ClientSecretName, secrets.ClientSecretSuffix), binding.Prefix() == "":
		return secrets.Binding{}, fmt.Errorf("invalid --mapping %q: secret name must be <prefix>%s", raw, secrets.ClientSecretSuffix)
	}
	return binding, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	credential, err := common.NewCredential()
	if err != nil {
		return nil, err
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
