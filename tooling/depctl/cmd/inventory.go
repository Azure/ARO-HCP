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
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/discovery"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
)

// Builds the engine dependencies a command requested.
func adapters(online bool, repositoryPath string) (orchestration.Deps, error) {
	deps := orchestration.Deps{
		Clock:    time.Now,
		Registry: consumers.Default(),
	}
	if online {
		if repositoryPath == "" {
			return deps, fmt.Errorf("online discovery requires --repo to locate the Go module directory")
		}
		deps.Resolver = discovery.NewCLIResolver(repositoryPath)
	}
	return deps, nil
}

// Builds the `depctl inventory` command.
func newInventoryCommand() *cobra.Command {
	var (
		repositoryPath string
		profile        string
		revision       string
		offline        bool
		output         string
	)
	command := &cobra.Command{
		Use:     "inventory",
		Short:   "Read pinned dependency versions without writing anything",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if profile != discovery.ProfileTools {
				return invalid(fmt.Errorf("unsupported profile %q", profile))
			}
			deps, err := adapters(!offline, repositoryPath)
			if err != nil {
				return invalid(err)
			}
			engine := orchestration.New(deps)
			inventory, err := engine.Inventory(cmd.Context(), repositoryPath, revision, profile, offline)
			if err != nil {
				return invalid(err)
			}
			if err := orchestration.WriteRecord(output, inventory); err != nil {
				return err
			}
			if !inventory.Complete {
				return incomplete("inventory is incomplete: %v", inventory.Errors)
			}
			return nil
		},
	}
	command.Flags().StringVar(&repositoryPath, "repo", ".", "repository working tree")
	command.Flags().StringVar(&profile, "profile", discovery.ProfileTools, "policy profile")
	command.Flags().StringVar(&revision, "revision", "", "revision to inventory (defaults to HEAD)")
	command.Flags().BoolVar(&offline, "offline", true, "restrict discovery to tracked pins")
	command.Flags().StringVar(&output, "output", "", "write the versioned inventory record here")
	return command
}

// Decodes one versioned record file.
func readRecord(path, kind string, target any) error {
	content, err := readFile(path)
	if err != nil {
		return err
	}
	if err := schema.DecodeEnvelope(content, kind, target); err != nil {
		return err
	}
	return nil
}

// Reads one record file, rejecting an empty path.
func readFile(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("a record path is required")
	}
	return osReadFile(path)
}
