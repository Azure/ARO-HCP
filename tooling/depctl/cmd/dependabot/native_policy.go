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
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
)

type nativePolicyInput struct {
	Root      string            `json:"root"`
	Base      string            `json:"base"`
	Tree      string            `json:"tree"`
	Paths     []string          `json:"paths"`
	Group     map[string]any    `json:"group"`
	Complete  bool              `json:"complete"`
	Intents   []map[string]any  `json:"intents"`
	Available []string          `json:"available"`
	Live      []string          `json:"live"`
	Contents  map[string]string `json:"contents"`
}

// Reads one trusted lifecycle evidence envelope without coercing identity numbers.
func readNativePolicyInput(cmd *cobra.Command) (nativePolicyInput, error) {
	var input nativePolicyInput
	decoder := json.NewDecoder(cmd.InOrStdin())
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, cli.Invalid(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return input, cli.Invalid(fmt.Errorf("expected one policy evidence object"))
	}
	return input, nil
}

// Reads only the immutable contents supplied by the trusted lifecycle caller.
func (input nativePolicyInput) content(filename string) (string, error) {
	value, ok := input.Contents[filename]
	if !ok {
		return "", fmt.Errorf("missing immutable coverage content: %s", filename)
	}
	return value, nil
}

// Converts exact path evidence without treating absent availability as an empty tree.
func nativePolicyPaths(paths []string) map[string]bool {
	if paths == nil {
		return nil
	}
	result := make(map[string]bool, len(paths))
	for _, filename := range paths {
		result[filename] = true
	}
	return result
}

// Builds the read-only local tree policy used by the trusted publisher.
func newNativeCheckTreeCommand() *cobra.Command {
	return &cobra.Command{Use: "check-tree", Short: "Check exact native replacement tree evidence from stdin", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := readNativePolicyInput(cmd)
			if err != nil {
				return err
			}
			return dependabot.NativeValidateTree(cmd.Context(), ghapi.NativeCLIRead, input.Root, input.Base, input.Tree, input.Paths, input.Group, input.Complete)
		},
	}
}

// Builds immutable intent coverage checks and their bounded evidence path query.
func newNativeCheckCoverageCommand() *cobra.Command {
	var pathsOnly bool
	command := &cobra.Command{Use: "check-coverage", Short: "Check native intent coverage evidence from stdin", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := readNativePolicyInput(cmd)
			if err != nil {
				return err
			}
			if pathsOnly {
				paths := []string{}
				for filename := range dependabot.NativeCoveragePaths(input.Intents) {
					paths = append(paths, filename)
				}
				sort.Strings(paths)
				return json.NewEncoder(cmd.OutOrStdout()).Encode(paths)
			}
			return dependabot.NativeVerifyCoverage(cmd.Context(), ghapi.NativeCLIRead, input.Intents, input.content, nativePolicyPaths(input.Available))
		},
	}
	command.Flags().BoolVar(&pathsOnly, "paths-only", false, "return only the immutable evidence paths required by the intents")
	return command
}

// Builds native admission eligibility checks without publication or arbitrary execution.
func newNativeCheckAdmissionCommand() *cobra.Command {
	return &cobra.Command{Use: "check-admission", Short: "Check native admission consumer evidence from stdin", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := readNativePolicyInput(cmd)
			if err != nil {
				return err
			}
			return dependabot.NativeAdmissionSafe(cmd.Context(), ghapi.NativeCLIRead, input.Group, nativePolicyPaths(input.Paths), nativePolicyPaths(input.Available), nativePolicyPaths(input.Live), input.content)
		},
	}
}
