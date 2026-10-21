// Copyright 2026 Microsoft Corporation
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

package root

import (
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/cmd/pin"
)

// NewCommand builds the root entra-app-credentials command.
func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:           "entra-app-credentials",
		Short:         "Manage Microsoft Entra application credentials.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	pinCommand, err := pin.NewCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(pinCommand)
	return cmd, nil
}
