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

package overview

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/pipeline-documentation/pkg/generator"
)

func DefaultGenerationOptions() *RawGenerationOptions {
	return &RawGenerationOptions{}
}

func BindGenerationOptions(opts *RawGenerationOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Input, "topology", opts.Input, "file holding topology configuration")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "output file path for overview")

	for _, flag := range []string{"topology", "output"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawGenerationOptions holds input values.
type RawGenerationOptions struct {
	Input  string
	Output string
}

// validatedGenerationOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedGenerationOptions struct {
	*RawGenerationOptions
}

type ValidatedGenerationOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedGenerationOptions
}

// completedGenerationOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedGenerationOptions struct {
	Topology   topology.Topology
	OutputFile io.WriteCloser
}

type GenerationOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedGenerationOptions
}

func (o *RawGenerationOptions) Validate() (*ValidatedGenerationOptions, error) {
	if o.Input == "" {
		return nil, fmt.Errorf("topology configuration file is required")
	}

	if o.Output == "" {
		return nil, fmt.Errorf("output markdown file is required")
	}

	if _, err := os.Stat(o.Input); os.IsNotExist(err) {
		return nil, fmt.Errorf("input file %s does not exist", o.Input)
	}

	return &ValidatedGenerationOptions{
		validatedGenerationOptions: &validatedGenerationOptions{
			RawGenerationOptions: o,
		},
	}, nil
}

func (o *ValidatedGenerationOptions) Complete() (*GenerationOptions, error) {
	rawInput, err := os.ReadFile(o.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to read input file %s: %w", o.Input, err)
	}

	var t topology.Topology
	if err := yaml.Unmarshal(rawInput, &t); err != nil {
		return nil, fmt.Errorf("failed to unmarshal topology: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(o.Output), os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", o.Output, err)
	}

	outputFile, err := os.Create(o.Output)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file %s: %w", o.Input, err)
	}

	return &GenerationOptions{
		completedGenerationOptions: &completedGenerationOptions{
			Topology:   t,
			OutputFile: outputFile,
		},
	}, nil
}

func (opts *GenerationOptions) GenerateOverview() error {
	return generator.Markdown(opts.Topology, opts.OutputFile)
}
