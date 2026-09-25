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

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/certificates"
)

// NewCommand builds the fixed-scope CI certificate backstop, independent of RG cleanup policy.
func NewCommand() *cobra.Command {
	opts := certificates.Options{
		DryRun:       true,
		DeleteActive: true,
		PurgeDeleted: true,
		MinAge:       168 * time.Hour,
		MaxDeletions: 1000,
		MaxPurges:    1000,
		Workers:      2,
	}
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "ci-certificates",
		Short: "Remove old active and deleted CI certificates from the shared dev service vault.",
		Long: `Sweep only aro-hcp-dev-svc-kv.vault.azure.net, guarding both fixed DEV infrastructure subscriptions.
Required guard subscriptions: 1d3378d3-5a3f-4712-85a1-2485495dfc4b and 0ef1ad54-9296-44cd-9600-5dc8e9a74034.
Scope cannot be overridden. Root workflow flags are rejected; place certificate flags after ci-certificates.
Only prow/ci00/ci01 frontend, admin-api, sessiongate and maestro-server job certificates are eligible.
Both latest created and updated timestamps must be older than --min-age; renewals postpone cleanup.
Job suffixes 0000000 through 0000099 and fixture 7654321 are always protected.
Eligible soft-deleted certificates are purgeable tombstones matching the same fixed CI name policy;
their age and RG ownership do not protect a name that is already deleted and unavailable for reuse.
Dry-run lists metadata only. --delete-active and --purge-deleted independently select the enabled actions.
Apply rechecks active and deleted certificate metadata and, when active deletion is enabled, refreshes the owner inventory
before the first delete and at most every 30 seconds. Successful deletes wait up to 30 seconds for their tombstone,
then revalidate and purge it immediately. A bounded worker pool limits concurrent Azure mutations.
--max-purges is a shared bound across immediate purges and previously deleted tombstones;
when both actions are enabled, capacity is reserved first for selected active certificates.
The deletion and purge limits bound metadata inspected as well as mutations.
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
				return fmt.Errorf("ci-certificates rejects parent command flags %s; scope is fixed to %s and both DEV infrastructure guard subscriptions; place certificate flags after ci-certificates", strings.Join(parentFlags, ", "), certificates.VaultURL)
			}
			if err := opts.Validate(); err != nil {
				return err
			}
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be positive")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			// Keep the machine-readable candidate report on stdout, not the root's pretty logger.
			ctx = logr.NewContext(ctx, logr.FromSlogHandler(slog.NewJSONHandler(cmd.OutOrStdout(), nil)))
			cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
			if err != nil {
				return fmt.Errorf("create Azure credential: %w", err)
			}
			return certificates.Run(ctx, cred, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "Report only; set --dry-run=false to soft-delete active certificates and purge eligible tombstones.")
	cmd.Flags().BoolVar(&opts.DeleteActive, "delete-active", opts.DeleteActive, "Discover and soft-delete old, unowned active CI certificates.")
	cmd.Flags().BoolVar(&opts.PurgeDeleted, "purge-deleted", opts.PurgeDeleted, "Discover and permanently purge eligible deleted CI certificate tombstones.")
	cmd.Flags().DurationVar(&opts.MinAge, "min-age", opts.MinAge, "Minimum age of BOTH latest created and updated timestamps (minimum 24h).")
	cmd.Flags().IntVar(&opts.MaxDeletions, "max-deletions", opts.MaxDeletions, "Maximum active certificate metadata entries inspected per run; also bounds selections and mutations (must be positive; also caps dry-run).")
	cmd.Flags().IntVar(&opts.MaxPurges, "max-purges", opts.MaxPurges, "Maximum total purge operations selected per run; immediate purges reserve capacity before deleted-certificate metadata is inspected (must be positive; also caps dry-run).")
	cmd.Flags().IntVar(&opts.Workers, "workers", opts.Workers, "Maximum concurrent certificate delete/purge chains (must be positive).")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Overall timeout, including discovery and all Azure requests (must be positive).")
	cmd.AddCommand(newInventoryCommand())
	return cmd
}

func newInventoryCommand() *cobra.Command {
	opts := certificates.InventoryOptions{IncludePending: true}
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Stream active certificate metadata from the shared dev service vault as CSV.",
		Long: `Read every active certificate metadata page from aro-hcp-dev-svc-kv.vault.azure.net and stream CSV to stdout.
This command does not apply cleanup eligibility rules, inventory resource groups, read certificate contents, or mutate Key Vault.
Progress is written to stderr after each page. Scope and output format cannot be overridden.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := rejectParentFlags(cmd, "ci-certificates inventory"); err != nil {
				return err
			}
			if err := opts.Validate(); err != nil {
				return err
			}
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be positive")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
			if err != nil {
				return fmt.Errorf("create Azure credential: %w", err)
			}
			return certificates.Inventory(ctx, cred, opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&opts.IncludePending, "include-pending", opts.IncludePending, "Include certificates whose creation is still pending.")
	cmd.Flags().IntVar(&opts.MaxItems, "max-items", opts.MaxItems, "Maximum certificate rows to write; zero inventories every page.")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Hour, "Overall inventory timeout (must be positive).")
	return cmd
}

func rejectParentFlags(cmd *cobra.Command, command string) error {
	var parentFlags []string
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		parent.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Changed {
				parentFlags = append(parentFlags, "--"+flag.Name)
			}
		})
	}
	if len(parentFlags) > 0 {
		return fmt.Errorf("%s rejects parent command flags %s; scope is fixed to %s", command, strings.Join(parentFlags, ", "), certificates.VaultURL)
	}
	return nil
}
