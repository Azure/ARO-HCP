// Copyright 2025 Microsoft Corporation
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
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/output"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func NewUpdateCommand() *cobra.Command {
	var (
		configPath     string
		subscriptionID string
		components     []string
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Discover and apply version bumps to config files",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if subscriptionID == "" {
				subscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
			}
			if subscriptionID == "" {
				return fmt.Errorf("--subscription-id or AZURE_SUBSCRIPTION_ID is required")
			}

			cred, err := azidentity.NewDefaultAzureCredential(nil)
			if err != nil {
				return fmt.Errorf("creating Azure credential: %w", err)
			}

			configDir := filepath.Dir(configPath)
			results, err := updater.Update(ctx, cfg, configDir, subscriptionID, cred, components)
			if err != nil {
				return err
			}

			fmt.Print(output.FormatMarkdownTable(results))

			hasUpdates := false
			for _, r := range results {
				if r.Next != "" && r.Error == nil {
					hasUpdates = true
					break
				}
			}
			if hasUpdates {
				fmt.Fprintln(os.Stderr, "Config files updated. Run 'make -C config materialize' to regenerate rendered configs.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "config.yaml", "path to component-updater config file")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Azure subscription ID (or AZURE_SUBSCRIPTION_ID env)")
	cmd.Flags().StringSliceVar(&components, "component", nil, "specific components to update (default: all)")

	return cmd
}
