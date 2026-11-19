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
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
)

// Builds the complete native candidate validation and guarded request handoff.
func newNativeValidateCommand() *cobra.Command {
	var options dependabot.NativeCandidateOptions
	command := &cobra.Command{
		Use:   "validate-candidate",
		Short: "Validate fixed native consumers and prepare the exact candidate request",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.Repository == "" || options.Inventory == "" || options.Group == "" || options.Target == "" || options.TitleFile == "" || options.BodyFile == "" || options.Artifacts == "" {
				return cli.Invalid(errors.New("--repository, --inventory, --group, --target, --title-file, --body-file and --artifact-dir are required"))
			}
			result, err := dependabot.ValidateNativeCandidate(cmd.Context(), ghapi.NativeCLIRead, options)
			if err != nil {
				return err
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
	command.Flags().StringVar(&options.Artifacts, "artifact-dir", "", "validation logs and request artifacts outside the checkout")
	command.Flags().StringVar(&options.Compiler, "compiler", "", "absolute path to the trusted pinned workflow compiler")
	return command
}

// Builds the fixed credential-free release read used before sandbox consumers run.
func newNativeSandboxReadCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "check-sandbox-read",
		Short: "Verify credential-free sandbox reads against an immutable official release",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "CI_TOKEN", "COPILOT_GITHUB_TOKEN", "GITHUB_MCP_SERVER_TOKEN"} {
				if os.Getenv(name) != "" {
					return cli.Invalid(errors.New("GitHub credentials must not be visible in the sandbox"))
				}
			}
			payload, err := ghapi.NativeCLIRead(cmd.Context(), "repos/actions/checkout/commits/v7.0.1", false, false)
			if err != nil {
				return err
			}
			var release struct {
				SHA string `json:"sha"`
			}
			if err := json.Unmarshal(payload, &release); err != nil {
				return err
			}
			if release.SHA != "3d3c42e5aac5ba805825da76410c181273ba90b1" {
				return errors.New("sandbox cannot verify the official checkout release")
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"status": "verified"})
		},
	}
}
