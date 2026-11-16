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

// Package dependabot implements the `depctl dependabot` commands.
package dependabot

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
)

// NewCommand builds the dependabot command with its subcommands.
func NewCommand(group string) (*cobra.Command, error) {
	command := &cobra.Command{
		Use:     "dependabot",
		Short:   "Inspect owned security remediation proposals",
		GroupID: group,
		Args:    cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	inventory, err := newInventoryCommand()
	if err != nil {
		return nil, err
	}
	command.AddCommand(inventory)
	return command, nil
}

// newInventoryCommand builds the read-only security proposal inventory.
func newInventoryCommand() (*cobra.Command, error) {
	options := DefaultInventoryOptions()
	command := &cobra.Command{
		Use:   "inventory",
		Short: "List open security proposals owned by the robot identity",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInventory(cmd.Context(), cmd, options)
		},
	}
	if err := BindInventoryOptions(options, command); err != nil {
		return nil, err
	}
	return command, nil
}

// runInventory validates, completes and executes the inventory.
func runInventory(ctx context.Context, cmd *cobra.Command, options *InventoryOptions) error {
	options.out, options.err = cmd.OutOrStdout(), cmd.ErrOrStderr()
	validated, err := options.Validate(ctx)
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}
