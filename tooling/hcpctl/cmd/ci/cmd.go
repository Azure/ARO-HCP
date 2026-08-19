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

package ci

import (
	"github.com/spf13/cobra"
)

// NewCommand creates the "ci" command group, which bundles helper operations
// used by ARO-HCP continuous integration.
func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "ci",
		Short:   "CI helper operations",
		GroupID: group,
		Long: `ci provides helper operations used by ARO-HCP continuous integration.

This command group includes subcommands invoked by CI pipelines, such as
checking the published health status of the ARO-HCP components affected by a
change, auto-detected from the files changed in <base-ref>..HEAD.`,
		Example: `  hcpctl ci component-health --base-ref ${PULL_BASE_SHA}`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add component-health subcommand
	componentHealthCmd, err := newComponentHealthCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(componentHealthCmd)

	return cmd, nil
}
