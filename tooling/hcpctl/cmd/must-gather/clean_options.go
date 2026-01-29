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

package mustgather

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type RawCleanOptions struct {
	PathToClean           string
	ServiceConfigPath     string
	MustGatherCleanBinary string
	CleanedOutputPath     string
	CleanConfigPath       string
}

func DefaultCleanOptions() *RawCleanOptions {
	return &RawCleanOptions{
		PathToClean: "must-gather-clean",
	}
}
func (opts *RawCleanOptions) Run(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	// defer os.RemoveAll(completed.WorkingDir)

	return completed.Run(ctx)
}
func BindCleanOptions(opts *RawCleanOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.PathToClean, "path-to-clean", opts.PathToClean, "Path to clean")
	cmd.Flags().StringVar(&opts.ServiceConfigPath, "service-config-path", opts.ServiceConfigPath, "Path to ARO-HCP Service Configuration file (not must-gather-clean config)")
	cmd.Flags().StringVar(&opts.MustGatherCleanBinary, "must-gather-clean-binary", opts.MustGatherCleanBinary, "Path to must-gather-clean binary")
	cmd.Flags().StringVar(&opts.CleanedOutputPath, "cleaned-output-path", opts.CleanedOutputPath, "Path to cleaned output")
	cmd.Flags().StringVar(&opts.CleanConfigPath, "clean-config-path", opts.CleanConfigPath, "Path to must-gather-clean config, will be extended with ARO-HCP Service Configuration literals")

	if err := cmd.MarkFlagDirname("path-to-clean"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "path-to-clean", err)
	}
	if err := cmd.MarkFlagRequired("path-to-clean"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a required: %w", "config-file-path", err)
	}
	if err := cmd.MarkFlagDirname("service-config-path"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "service-config-path", err)
	}
	if err := cmd.MarkFlagRequired("service-config-path"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a required: %w", "service-config-path", err)
	}
	if err := cmd.MarkFlagFilename("must-gather-clean-binary"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "must-gather-clean-binary", err)
	}
	if err := cmd.MarkFlagRequired("must-gather-clean-binary"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a required: %w", "must-gather-clean-binary", err)
	}
	if err := cmd.MarkFlagDirname("cleaned-output-path"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a directory: %w", "cleaned-output-path", err)
	}
	return nil
}

type ValidatedCleanOptions struct {
	*RawCleanOptions
}

type CleanOptions struct {
	*ValidatedCleanOptions
	WorkingDir string
}

func (opts *RawCleanOptions) Validate(ctx context.Context) (*ValidatedCleanOptions, error) {
	if opts.PathToClean == "" {
		return nil, fmt.Errorf("path-to-clean is required")
	}
	if opts.ServiceConfigPath == "" {
		return nil, fmt.Errorf("config-file-path is required")
	}
	if opts.MustGatherCleanBinary == "" {
		return nil, fmt.Errorf("must-gather-clean-binary is required")
	}
	if opts.CleanedOutputPath == "" {
		return nil, fmt.Errorf("cleaned-output-path is required")
	}

	return &ValidatedCleanOptions{
		RawCleanOptions: opts,
	}, nil
}

func (opts *ValidatedCleanOptions) Complete(ctx context.Context) (*CleanOptions, error) {
	workingDir, err := os.MkdirTemp("", "must-gather-clean-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create working directory: %w", err)
	}

	return &CleanOptions{
		ValidatedCleanOptions: opts,
		WorkingDir:            workingDir,
	}, nil
}
