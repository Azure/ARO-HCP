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

package pin

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/pinning"
)

const defaultTimeout = 5 * time.Minute

// RawOptions contains the pin command flags before validation.
type RawOptions struct {
	VaultURL               string
	Mappings               []string
	IndexedApplicationBase string
	IndexedCertificateBase string
	IndexedCount           int
	ReplaceAll             bool
	DryRun                 bool
	Timeout                time.Duration
}

// ValidatedOptions contains normalized pinning inputs.
type ValidatedOptions struct {
	vaultURL   string
	bindings   []pinning.Binding
	replaceAll bool
	dryRun     bool
	timeout    time.Duration
}

// Options contains completed runtime dependencies.
type Options struct {
	*ValidatedOptions
	pinner *pinning.Pinner
}

// DefaultOptions returns the default pin command options.
func DefaultOptions() *RawOptions {
	return &RawOptions{Timeout: defaultTimeout}
}

// BindOptions binds command-line flags to raw options.
func BindOptions(opts *RawOptions, cmd *cobra.Command) {
	cmd.Flags().StringVar(&opts.VaultURL, "vault-url", opts.VaultURL, "Key Vault data-plane URL, for example https://example.vault.azure.net.")
	cmd.Flags().StringArrayVar(&opts.Mappings, "mapping", opts.Mappings, "Application display name and certificate name as app=certificate (repeatable).")
	cmd.Flags().StringVar(&opts.IndexedApplicationBase, "indexed-application-base", opts.IndexedApplicationBase, "Base application display name for an indexed set.")
	cmd.Flags().StringVar(&opts.IndexedCertificateBase, "indexed-certificate-base", opts.IndexedCertificateBase, "Base Key Vault certificate name for an indexed set.")
	cmd.Flags().IntVar(&opts.IndexedCount, "indexed-count", opts.IndexedCount, "Number of indexed app/certificate pairs, named <base>-0 through <base>-N.")
	cmd.Flags().BoolVar(&opts.ReplaceAll, "replace-all", opts.ReplaceAll, "Replace the application's complete keyCredentials collection with the requested certificate.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Resolve and compare credentials without modifying applications.")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout per application, including Graph propagation retries.")
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(o.VaultURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.Path != "" {
		return nil, fmt.Errorf("--vault-url must be an HTTPS Key Vault base URL without a path")
	}
	if !o.ReplaceAll {
		return nil, fmt.Errorf("--replace-all is required; pinning replaces the application's complete keyCredentials collection")
	}
	if o.Timeout <= 0 {
		return nil, fmt.Errorf("--timeout must be greater than zero")
	}
	if o.IndexedCount < 0 {
		return nil, fmt.Errorf("--indexed-count must be >= 0")
	}
	if o.IndexedCount > 0 && (strings.TrimSpace(o.IndexedApplicationBase) == "" || strings.TrimSpace(o.IndexedCertificateBase) == "") {
		return nil, fmt.Errorf("--indexed-application-base and --indexed-certificate-base are required when --indexed-count is greater than zero")
	}

	bindings := make([]pinning.Binding, 0, len(o.Mappings)+o.IndexedCount)
	seen := map[string]struct{}{}
	for _, mapping := range o.Mappings {
		binding, err := parseMapping(mapping)
		if err != nil {
			return nil, err
		}
		if _, found := seen[binding.ApplicationDisplayName]; found {
			return nil, fmt.Errorf("application %q is specified more than once", binding.ApplicationDisplayName)
		}
		seen[binding.ApplicationDisplayName] = struct{}{}
		bindings = append(bindings, binding)
	}
	for i := range o.IndexedCount {
		binding := pinning.Binding{
			ApplicationDisplayName: fmt.Sprintf("%s-%d", strings.TrimSpace(o.IndexedApplicationBase), i),
			CertificateName:        fmt.Sprintf("%s-%d", strings.TrimSpace(o.IndexedCertificateBase), i),
		}
		if _, found := seen[binding.ApplicationDisplayName]; found {
			return nil, fmt.Errorf("application %q is specified more than once", binding.ApplicationDisplayName)
		}
		seen[binding.ApplicationDisplayName] = struct{}{}
		bindings = append(bindings, binding)
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("at least one --mapping or a positive --indexed-count is required")
	}

	return &ValidatedOptions{
		vaultURL:   parsedURL.String(),
		bindings:   bindings,
		replaceAll: o.ReplaceAll,
		dryRun:     o.DryRun,
		timeout:    o.Timeout,
	}, nil
}

func parseMapping(raw string) (pinning.Binding, error) {
	application, certificate, found := strings.Cut(raw, "=")
	application = strings.TrimSpace(application)
	certificate = strings.TrimSpace(certificate)
	if !found || application == "" || certificate == "" {
		return pinning.Binding{}, fmt.Errorf("invalid --mapping %q: expected app=certificate", raw)
	}
	return pinning.Binding{
		ApplicationDisplayName: application,
		CertificateName:        certificate,
	}, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	certificateClient, err := azcertificates.NewClient(o.vaultURL, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create Key Vault certificate client: %w", err)
	}
	graphClient, err := pinning.NewGraphClient(credential)
	if err != nil {
		return nil, err
	}
	return &Options{
		ValidatedOptions: o,
		pinner: pinning.NewPinner(
			pinning.NewCertificateClient(certificateClient),
			graphClient,
		),
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	return o.pinner.Pin(ctx, o.bindings, pinning.Options{
		ReplaceAll: o.replaceAll,
		DryRun:     o.dryRun,
		Timeout:    o.timeout,
	})
}
