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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/analysis"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/render"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// NewInvestigateCommand creates the investigate cobra command.
func NewInvestigateCommand() *cobra.Command {
	var (
		since    string
		testName string
	)

	cmd := &cobra.Command{
		Use:   "investigate ENV",
		Short: "Deep investigation of a test failure with GCS artifacts and cross-CI search",
		Long: `Investigate a specific test failure in depth. Chains Sippy failure data,
GCS artifact analysis (step timing, Azure API logs), and cross-CI search
to build a complete evidence package.

If --test is not specified, investigates the top failing test.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env := args[0]

			sinceDur, err := parseSinceDuration(since)
			if err != nil {
				return err
			}

			sc := sippy.NewClient()
			data, err := analysis.Investigate(ctx, sc, env, testName, sinceDur)
			if err != nil {
				return err
			}

			out, err := render.JSON(data)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "14d", "lookback window (7d, 24h, 2w)")
	cmd.Flags().StringVar(&testName, "test", "", "specific test name to investigate (default: top failure)")
	return cmd
}
