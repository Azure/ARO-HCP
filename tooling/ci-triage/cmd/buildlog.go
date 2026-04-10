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
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/buildlog"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
)

// NewBuildLogCommand creates the build-log cobra command.
func NewBuildLogCommand() *cobra.Command {
	var (
		step  string
		lines int
	)

	cmd := &cobra.Command{
		Use:   "build-log URL [ENV]",
		Short: "Build-log tail from a specific job",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			baseURL := config.NormalizeBaseURL(args[0])
			env := ""
			if len(args) > 1 {
				env = args[1]
			}
			if env == "" {
				env = config.DetectEnvFromURL(baseURL)
			}
			if env == "" {
				return fmt.Errorf("cannot detect env from URL; specify env explicitly")
			}

			gcsClient := gcs.NewClient(&http.Client{Timeout: 30 * time.Second})
			result, err := buildlog.Fetch(ctx, gcsClient, baseURL, env, step, lines)
			if err != nil {
				return err
			}

			if result == nil {
				data, _ := json.MarshalIndent(map[string]string{
					"status":  "not_found",
					"message": fmt.Sprintf("build-log.txt not found for %s step", step),
				}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}

	cmd.Flags().StringVar(&step, "step", "test", "build step (test or provision)")
	cmd.Flags().IntVar(&lines, "lines", 80, "number of tail lines to show")

	return cmd
}
