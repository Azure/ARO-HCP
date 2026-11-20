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
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
)

// Builds the complete native candidate validation and guarded request handoff.
func newNativeValidateCommand() *cobra.Command {
	options := &rawNativeCandidateOptions{}
	command := &cobra.Command{
		Use:   "validate-candidate",
		Short: "Validate fixed native consumers and prepare the exact candidate request",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := options.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			return completed.Run(cmd.Context())
		},
	}
	options.BindFlags(command, "validation logs and request artifacts outside the checkout")
	command.Flags().StringVar(&options.compiler, "compiler", "", "absolute path to the trusted pinned workflow compiler")
	return command
}

// Builds the fixed credential-free release read used before sandbox consumers run.
func newNativeSandboxReadCommand() *cobra.Command {
	options := &rawNativeSandboxOptions{}
	return &cobra.Command{
		Use:   "check-sandbox-read",
		Short: "Verify credential-free sandbox reads against an immutable official release",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := options.Validate()
			if err != nil {
				return err
			}
			completed := validated.Complete(cmd.OutOrStdout())
			return completed.Run(cmd.Context())
		},
	}
}
