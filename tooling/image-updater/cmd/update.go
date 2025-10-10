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
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/options"
)

func NewUpdateCommand() *cobra.Command {
	opts := options.DefaultUpdateOptions()

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update image digests from their source registries",
		Long: `Update reads the configuration file and fetches the latest image digests
from their source registries, then updates the target configuration files
with the new digests.

Use --dry-run to see what changes would be made without actually updating files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, opts)
		},
	}

	if err := options.BindUpdateOptions(opts, cmd); err != nil {
		return nil
	}

	return cmd
}

func runUpdate(cmd *cobra.Command, opts *options.RawUpdateOptions) error {
	ctx := cmd.Context()

	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	return completed.UpdateImages(ctx)
}
