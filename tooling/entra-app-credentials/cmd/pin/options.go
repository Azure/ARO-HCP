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
	"strings"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/cmd/internal/common"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/pinning"
)

const (
	defaultTimeout = 5 * time.Minute

	// maxCommonNameLength is the X.509 common-name limit for certificate DNS names.
	maxCommonNameLength = 64
)

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
	vaultURL, err := common.ParseVaultURL(o.VaultURL)
	if err != nil {
		return nil, err
	}
	err = o.validateFlags()
	if err != nil {
		return nil, err
	}

	bindings, err := o.bindings()
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("at least one --mapping or a positive --indexed-count is required")
	}
	if o.CreateMissing || o.Rotate {
		err = o.validateLifecycleBindings(bindings)
		if err != nil {
			return nil, err
		}
	}

	return &ValidatedOptions{
		vaultURL:      vaultURL,
		bindings:      bindings,
		createMissing: o.CreateMissing,
		rotate:        o.Rotate,
		dryRun:        o.DryRun,
		timeout:       o.Timeout,
	}, nil
}

func (o *RawOptions) validateFlags() error {
	switch {
	case o.Timeout <= 0:
		return fmt.Errorf("--timeout must be greater than zero")
	case o.DryRun && (o.CreateMissing || o.Rotate):
		return fmt.Errorf("--dry-run cannot be combined with certificate creation or rotation")
	case o.CreateMissing && o.Rotate:
		return fmt.Errorf("--create-missing and --rotate are mutually exclusive")
	case o.IndexedCount < 0:
		return fmt.Errorf("--indexed-count must be >= 0")
	case o.IndexedCount > 0 && (strings.TrimSpace(o.IndexedApplicationBase) == "" || strings.TrimSpace(o.IndexedCertificateBase) == ""):
		return fmt.Errorf("--indexed-application-base and --indexed-certificate-base are required when --indexed-count is greater than zero")
	}
	return nil
}

// bindings returns the explicit mappings followed by the indexed set, rejecting
// applications that appear more than once.
func (o *RawOptions) bindings() ([]pinning.Binding, error) {
	bindings := make([]pinning.Binding, 0, len(o.Mappings)+o.IndexedCount)
	for _, mapping := range o.Mappings {
		binding, err := parseMapping(mapping)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}

	for i := range o.IndexedCount {
		binding := pinning.Binding{
			ApplicationDisplayName: fmt.Sprintf("%s-%d", strings.TrimSpace(o.IndexedApplicationBase), i),
			CertificateName:        fmt.Sprintf("%s-%d", strings.TrimSpace(o.IndexedCertificateBase), i),
		}
		dnsSuffix := strings.TrimSpace(o.IndexedCertificateDNS)
		if dnsSuffix != "" {
			binding.CertificateDNSName = fmt.Sprintf("%d.%s", i, dnsSuffix)
		}
		if len(binding.CertificateDNSName) > maxCommonNameLength {
			return nil, fmt.Errorf("certificate DNS name %q exceeds the %d-character common-name limit", binding.CertificateDNSName, maxCommonNameLength)
		}
		bindings = append(bindings, binding)
	}

	seen := sets.New[string]()
	for _, binding := range bindings {
		if seen.Has(binding.ApplicationDisplayName) {
			return nil, fmt.Errorf("application %q is specified more than once", binding.ApplicationDisplayName)
		}
		seen.Insert(binding.ApplicationDisplayName)
	}
	return bindings, nil
}

// validateLifecycleBindings checks the bindings can have certificates created
// or rotated: each needs a DNS name, and a rotated certificate must belong to
// a single application.
func (o *RawOptions) validateLifecycleBindings(bindings []pinning.Binding) error {
	rotated := sets.New[string]()
	for _, binding := range bindings {
		if binding.CertificateDNSName == "" {
			return fmt.Errorf("certificate %q requires a DNS name when creating or rotating certificates", binding.CertificateName)
		}
		if !o.Rotate {
			continue
		}
		if rotated.Has(binding.CertificateName) {
			return fmt.Errorf("certificate %q cannot be rotated for more than one application in the same invocation", binding.CertificateName)
		}
		rotated.Insert(binding.CertificateName)
	}
	return nil
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
		if len(parts[2]) > maxCommonNameLength {
			return pinning.Binding{}, fmt.Errorf("certificate DNS name %q exceeds the %d-character common-name limit", parts[2], maxCommonNameLength)
		}
		binding.CertificateDNSName = parts[2]
	}
	return binding, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	credential, err := common.NewCredential()
	if err != nil {
		return nil, err
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
		pinner:           pinning.NewPinner(pinning.NewCertificateClient(certificateClient), graphClient),
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
