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

package options

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
)

// RawValidateOptions holds the validate command input.
type RawValidateOptions struct {
	ConfigPath string
}

// DefaultValidateOptions returns defaults for the validate command.
func DefaultValidateOptions() *RawValidateOptions {
	return &RawValidateOptions{}
}

// BindValidateOptions binds the validate flags to the raw options.
func BindValidateOptions(opts *RawValidateOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "", "Path to image-updater configuration file")
	return cmd.MarkFlagRequired("config")
}

// validatedValidateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedValidateOptions struct {
	*RawValidateOptions
}

// ValidatedValidateOptions contains validated configuration and inputs
type ValidatedValidateOptions struct {
	*validatedValidateOptions
}

// Validate checks the input before any configuration is read.
func (o *RawValidateOptions) Validate(_ context.Context) (*ValidatedValidateOptions, error) {
	if strings.TrimSpace(o.ConfigPath) == "" {
		return nil, fmt.Errorf("config path is required")
	}

	return &ValidatedValidateOptions{validatedValidateOptions: &validatedValidateOptions{RawValidateOptions: o}}, nil
}

// completedValidateOptions is a private wrapper that enforces a call of Complete() before a report can be printed.
type completedValidateOptions struct {
	Config *config.Config
}

// CompletedValidateOptions contains a loaded and validated configuration
type CompletedValidateOptions struct {
	*completedValidateOptions
}

// Complete loads the configuration and checks the bundle partition.
func (v *ValidatedValidateOptions) Complete(_ context.Context) (*CompletedValidateOptions, error) {
	cfg, err := config.LoadStrict(v.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if err := cfg.ValidateBundles(); err != nil {
		return nil, fmt.Errorf("invalid bundles: %w", err)
	}

	return &CompletedValidateOptions{completedValidateOptions: &completedValidateOptions{Config: cfg}}, nil
}

// PrintReport writes the validated bundle partition to out.
func (c *CompletedValidateOptions) PrintReport(out io.Writer) error {
	bundleNames := make([]string, 0, len(c.Config.Bundles))
	for name := range c.Config.Bundles {
		bundleNames = append(bundleNames, name)
	}
	sort.Strings(bundleNames)

	for _, name := range bundleNames {
		groups := slices.Clone(c.Config.Bundles[name])
		sort.Strings(groups)

		components := 0
		for _, imageConfig := range c.Config.Images {
			if slices.Contains(groups, imageConfig.Group) {
				components++
			}
		}
		if _, err := fmt.Fprintf(out, "%s: %d groups, %d components\n", name, len(groups), components); err != nil {
			return fmt.Errorf("failed to write bundle %q: %w", name, err)
		}
	}

	_, err := fmt.Fprintf(out, "validated %d components in %d groups across %d bundles\n",
		len(c.Config.Images), len(c.Config.Groups()), len(bundleNames))
	if err != nil {
		return fmt.Errorf("failed to write summary: %w", err)
	}

	return nil
}
