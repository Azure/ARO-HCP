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

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/version"
)

// Builds the `depctl version` command.
func newVersionCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:     "version",
		Short:   "Print binary provenance and the record schema version",
		GroupID: groupHelper,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := map[string]string{
				"commit":        version.Revision(),
				"buildDate":     version.BuildDate(),
				"platform":      version.Platform(),
				"schemaVersion": schema.Version,
			}
			if !asJSON {
				cmd.Printf("commit: %s\nbuild date: %s\nplatform: %s\nschema version: %s\n",
					info["commit"], info["buildDate"], info["platform"], info["schemaVersion"])
				return nil
			}
			encoded, err := json.Marshal(info)
			if err != nil {
				return err
			}
			cmd.Println(string(encoded))
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "output-json", false, "print machine-readable provenance")
	_ = fmt.Sprint
	return command
}
