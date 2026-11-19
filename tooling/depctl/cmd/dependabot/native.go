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

package dependabot

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
)

// Builds native Dependabot selection, validation and request commands.
func newNativeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "native",
		Short: "Inspect native Dependabot inventory",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	var repository, inventory, group, target string
	selectGroup := &cobra.Command{
		Use:   "select-group",
		Short: "Select one eligible native family and exact target without network access",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repository == "" || inventory == "" || group == "" || target == "" {
				return cli.Invalid(errors.New("--repository, --inventory, --group and --target are required"))
			}
			file, err := os.Open(inventory)
			if err != nil {
				return cli.Invalid(err)
			}
			defer file.Close()
			selection, err := dependabot.SelectNativeInventoryGroup(file, repository, group, target)
			if err != nil {
				return cli.Invalid(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(selection)
		},
	}
	selectGroup.Flags().StringVar(&repository, "repository", "", "OWNER/REPO")
	selectGroup.Flags().StringVar(&inventory, "inventory", "", "native inventory JSON file")
	selectGroup.Flags().StringVar(&group, "group", "", "24-character native family group ID")
	selectGroup.Flags().StringVar(&target, "target", "", "exact inventory target")
	command.AddCommand(selectGroup, newNativePrepareCommand(), newNativeValidateCommand(), newNativeSandboxReadCommand())
	return command
}
