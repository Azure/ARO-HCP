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

package prometheusrules

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/internal"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		PromtoolPath: "promtool",
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "Path to configuration")
	cmd.Flags().StringVar(&opts.PromtoolPath, "promtool-path", opts.PromtoolPath, "Path to promtool binary")
	cmd.Flags().BoolVar(&opts.SkipTests, "skip-tests", opts.SkipTests, "Skip promtool test execution")
	if err := cmd.MarkFlagRequired("config-file"); err != nil {
		return err
	}
	return nil
}

type RawOptions struct {
	ConfigFile   string
	PromtoolPath string
	SkipTests    bool
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.ConfigFile == "" {
		return nil, fmt.Errorf("--config-file is required")
	}
	if !o.SkipTests {
		if o.PromtoolPath == "" {
			return nil, fmt.Errorf("--promtool-path cannot be empty when tests are enabled")
		}
		if _, err := exec.LookPath(o.PromtoolPath); err != nil {
			return nil, fmt.Errorf("promtool not found at %q: %w", o.PromtoolPath, err)
		}
	}
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	if _, err := os.Stat(o.ConfigFile); err != nil {
		return nil, fmt.Errorf("config file %q not found: %w", o.ConfigFile, err)
	}

	gen := internal.NewOptions()
	if err := gen.Complete(o.ConfigFile, o.PromtoolPath, o.SkipTests); err != nil {
		return nil, fmt.Errorf("could not complete options: %w", err)
	}
	return &Options{
		completedOptions: &completedOptions{
			generator: gen,
			skipTests: o.SkipTests,
		},
	}, nil
}

type completedOptions struct {
	generator *internal.Options
	skipTests bool
}

type Options struct {
	*completedOptions
}

func (o *Options) Run() error {
	if !o.skipTests {
		if err := o.generator.RunTests(); err != nil {
			return fmt.Errorf("testing rules failed: %w", err)
		}
	}
	if err := o.generator.Generate(); err != nil {
		return fmt.Errorf("failed to generate bicep: %w", err)
	}
	return nil
}
