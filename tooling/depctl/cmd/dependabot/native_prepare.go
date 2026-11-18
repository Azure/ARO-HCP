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

package dependabot

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
)

// Builds offline packaging for a candidate after consumer validation.
func newNativePrepareCommand() *cobra.Command {
	var options dependabot.NativePrepareOptions
	command := &cobra.Command{
		Use:   "prepare-request",
		Short: "Package an already-validated native candidate without network access",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.Repository == "" || options.Inventory == "" || options.Group == "" || options.Target == "" || options.TitleFile == "" || options.BodyFile == "" || options.Artifacts == "" {
				return cli.Invalid(errors.New("--repository, --inventory, --group, --target, --title-file, --body-file and --artifact-dir are required"))
			}
			result, err := dependabot.PrepareNativeRequest(cmd.Context(), options)
			if err != nil {
				return cli.Invalid(err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	command.Flags().StringVar(&options.Repository, "repository", "", "OWNER/REPO")
	command.Flags().StringVar(&options.Root, "repo", ".", "candidate repository root")
	command.Flags().StringVar(&options.Inventory, "inventory", "", "native inventory JSON file")
	command.Flags().StringVar(&options.Group, "group", "", "native family group ID")
	command.Flags().StringVar(&options.Target, "target", "", "exact inventory target")
	command.Flags().StringVar(&options.TitleFile, "title-file", "", "UTF-8 publication title file")
	command.Flags().StringVar(&options.BodyFile, "body-file", "", "UTF-8 publication body file")
	command.Flags().StringVar(&options.Artifacts, "artifact-dir", "", "directory for the digest-bound request")
	return command
}
