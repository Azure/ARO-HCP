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

// Package verify implements the `depctl verify` command.
package verify

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
)

// NewCommand builds the verify command.
func NewCommand(group string) (*cobra.Command, error) {
	options := DefaultOptions()
	command := &cobra.Command{
		Use:     "verify",
		Short:   "Run every required consumer on the baseline and the candidate",
		GroupID: group,
		Args:    cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVerify(cmd.Context(), cmd, options)
		},
	}
	if err := BindOptions(options, command); err != nil {
		return nil, err
	}
	return command, nil
}

// runVerify validates, completes and executes the verification.
func runVerify(ctx context.Context, cmd *cobra.Command, options *RawOptions) error {
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
