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
	IndexedCertificateDNS  string
	IndexedCount           int
	CreateMissing          bool
	Rotate                 bool
	DryRun                 bool
	Timeout                time.Duration
}

// ValidatedOptions contains normalized pinning inputs.
type ValidatedOptions struct {
	vaultURL      string
	bindings      []pinning.Binding
	createMissing bool
	rotate        bool
	dryRun        bool
	timeout       time.Duration
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
	cmd.Flags().StringArrayVar(&opts.Mappings, "mapping", opts.Mappings, "Application, certificate, and optional DNS name as quoted 'application;certificate[;dns]' (repeatable; DNS required for certificate creation).")
	cmd.Flags().StringVar(&opts.IndexedApplicationBase, "indexed-application-base", opts.IndexedApplicationBase, "Base application display name for an indexed set.")
	cmd.Flags().StringVar(&opts.IndexedCertificateBase, "indexed-certificate-base", opts.IndexedCertificateBase, "Base Key Vault certificate name for an indexed set.")
	cmd.Flags().StringVar(&opts.IndexedCertificateDNS, "indexed-certificate-dns-suffix", opts.IndexedCertificateDNS, "DNS suffix for indexed certificates, producing <index>.<suffix>.")
	cmd.Flags().IntVar(&opts.IndexedCount, "indexed-count", opts.IndexedCount, "Number of indexed app/certificate pairs, named <base>-0 through <base>-N.")
	cmd.Flags().BoolVar(&opts.CreateMissing, "create-missing", opts.CreateMissing, "Create missing self-signed Key Vault certificates before pinning.")
	cmd.Flags().BoolVar(&opts.Rotate, "rotate", opts.Rotate, "Disruptively replace the current Key Vault certificates before pinning the new credentials.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Resolve and compare credentials without modifying applications.")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "Timeout per application, including certificate creation and Graph propagation retries.")
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(o.VaultURL))
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.Path != "" {
		return nil, fmt.Errorf("--vault-url must be an HTTPS Key Vault base URL without a path")
	}
	if o.Timeout <= 0 {
		return nil, fmt.Errorf("--timeout must be greater than zero")
	}
	if o.DryRun && (o.CreateMissing || o.Rotate) {
		return nil, fmt.Errorf("--dry-run cannot be combined with certificate creation or rotation")
	}
	if o.CreateMissing && o.Rotate {
		return nil, fmt.Errorf("--create-missing and --rotate are mutually exclusive")
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
		if strings.TrimSpace(o.IndexedCertificateDNS) != "" {
			binding.CertificateDNSName = fmt.Sprintf("%d.%s", i, strings.TrimSpace(o.IndexedCertificateDNS))
			if len(binding.CertificateDNSName) > 64 {
				return nil, fmt.Errorf("certificate DNS name %q exceeds the 64-character common-name limit", binding.CertificateDNSName)
			}
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
	if o.CreateMissing || o.Rotate {
		seenCertificates := map[string]struct{}{}
		for _, binding := range bindings {
			if binding.CertificateDNSName == "" {
				return nil, fmt.Errorf("certificate %q requires a DNS name when creating or rotating certificates", binding.CertificateName)
			}
			if o.Rotate {
				if _, found := seenCertificates[binding.CertificateName]; found {
					return nil, fmt.Errorf("certificate %q cannot be rotated for more than one application in the same invocation", binding.CertificateName)
				}
				seenCertificates[binding.CertificateName] = struct{}{}
			}
		}
	}

	return &ValidatedOptions{
		vaultURL:      parsedURL.String(),
		bindings:      bindings,
		createMissing: o.CreateMissing,
		rotate:        o.Rotate,
		dryRun:        o.DryRun,
		timeout:       o.Timeout,
	}, nil
}

func parseMapping(raw string) (pinning.Binding, error) {
	parts := strings.Split(raw, ";")
	if len(parts) < 2 || len(parts) > 3 {
		return pinning.Binding{}, fmt.Errorf("invalid --mapping %q: expected application;certificate[;dns]", raw)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return pinning.Binding{}, fmt.Errorf("invalid --mapping %q: expected application;certificate[;dns]", raw)
		}
	}
	binding := pinning.Binding{
		ApplicationDisplayName: parts[0],
		CertificateName:        parts[1],
	}
	if len(parts) == 3 {
		if len(parts[2]) > 64 {
			return pinning.Binding{}, fmt.Errorf("certificate DNS name %q exceeds the 64-character common-name limit", parts[2])
		}
		binding.CertificateDNSName = parts[2]
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
		CreateMissing: o.createMissing,
		Rotate:        o.rotate,
		DryRun:        o.dryRun,
		Timeout:       o.timeout,
	})
}
