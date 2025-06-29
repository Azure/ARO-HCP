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

package breakglass

import (
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/breakglass/hcp"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/breakglass/mc"
)

func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:              "breakglass",
		Short:            "Emergency access tools for ARO-HCP clusters",
		Long:             "breakglass provides emergency access tools for different ARO-HCP cluster types.",
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	hcpCmd, err := hcp.NewCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(hcpCmd)

	mcCmd, err := mc.NewCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(mcCmd)

	return cmd, nil
}
