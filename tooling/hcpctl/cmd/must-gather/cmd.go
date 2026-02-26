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

package mustgather

import (
	"github.com/spf13/cobra"
)

var ServicesLogDirectory = "service"
var HostedControlPlaneLogDirectory = "hosted-control-plane"
var InfraLogDirectory = "cluster"

var OptionsOutputFile = "options.json"

func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "must-gather",
		Short:   "Azure Data Explorer must-gather operations",
		GroupID: group,
		Long: `must-gather provides data collection operations for Azure Data Explorer clusters.

This command group includes subcommands for querying Azure Data Explorer instances
and collecting diagnostic data for troubleshooting and analysis.`,
		Example: `  hcpctl must-gather query --kusto my-kusto-cluster --region eastus
  hcpctl must-gather query --kusto my-kusto-cluster --region eastus --output results.json`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add query subcommand
	queryCmd, err := newQueryCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryCmd)

	// Add query-infra subcommand
	queryInfraCmd, err := newQueryInfraCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryInfraCmd)

	// Add legacy-query subcommand
	queryCmdLegacy, err := newQueryCommandLegacy()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryCmdLegacy)

	cleanCommand, err := newCleanCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(cleanCommand)

	return cmd, nil
}
