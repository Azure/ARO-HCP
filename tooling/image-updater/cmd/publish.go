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
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/options"
)

// NewPublishCommand builds the command that publishes the digest update.
func NewPublishCommand() *cobra.Command {
	raw := options.DefaultPublishOptions()

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Commit the digest update and publish its pull request",
		Long: `Publish commits the pending digest update, pushes the rolling branch and
creates or updates one owned pull request through the shared GitHub library.

The branch is reused across runs, so a later run folds into the pull request
already under review instead of opening a second one. The push pins the branch
tip read before publication, and the library verifies the head, the base, the
metadata and the complete file set before reporting a receipt.

With --dry-run the commit is prepared and nothing is pushed or published.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPublish(cmd, raw)
		},
	}

	if err := options.BindPublishOptions(raw, cmd); err != nil {
		cmd.RunE = func(*cobra.Command, []string) error { return err }
	}

	return cmd
}

// runPublish runs the publication phases in order.
func runPublish(cmd *cobra.Command, raw *options.RawPublishOptions) error {
	validated, err := raw.Validate()
	if err != nil {
		return err
	}

	completed, err := validated.Complete(cmd.Context())
	if err != nil {
		return err
	}

	return completed.Run(cmd.Context())
}
