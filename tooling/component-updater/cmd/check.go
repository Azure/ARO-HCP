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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
		configPath      string
		subscriptionID  string
		components      []string
		timeout         time.Duration
		materialize     = true
		skipMaterialize bool
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

			cred, err := azidentity.NewDefaultAzureCredential(nil)
			if err != nil {
				return fmt.Errorf("creating Azure credential: %w", err)
			}
			if strings.TrimSpace(subscriptionID) == "" {
				subscriptionID, err = providers.ResolveSubscriptionID(ctx, cred)
				if err != nil {
					return err
				}
			}
			factory := func(providerName string) (providers.VersionProvider, error) {
				return providers.New(providerName, subscriptionID, cred)
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
			if runErr != nil {
				return runErr
			}
			if skipMaterialize {
				materialize = false
			}
			return materializeUpdates(cmd.Context(), cfg, configDir, results, materialize, func(ctx context.Context, directory string) error {
				executable, err := exec.LookPath("make")
				if err != nil {
					executable, err = exec.LookPath("gmake")
				}
				if err != nil {
					return fmt.Errorf("make or gmake is required: %w", err)
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
			}, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "config.yaml", "path to component-updater config file")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Azure subscription ID (or AZURE_SUBSCRIPTION_ID; if omitted, uses the sole Enabled subscription visible to the credential)")
	cmd.Flags().StringSliceVar(&components, "component", nil, "specific components to check (default: all)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "overall discovery/update deadline, including retries")
	if action == "update" {
		cmd.Flags().BoolVar(&materialize, "materialize", true, "run make materialize after a successful source edit (default true)")
		cmd.Flags().BoolVar(&skipMaterialize, "skip-materialize", false, "edit source configuration only; do not run make materialize")
	}

	return cmd
}
