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

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/output"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func NewCheckCommand() *cobra.Command {
	return newCommand("check", "Report compatible component versions without changing files", updater.Check)
}

type runFunc func(context.Context, *config.Config, string, updater.ProviderFactory, []string) ([]updater.ComponentResult, error)

func newCommand(action, short string, run runFunc) *cobra.Command {
	var (
		configPath      string
		subscriptionID  string
		components      []string
		timeout         time.Duration
		skipMaterialize bool
	)

	cmd := &cobra.Command{
		Use:   action,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be positive")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if err := updater.ValidateComponents(cfg, components); err != nil {
				return err
			}

			if strings.TrimSpace(subscriptionID) == "" {
				subscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
			}

			// Azure is set up lazily, on first use by a provider, so paused-only
			// updates and non-Azure providers never contact it.
			// RequireAzureTokenCredentials makes an unset AZURE_TOKEN_CREDENTIALS fail.
			deps := providers.Deps{
				Azure: sync.OnceValues(func() (providers.AzureAccess, error) {
					cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
					if err != nil {
						return providers.AzureAccess{}, fmt.Errorf("creating Azure credential (set AZURE_TOKEN_CREDENTIALS, for example dev): %w", err)
					}
					id := strings.TrimSpace(subscriptionID)
					if id == "" {
						if id, err = providers.ResolveSubscriptionID(ctx, cred); err != nil {
							return providers.AzureAccess{}, err
						}
					}
					return providers.AzureAccess{SubscriptionID: id, Credential: cred}, nil
				}),
			}
			factory := func(providerName string) (providers.VersionProvider, error) {
				return providers.New(providerName, deps)
			}

			// Resolve GNU Make before discovery so an unsuitable make fails before
			// any source file is edited.
			var makeExecutable string
			if action == "update" && !skipMaterialize {
				if makeExecutable, err = findGNUMake(ctx, exec.LookPath, makeVersion); err != nil {
					return fmt.Errorf("%w (or pass --skip-materialize)", err)
				}
			}

			configDir := filepath.Dir(configPath)
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Discovering available versions and validating update eligibility (timeout: %s)...\n\n", timeout); err != nil {
				return err
			}
			results, runErr := run(ctx, cfg, configDir, factory, components)
			if len(results) > 0 {
				if _, err := fmt.Fprint(cmd.OutOrStdout(), output.FormatMarkdownTable(results)); err != nil {
					return err
				}
			}
			if action != "update" {
				return runErr
			}
			if runErr != nil {
				// Another component may have failed after this one was written;
				// still tell the user how to regenerate for the persisted edits.
				if err := materializeUpdates(ctx, cfg, configDir, results, false, nil, cmd.OutOrStdout()); err != nil {
					return errors.Join(runErr, err)
				}
				return runErr
			}
			// Not bounded by --timeout: killing make midway would leave the source
			// edited but rendered output stale. SIGINT/SIGTERM still cancel it.
			return materializeUpdates(cmd.Context(), cfg, configDir, results, !skipMaterialize, func(ctx context.Context, directory string) error {
				process := exec.CommandContext(ctx, makeExecutable, "materialize")
				process.Dir = directory
				// Keep make quiet on success so Slack/CI digests stay readable; dump
				// combined output only when materialize fails.
				var buf bytes.Buffer
				process.Stdout = &buf
				process.Stderr = &buf
				if err := process.Run(); err != nil {
					out := strings.TrimSpace(buf.String())
					if out == "" {
						return err
					}
					return fmt.Errorf("%w\n%s", err, out)
				}
				return nil
			}, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "config.yaml", "path to component-updater config file")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Azure subscription ID (or AZURE_SUBSCRIPTION_ID; if omitted, uses the sole Enabled subscription visible to the credential)")
	cmd.Flags().StringSliceVar(&components, "component", nil, "specific components to check (default: all)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "deadline for discovery and the source edit, including retries (make materialize is not bounded by it)")
	if action == "update" {
		cmd.Flags().BoolVar(&skipMaterialize, "skip-materialize", false, "edit source configuration only; do not run make materialize")
	}

	return cmd
}
