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

package certificates

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	certificates "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow/certificates"
	cleanupengine "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

// NewCommand builds the fixed-scope CI certificate backstop, independent of RG cleanup policy.
func NewCommand() *cobra.Command {
	opts := certificates.RunOptions{
		DryRun:       true,
		DeleteActive: true,
		PurgeDeleted: true,
		MaxDeletions: 1000,
		MaxPurges:    1000,
		Workers:      2,
	}
	var timeout time.Duration
	var policyFile string
	cmd := &cobra.Command{
		Use:   "ci-certificates",
		Short: "Remove old active and deleted CI certificates from the shared dev service vault.",
		Long: `Sweep only aro-hcp-dev-svc-kv.vault.azure.net, guarding both fixed DEV infrastructure subscriptions.
Required guard subscriptions: 1d3378d3-5a3f-4712-85a1-2485495dfc4b and 0ef1ad54-9296-44cd-9600-5dc8e9a74034.
Scope cannot be overridden. Root workflow flags are rejected; place certificate flags after ci-certificates.
--policy is required: it configures first-match-wins name rules, retention windows, and owner guards.
Every delete rule must require its owner resource group to be absent or explicitly opt out with allowWithoutOwnerGuard.
Both latest created and updated timestamps must be older than the matching rule retention; renewals postpone cleanup.
Eligible soft-deleted certificates are purgeable tombstones matching the same policy rules and protected-tag safeguard;
neither retention nor the owner guard is reapplied because the certificate is already deleted and the tombstone only blocks reuse of the name.
Dry-run lists metadata only. --delete-active and --purge-deleted independently select the enabled actions.
Apply rechecks active and deleted certificate metadata and, when active deletion is enabled, refreshes the owner inventory
before the first delete and at most every 30 seconds. Successful deletes wait up to 30 seconds for their tombstone,
then revalidate and purge it immediately. A bounded worker pool limits concurrent Azure mutations.
--max-deletions bounds the active certificates deleted (each is purged immediately when --purge-deleted is set);
--max-purges independently bounds the previously deleted tombstones purged.
Inventory paging continues past ineligible metadata until each enabled mutation budget is filled or inventory is exhausted.
Azure has no atomic owner-check/delete:
concurrent RG or certificate creation remains a race. Purging is irreversible and requires certificates/purge permission.
The command never deletes keys or secrets directly.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TraverseChildren can consume root-local flags before selecting this command.
			// Never silently ignore those scope hints or a conflicting root dry-run flag.
			var parentFlags []string
			for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
				parent.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
					if flag.Changed {
						parentFlags = append(parentFlags, "--"+flag.Name)
					}
				})
			}
			if len(parentFlags) > 0 {
				return fmt.Errorf("ci-certificates rejects parent command flags %s; scope is fixed to %s and both DEV infrastructure guard subscriptions; place certificate flags after ci-certificates", strings.Join(parentFlags, ", "), cleanupengine.CertificateVaultURL)
			}
			if err := opts.Validate(); err != nil {
				return err
			}
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be positive")
			}
			if policyFile == "" {
				return fmt.Errorf("--policy is required")
			}
			loadedPolicy, err := policy.LoadCertificates(policyFile)
			if err != nil {
				return err
			}
			opts.Policy = loadedPolicy
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			// Keep the machine-readable candidate report on stdout, not the root's pretty logger.
			ctx = logr.NewContext(ctx, logr.FromSlogHandler(slog.NewJSONHandler(cmd.OutOrStdout(), nil)))
			cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
			if err != nil {
				return fmt.Errorf("create Azure credential: %w", err)
			}
			opts.AzureCredential = cred
			return certificates.Run(ctx, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Report only; set --dry-run=false to soft-delete active certificates and purge eligible tombstones.")
	cmd.Flags().BoolVar(&opts.DeleteActive, "delete-active", opts.DeleteActive, "Discover and soft-delete old, unowned active CI certificates.")
	cmd.Flags().BoolVar(&opts.PurgeDeleted, "purge-deleted", opts.PurgeDeleted, "Discover and permanently purge eligible deleted CI certificate tombstones.")
	cmd.Flags().StringVar(&policyFile, "policy", policyFile, "Certificate cleanup policy file (required): first-match-wins name rules, retention windows, and mandatory owner guards.")
	cmd.Flags().IntVar(&opts.MaxDeletions, "max-deletions", opts.MaxDeletions, "Maximum active certificate mutations selected per run; inventory continues until this budget is filled or exhausted (must be positive; also caps dry-run selections).")
	cmd.Flags().IntVar(&opts.MaxPurges, "max-purges", opts.MaxPurges, "Maximum previously deleted certificates (tombstones) purged per run; the immediate purge of each deleted active certificate is bounded by --max-deletions instead (must be positive; also caps dry-run selections).")
	cmd.Flags().IntVar(&opts.Workers, "workers", opts.Workers, "Maximum concurrent certificate delete/purge chains (must be positive).")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Overall timeout, including discovery and all Azure requests (must be positive).")
	return cmd
}
