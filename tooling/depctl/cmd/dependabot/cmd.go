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
	"errors"

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
	command.AddCommand(newGuardCommand())
	command.AddCommand(newValidateCommand())
	command.AddCommand(newRetestCommand())
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

// newGuardCommand builds the guarded publication step.
func newGuardCommand() *cobra.Command {
	options := guardOptions{}
	command := &cobra.Command{
		Use:   "guard",
		Short: "Validate proposals and defer open dependency overlaps",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return guardCreate(cmd.Context(), options)
		},
	}
	command.Flags().StringVar(&options.Repository, "repository", "", "OWNER/REPO")
	command.Flags().StringVar(&options.AgentOutput, "agent-output", "", "agent safe-output JSON file")
	return command
}

// newValidateCommand builds the local proposal preflight.
func newValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate PROPOSAL",
		Short: "Validate one dependency proposal and print its canonical form",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.Invalid(errors.New("validate requires exactly one proposal path"))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return validateProposal(args[0])
		},
	}
}

// newRetestCommand builds the guarded retest step.
func newRetestCommand() *cobra.Command {
	options := retestOptions{}
	command := &cobra.Command{
		Use:   "retest",
		Short: "Verify a transient-failure retest request",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return guardRetest(cmd.Context(), options)
		},
	}
	command.Flags().StringVar(&options.Repository, "repository", "", "OWNER/REPO")
	command.Flags().IntVar(&options.PullRequest, "pull-request", 0, "pull request number")
	command.Flags().StringVar(&options.Head, "head", "", "expected head SHA")
	command.Flags().StringVar(&options.Evidence, "evidence", "", "failure and comparison URLs")
	return command
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
