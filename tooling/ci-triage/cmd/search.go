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

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/cisearch"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/render"
)

// NewSearchCommand creates the search cobra command.
func NewSearchCommand() *cobra.Command {
	var (
		maxAge     string
		resultType string
		crossCI    bool
	)

	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search for failure patterns across CI jobs",
		Long: `Search for a failure string or regex across OpenShift CI jobs.
By default, searches only ARO-HCP jobs. Use --cross-ci to compare
ARO results against all of OpenShift CI to determine if a failure
is ARO-specific or platform-wide.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			query := args[0]

			client := cisearch.NewClient()

			if crossCI {
				scope, err := client.IsAROSpecific(ctx, query)
				if err != nil {
					return err
				}
				out, err := render.JSON(scope)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), out)
				return nil
			}

			results, err := client.Search(ctx, query, cisearch.AROJobFilter, maxAge, resultType)
			if err != nil {
				return err
			}
			out, err := render.JSON(results)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}

	cmd.Flags().StringVar(&maxAge, "max-age", "48h", "lookback window (Go duration: 24h, 48h, 168h)")
	cmd.Flags().StringVar(&resultType, "type", "junit", "result type: junit, build-log, all")
	cmd.Flags().BoolVar(&crossCI, "cross-ci", false, "compare ARO vs all OpenShift CI to check if failure is ARO-specific")
	return cmd
}
