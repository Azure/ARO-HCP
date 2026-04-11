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
)

// NewTestDetailCommand creates the test-detail cobra command.
func NewTestDetailCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test-detail URL ENV TEST_NAME",
		Short: "Deep dive into one test: full error, output, and Azure API logs",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			jobURL := args[0]
			env := args[1]
			testName := args[2]

			data, err := analysis.TestDetail(ctx, jobURL, env, testName)
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

	return cmd
}
