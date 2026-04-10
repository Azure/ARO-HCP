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
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/analysis"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/db"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/render"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// NewPRCommand creates the pr cobra command.
func NewPRCommand() *cobra.Command {
	var (
		jsonOutput bool
		noSync     bool
	)

	cmd := &cobra.Command{
		Use:   "pr NUMBER",
		Short: "PR failure analysis with baseline comparison",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dbPath := mustDBPath(cmd)

			prNumber, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid PR number: %w", err)
			}

			database, err := db.OpenAndMigrate(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			s := store.New(database)

			if !noSync {
				// PR triage needs broader data: presubmit for the PR + periodic for baseline
				sinceTime := time.Now().UTC().Add(-14 * 24 * time.Hour)
				if err := syncIngest(ctx, s, sinceTime, ""); err != nil {
					return err
				}
			}

			data, err := analysis.PR(ctx, s, prNumber)
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
				fmt.Fprint(cmd.OutOrStdout(), render.PR(data))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output JSON instead of markdown")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "skip data ingestion (use existing DB only)")

	return cmd
}
