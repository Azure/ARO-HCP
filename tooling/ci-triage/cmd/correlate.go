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
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/analysis"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/render"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// NewCorrelateCommand creates the correlate cobra command.
func NewCorrelateCommand() *cobra.Command {
	var (
		since      string
		testFilter string
		window     string
	)

	cmd := &cobra.Command{
		Use:   "correlate ENV",
		Short: "Map failure onsets to merged PRs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env := args[0]

			sinceDur, err := parseSinceDuration(since)
			if err != nil {
				return err
			}

			windowDur, err := time.ParseDuration(window)
			if err != nil {
				return fmt.Errorf("invalid --window: %w", err)
			}

			sc := sippy.NewClient()
			data, err := analysis.Correlate(ctx, sc, env, sinceDur, testFilter, windowDur)
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
	cmd.Flags().StringVar(&testFilter, "test", "", "specific test name to correlate")
	cmd.Flags().StringVar(&window, "window", "6h", "time window around onset to search for merges")
	return cmd
}
