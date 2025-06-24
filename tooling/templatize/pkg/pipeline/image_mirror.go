package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Azure/ARO-Tools/pkg/types"
)

func runImageMirrorStep(ctx context.Context, step *types.ImageMirrorStep, options *PipelineRunOptions, inputs map[string]Output, outputWriter io.Writer) error {
	tmpFile, err := os.CreateTemp("", "")
	if err != nil {
		return fmt.Errorf("error creating script temp file %w", err)
	}

	err = os.Chmod(tmpFile.Name(), 0755)
	if err != nil {
		return fmt.Errorf("error make script temp file executable %w", err)
	}

	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write(types.OnDemandSyncScript)
	if err != nil {
		return fmt.Errorf("error writing script to temp file %w", err)
	}

	err = tmpFile.Close()
	if err != nil {
		return fmt.Errorf("error closing write to script file %w", err)
	}

	tmpShellStep, err := types.ResolveImageMirrorStep(*step, tmpFile.Name())
	if err != nil {
		return fmt.Errorf("error resolving image mirror step %w", err)
	}

	return runShellStep(tmpShellStep, ctx, "", options, inputs, outputWriter)
}
