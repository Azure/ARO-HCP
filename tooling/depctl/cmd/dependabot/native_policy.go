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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

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

type nativePolicyOperation uint8

const (
	nativePolicyTree nativePolicyOperation = iota
	nativePolicyCoverage
	nativePolicyAdmission
)

type nativePolicyRawOptions struct {
	operation nativePolicyOperation
	pathsOnly bool
}

type nativePolicyValidatedOptions struct {
	operation nativePolicyOperation
	pathsOnly bool
	input     nativePolicyInput
}

type nativePolicyOptions struct {
	operation     nativePolicyOperation
	input         nativePolicyInput
	read          dependabot.NativeReadFunc
	output        io.Writer
	coveragePaths []string
	paths         map[string]bool
	available     map[string]bool
	live          map[string]bool
}

// Validate decodes strict evidence and checks required local inputs before execution.
func (o *nativePolicyRawOptions) Validate(reader io.Reader) (*nativePolicyValidatedOptions, error) {
	var input nativePolicyInput
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, cli.Invalid(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, cli.Invalid(fmt.Errorf("expected one policy evidence object"))
	}
	switch o.operation {
	case nativePolicyTree:
		if !filepath.IsAbs(input.Root) || filepath.Clean(input.Root) != input.Root ||
			len(input.Base) != 40 || strings.Trim(input.Base, "0123456789abcdef") != "" ||
			len(input.Tree) != 40 || strings.Trim(input.Tree, "0123456789abcdef") != "" {
			return nil, &dependabot.NativePolicyError{Reason: "tree validation requires a repository root and full lowercase base/tree SHAs"}
		}
	case nativePolicyCoverage:
		if !o.pathsOnly && len(input.Intents) == 0 {
			return nil, &dependabot.NativePolicyError{Reason: "missing coverage intent"}
		}
	}
	return &nativePolicyValidatedOptions{operation: o.operation, pathsOnly: o.pathsOnly, input: input}, nil
}

// Complete prepares immutable evidence sets, read dependencies and output.
func (o *nativePolicyValidatedOptions) Complete(output io.Writer) *nativePolicyOptions {
	options := &nativePolicyOptions{operation: o.operation, input: o.input, output: output}
	switch o.operation {
	case nativePolicyCoverage:
		if o.pathsOnly {
			paths := dependabot.NativeCoveragePaths(o.input.Intents)
			options.coveragePaths = make([]string, 0, len(paths))
			for filename := range paths {
				options.coveragePaths = append(options.coveragePaths, filename)
			}
			sort.Strings(options.coveragePaths)
			return options
		}
		options.available = nativePolicyPaths(o.input.Available)
	case nativePolicyAdmission:
		options.paths = nativePolicyPaths(o.input.Paths)
		options.available = nativePolicyPaths(o.input.Available)
		options.live = nativePolicyPaths(o.input.Live)
	}
	options.read = ghapi.NativeCLIRead
	return options
}

// Run checks completed policy evidence or emits the bounded coverage path query.
func (o *nativePolicyOptions) Run(ctx context.Context) error {
	switch o.operation {
	case nativePolicyTree:
		return dependabot.NativeValidateTree(ctx, o.read, o.input.Root, o.input.Base, o.input.Tree, o.input.Paths, o.input.Group, o.input.Complete)
	case nativePolicyCoverage:
		if o.coveragePaths != nil {
			return json.NewEncoder(o.output).Encode(o.coveragePaths)
		}
		return dependabot.NativeVerifyCoverage(ctx, o.read, o.input.Intents, o.input.content, o.available)
	case nativePolicyAdmission:
		return dependabot.NativeAdmissionSafe(ctx, o.read, o.input.Group, o.paths, o.available, o.live, o.input.content)
	}
	return fmt.Errorf("unknown native policy operation")
}

// Runs the typed validation, completion and execution phases for policy commands.
func runNativePolicyCommand(cmd *cobra.Command, raw *nativePolicyRawOptions) error {
	validated, err := raw.Validate(cmd.InOrStdin())
	if err != nil {
		return err
	}
	return validated.Complete(cmd.OutOrStdout()).Run(cmd.Context())
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
	options := &nativePolicyRawOptions{operation: nativePolicyTree}
	return &cobra.Command{Use: "check-tree", Short: "Check exact native replacement tree evidence from stdin", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNativePolicyCommand(cmd, options)
		},
	}
}

// Builds immutable intent coverage checks and their bounded evidence path query.
func newNativeCheckCoverageCommand() *cobra.Command {
	options := &nativePolicyRawOptions{operation: nativePolicyCoverage}
	command := &cobra.Command{Use: "check-coverage", Short: "Check native intent coverage evidence from stdin", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNativePolicyCommand(cmd, options)
		},
	}
	command.Flags().BoolVar(&options.pathsOnly, "paths-only", false, "return only the immutable evidence paths required by the intents")
	return command
}

// Builds native admission eligibility checks without publication or arbitrary execution.
func newNativeCheckAdmissionCommand() *cobra.Command {
	options := &nativePolicyRawOptions{operation: nativePolicyAdmission}
	return &cobra.Command{Use: "check-admission", Short: "Check native admission consumer evidence from stdin", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNativePolicyCommand(cmd, options)
		},
	}
}
