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

package yamlwrap

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		SkipResultValidation: false,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.InputPath, "input", opts.InputPath, "Path to the input file.")
	cmd.Flags().StringVar(&opts.OutputPath, "output", opts.OutputPath, "Path to the output file (defaults to input file).")
	cmd.Flags().StringVar(&opts.Directory, "dir", opts.Directory, "Directory to walk. Ignored if --file is provided.")
	cmd.Flags().BoolVar(&opts.SkipResultValidation, "no-validate-result", false, "Skip validation of the result YAML (default is to validate).")

	for _, flag := range []string{
		"input",
		"output",
	} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	InputPath            string
	OutputPath           string
	Directory            string
	SkipResultValidation bool
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	InputPath            string
	OutputPath           string
	Directory            string
	SkipResultValidation bool
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before Config generation can be invoked.
type completedOptions struct {
	InputPath            string
	OutputPath           string
	Directory            string
	SkipResultValidation bool
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.Directory == "" {
		// input file must be specified
		if o.InputPath == "" {
			return nil, fmt.Errorf("the file to process must be provided with --input")
		}

		// default output to input if not specified
		if o.OutputPath == "" {
			o.OutputPath = o.InputPath
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			InputPath:            o.InputPath,
			OutputPath:           o.OutputPath,
			Directory:            o.Directory,
			SkipResultValidation: o.SkipResultValidation,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	return &Options{
		completedOptions: &completedOptions{
			InputPath:            o.InputPath,
			OutputPath:           o.OutputPath,
			Directory:            o.Directory,
			SkipResultValidation: o.SkipResultValidation,
		},
	}, nil
}

func (opts *Options) Wrap(ctx context.Context) error {
	if opts.Directory != "" {
		return WrapDirectory(opts.Directory, !opts.SkipResultValidation)
	}
	return WrapFile(opts.InputPath, opts.OutputPath, !opts.SkipResultValidation)
}

func (opts *Options) Unwrap(ctx context.Context) error {
	if opts.Directory != "" {
		return UnwrapDirectory(opts.Directory)
	}
	return UnwrapFile(opts.InputPath, opts.OutputPath)
}
