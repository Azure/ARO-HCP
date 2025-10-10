package mustgather

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

func newCleanCommand() (*cobra.Command, error) {
	opts := DefaultCleanOptions()

	cmd := &cobra.Command{
		Use:              "clean",
		Short:            "Clean must-gather data",
		Long:             `Create must-gather-clean config file from config and possibly run must-gather-clean.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindCleanOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func (opts *CleanOptions) Run(ctx context.Context) error {
	args := []string{
		"-i", opts.PathToClean,
		"-o", opts.CleanedOutputPath,
	}

	cmd := exec.Command(opts.MustGatherCleanBinary, args...)

	output, err := cmd.CombinedOutput()
	fmt.Printf("output: %s\n", string(output))
	if err != nil {
		return fmt.Errorf("failed to run must-gather-clean: %w", err)
	}
	return nil
}

func generateMustGatherCleanConfig(ctx context.Context, opts *CleanOptions) error {
	return nil
}
