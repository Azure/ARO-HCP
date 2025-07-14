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

package configuration

import (
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/configuration/validate"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/configuration/render"
)

func NewCommand() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:              "configuration",
		Short:            "Operate over service configuration.",
		SilenceUsage:     true,
		TraverseChildren: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	commands := []func() (*cobra.Command, error){
		render.NewCommand,
		func() (*cobra.Command, error) {
			return validate.NewCommand("https://github.com/Azure/ARO-HCP.git")
		},
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			return nil, err
		}
		cmd.AddCommand(c)
	}

	cmd.SetHelpCommand(&cobra.Command{Hidden: true})

	return cmd, nil
}
