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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/output"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func NewCheckCommand() *cobra.Command {
	return newCommand("check", updater.Check)
}

type runFunc func(context.Context, *config.Config, string, updater.ProviderFactory, []string) ([]updater.ComponentResult, error)

func newCommand(action string, run runFunc) *cobra.Command {
	var (
		configPath     string
		subscriptionID string
		components     []string
		timeout        time.Duration
	)

	cmd := &cobra.Command{
		Use:   action,
		Short: "Discover compatible component versions; update writes local configuration",
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

			var (
				azureOnce    sync.Once
				azureCred    azcore.TokenCredential
				azureSub     string
				azureInitErr error
			)
			ensureAzure := func() error {
				azureOnce.Do(func() {
					azureCred, azureInitErr = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
						RequireAzureTokenCredentials: true,
					})
					if azureInitErr != nil {
						azureInitErr = fmt.Errorf("creating Azure credential: %w", azureInitErr)
						return
					}
					azureSub = strings.TrimSpace(subscriptionID)
					if azureSub == "" {
						azureSub, azureInitErr = providers.ResolveSubscriptionID(ctx, azureCred)
					}
				})
				return azureInitErr
			}
			factory := func(providerName string) (providers.VersionProvider, error) {
				if err := ensureAzure(); err != nil {
					return nil, err
				}
				return providers.New(providerName, azureSub, azureCred)
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
			// check is read-only: it never writes, so it must not report
			// materialization status at all.
			if action == "update" && (runErr == nil || hasSuccessfulUpdate(results)) {
				if err := materializeUpdates(cmd.Context(), cfg, configDir, results, func(ctx context.Context, directory string) error {
					executable, err := lookPathMake()
					if err != nil {
						return err
					}
					process := exec.CommandContext(ctx, executable, "materialize")
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
				}, cmd.OutOrStdout()); err != nil {
					return errors.Join(runErr, err)
				}
			}
			return runErr
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "config.yaml", "path to component-updater config file")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Azure subscription ID (or AZURE_SUBSCRIPTION_ID; if omitted, uses the sole Enabled subscription visible to the credential)")
	cmd.Flags().StringSliceVar(&components, "component", nil, "specific components to check (default: all)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "overall discovery/update deadline, including retries")
	return cmd
}

func hasSuccessfulUpdate(results []updater.ComponentResult) bool {
	for _, result := range results {
		if result.Updated && result.Error == nil {
			return true
		}
	}
	return false
}

// Prefer GNU Make (gmake on macOS Homebrew) when both are present.
func lookPathMake() (string, error) {
	if path, err := exec.LookPath("gmake"); err == nil {
		return path, nil
	}
	path, err := exec.LookPath("make")
	if err != nil {
		return "", fmt.Errorf("make or gmake is required: %w", err)
	}
	return path, nil
}
