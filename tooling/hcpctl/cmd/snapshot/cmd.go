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

package snapshot

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the "snapshot" top-level command with its subcommands.
func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "snapshot",
		Short:   "Gather and analyze structured diagnostic snapshots",
		GroupID: group,
		Long: `Gather a minimal, structured diagnostic snapshot by tracing ARM requests
through frontend, backend, Clusters Service, Maestro, and HyperShift.

The output is a directory containing per-resource query results with a
manifest.json index, suitable for automated analysis or manual review.

Use one of the subcommands to specify the entrypoint:
  from-resource    Start from a resource group and time window
  from-prow-job    Start from a Prow job URL (use --test to select a specific test)
  analyze          Run LLM-driven root cause analysis on gathered data`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	fromResourceCmd, err := newFromResourceCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(fromResourceCmd)

	fromProwJobCmd, err := newFromProwJobCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(fromProwJobCmd)

	analyzeCmd, err := newAnalyzeCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(analyzeCmd)

	return cmd, nil
}
