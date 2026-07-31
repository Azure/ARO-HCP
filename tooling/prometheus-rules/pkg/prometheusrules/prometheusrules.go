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
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/internal"
)

// CorrelationMapEntry re-exports the internal type for callers.
type CorrelationMapEntry = internal.CorrelationMapEntry

// CorrelationIDSegment re-exports the internal type for callers.
type CorrelationIDSegment = internal.CorrelationIDSegment

func DefaultOptions() *RawOptions {
	return &RawOptions{
		PromtoolPath: "promtool",
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "Path to configuration")
	cmd.Flags().StringVar(&opts.PromtoolPath, "promtool-path", opts.PromtoolPath, "Path to promtool binary")
	cmd.Flags().BoolVar(&opts.SkipTests, "skip-tests", opts.SkipTests, "Skip promtool test execution")
	cmd.Flags().BoolVar(&opts.CorrelationMap, "correlation-map", opts.CorrelationMap, "Output a YAML correlation map instead of generating Bicep")
	return nil
}

type RawOptions struct {
	ConfigFile     string
	PromtoolPath   string
	SkipTests      bool
	CorrelationMap bool
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.ConfigFile == "" {
		return nil, fmt.Errorf("--config-file is required")
	}
	if !o.SkipTests {
		if o.PromtoolPath == "" {
			return nil, fmt.Errorf("--promtool-path cannot be empty when tests are enabled")
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
	promtoolPath := o.PromtoolPath
	if !o.SkipTests {
		resolved, err := exec.LookPath(o.PromtoolPath)
		if err != nil {
			return nil, fmt.Errorf("promtool not found at %q: %w", o.PromtoolPath, err)
		}
		promtoolPath = resolved
	}

	gen := internal.NewOptions()
	if err := gen.Complete(o.ConfigFile, promtoolPath); err != nil {
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

// GenerateCorrelationMap loads rule configs and returns a structured mapping
// from group/alert to parsed correlation ID segments.
func GenerateCorrelationMap(configFilePaths []string) ([]CorrelationMapEntry, error) {
	var all []CorrelationMapEntry
	for _, configFilePath := range configFilePaths {
		o := internal.NewOptions()
		if err := o.Complete(configFilePath, ""); err != nil {
			return nil, fmt.Errorf("could not complete options for %s: %w", configFilePath, err)
		}
		entries, err := o.CorrelationMap()
		if err != nil {
			return nil, fmt.Errorf("failed to generate correlation map for %s: %w", configFilePath, err)
		}
		all = append(all, entries...)
	}
	return all, nil
}
