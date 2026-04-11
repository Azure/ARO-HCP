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

// NewDiagnoseCommand creates the diagnose cobra command.
func NewDiagnoseCommand() *cobra.Command {
	var (
		since    string
		testName string
	)

	cmd := &cobra.Command{
		Use:   "diagnose ENV",
		Short: "Full synthesis: fleet health + investigation + correlation → verdict",
		Long: `Diagnose runs the complete triage chain for an environment:
1. Fleet health scan across all environments
2. Deep investigation of the target test (or top failure)
3. Onset-to-deployment and PR correlation
4. Cross-CI scope check
5. Synthesized verdict with confidence and evidence chain`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env := args[0]

			sinceDur, err := parseSinceDuration(since)
			if err != nil {
				return err
			}

			sc := sippy.NewClient()
			data, err := analysis.Diagnose(ctx, sc, env, testName, sinceDur)
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
	cmd.Flags().StringVar(&testName, "test", "", "specific test name (default: top failure)")
	return cmd
}
