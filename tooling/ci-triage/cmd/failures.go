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
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/db"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/render"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// NewFailuresCommand creates the failures cobra command.
func NewFailuresCommand() *cobra.Command {
	var (
		since      string
		until      string
		jsonOutput bool
		noSync     bool
	)

	cmd := &cobra.Command{
		Use:   "failures ENV",
		Short: "Evidence packet for one environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env := args[0]
			dbPath := mustDBPath(cmd)

			sinceStr, err := config.ParseSince(since)
			if err != nil {
				return err
			}
			if sinceStr == "" {
				sinceStr = time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
			}

			database, err := db.OpenAndMigrate(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			s := store.New(database)

			if !noSync {
				sinceTime, err := parseSinceToTime(sinceStr)
				if err != nil {
					return err
				}
				if err := syncIngest(ctx, s, sinceTime, env); err != nil {
					return err
				}
			}

			data, err := analysis.Failures(ctx, s, env, sinceStr, until)
			if err != nil {
				return err
			}

			if jsonOutput {
				out, err := render.JSON(data)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), out)
			} else {
				fmt.Fprint(cmd.OutOrStdout(), render.Evidence(data))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "7d", "ISO date or relative (7d, 24h, 2w)")
	cmd.Flags().StringVar(&until, "until", "", "ISO date end filter")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output JSON instead of markdown")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "skip data ingestion (use existing DB only)")

	return cmd
}
