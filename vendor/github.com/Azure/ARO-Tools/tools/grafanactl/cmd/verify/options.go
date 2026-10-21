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

package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/grafanactl/config"
)

// RawVerifyDashboardsOptions represents the initial, unvalidated configuration for verify operations.
type RawVerifyDashboardsOptions struct {
	ConfigFilePath string
}

// validatedVerifyDashboardsOptions is a private struct that enforces the options validation pattern.
type validatedVerifyDashboardsOptions struct {
	*RawVerifyDashboardsOptions
}

// ValidatedVerifyDashboardsOptions represents verify configuration that has passed validation.
type ValidatedVerifyDashboardsOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedVerifyDashboardsOptions
}

// CompletedVerifyDashboardsOptions represents the final, fully validated and initialized configuration
// for verify operations.
type CompletedVerifyDashboardsOptions struct {
	*validatedVerifyDashboardsOptions
	Config *config.ObservabilityConfig
}

// DefaultVerifyDashboardsOptions returns a new RawVerifyDashboardsOptions with default values
func DefaultVerifyDashboardsOptions() *RawVerifyDashboardsOptions {
	return &RawVerifyDashboardsOptions{}
}

// BindVerifyDashboardsOptions binds command-line flags to the options
func BindVerifyDashboardsOptions(opts *RawVerifyDashboardsOptions, cmd *cobra.Command) error {
	flags := cmd.Flags()
	flags.StringVar(&opts.ConfigFilePath, "config-file", "", "Path to config file with Grafana dashboard references (absolute or relative path, required)")

	_ = cmd.MarkFlagRequired("config-file")
	return nil
}

// Validate performs validation on the raw options
func (o *RawVerifyDashboardsOptions) Validate(ctx context.Context) (*ValidatedVerifyDashboardsOptions, error) {
	absPath, err := filepath.Abs(o.ConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config file path: %w", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("config file not found: %w", err)
	}

	o.ConfigFilePath = absPath

	return &ValidatedVerifyDashboardsOptions{
		validatedVerifyDashboardsOptions: &validatedVerifyDashboardsOptions{
			RawVerifyDashboardsOptions: o,
		},
	}, nil
}

// Complete performs final initialization to create fully usable verify options.
func (o *ValidatedVerifyDashboardsOptions) Complete(ctx context.Context) (*CompletedVerifyDashboardsOptions, error) {
	cfg, err := config.LoadFromFile(o.ConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &CompletedVerifyDashboardsOptions{
		validatedVerifyDashboardsOptions: o.validatedVerifyDashboardsOptions,
		Config:                           cfg,
	}, nil
}
