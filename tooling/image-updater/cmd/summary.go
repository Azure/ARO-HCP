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

// NewSummaryCommand returns the command that writes the run summary artifacts.
func NewSummaryCommand() (*cobra.Command, error) {
	opts := options.DefaultSummaryOptions()

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Write the HTML and JSON summary of a completed run",
		Long: `Summary reads the update results and the publish receipt of a completed
run and writes a self-contained HTML page and an optional JSON artifact that
show the revision, the templatize source, every component's digest and the
published pull request.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSummary(cmd, opts)
		},
	}

	if err := options.BindSummaryOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

// runSummary executes the summary lifecycle: Validate, Complete, then Run.
func runSummary(cmd *cobra.Command, opts *options.RawSummaryOptions) error {
	validated, err := opts.Validate(cmd.Context())
	if err != nil {
		return err
	}

	completed, err := validated.Complete(cmd.Context())
	if err != nil {
		return err
	}

	return completed.Run()
}
