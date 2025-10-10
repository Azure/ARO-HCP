package mustgather

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/base"
	"github.com/spf13/cobra"
)

type RawCleanOptions struct {
	BaseOptions           *base.RawBaseOptions
	PathToClean           string
	ConfigFilePath        string
	MustGatherCleanBinary string
	CleanedOutputPath     string
}

func DefaultCleanOptions() *RawCleanOptions {
	return &RawCleanOptions{
		BaseOptions: base.DefaultBaseOptions(),
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

	return completed.Run(ctx)
}
func BindCleanOptions(opts *RawCleanOptions, cmd *cobra.Command) error {
	// Bind base options first
	if opts.BaseOptions == nil {
		return fmt.Errorf("base options cannot be nil")
	}
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return fmt.Errorf("failed to bind base options: %w", err)
	}
	cmd.Flags().StringVar(&opts.PathToClean, "path-to-clean", opts.PathToClean, "Path to clean")
	cmd.Flags().StringVar(&opts.ConfigFilePath, "config-file-path", opts.ConfigFilePath, "Path to ARO-HCP Service Configuration file (not must-gather-clean config)")
	cmd.Flags().StringVar(&opts.MustGatherCleanBinary, "must-gather-clean-binary", opts.MustGatherCleanBinary, "Path to must-gather-clean binary")
	cmd.Flags().StringVar(&opts.CleanedOutputPath, "cleaned-output-path", opts.CleanedOutputPath, "Path to cleaned output")

	if err := cmd.MarkFlagDirname("path-to-clean"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "path-to-clean", err)
	}
	if err := cmd.MarkFlagRequired("path-to-clean"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a required: %w", "config-file-path", err)
	}
	if err := cmd.MarkFlagDirname("config-file-path"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", "config-file-path", err)
	}
	if err := cmd.MarkFlagRequired("config-file-path"); err != nil {
		return fmt.Errorf("failed to mark flag %q as a required: %w", "path-to-clean", err)
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
}

func (opts *RawCleanOptions) Validate(ctx context.Context) (*ValidatedCleanOptions, error) {
	if opts.PathToClean == "" {
		return nil, fmt.Errorf("path-to-clean is required")
	}
	if _, err := os.Stat(opts.PathToClean); os.IsNotExist(err) {
		return nil, fmt.Errorf("path-to-clean does not exist")
	}
	if opts.ConfigFilePath == "" {
		return nil, fmt.Errorf("config-file-path is required")
	}
	if _, err := os.Stat(opts.ConfigFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config-file-path does not exist")
	}
	if opts.MustGatherCleanBinary == "" {
		return nil, fmt.Errorf("must-gather-clean-binary is required")
	}
	if _, err := os.Stat(opts.MustGatherCleanBinary); os.IsNotExist(err) {
		return nil, fmt.Errorf("must-gather-clean-binary does not exist")
	}
	if opts.CleanedOutputPath == "" {
		return nil, fmt.Errorf("cleaned-output-path is required")
	}

	return &ValidatedCleanOptions{
		RawCleanOptions: opts,
	}, nil
}

func (opts *ValidatedCleanOptions) Complete(ctx context.Context) (*CleanOptions, error) {

	return &CleanOptions{
		ValidatedCleanOptions: opts,
	}, nil
}
