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

package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/types"
)

func runImageMirrorStep(id graph.Identifier, ctx context.Context, step *types.ImageMirrorStep, options *StepRunOptions, state *ExecutionState, outputWriter io.Writer) error {
	logger := logr.FromContextOrDiscard(ctx)

	tmpFile, err := os.CreateTemp("", "")
	if err != nil {
		return fmt.Errorf("error creating script temp file %w", err)
	}

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return fmt.Errorf("error make script temp file executable %w", err)
	}

	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			logger.Info("error removing tempfile", "error", err.Error())
		}
	}()

	_, err = tmpFile.Write(types.OnDemandSyncScript)
	if err != nil {
		// close file handle in error case
		if err := tmpFile.Close(); err != nil {
			logger.Info("error closing tempfile", "error", err.Error())
		}
		return fmt.Errorf("error writing script to temp file %w", err)
	}

	// must close before using or shell bash will raise errors, do not defer
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("error closing write to script file %w", err)
	}

	tmpShellStep, err := types.ResolveImageMirrorStep(*step, tmpFile.Name())
	if err != nil {
		return fmt.Errorf("error resolving image mirror step %w", err)
	}

	return runShellStep(id, tmpShellStep, ctx, "", "", options, state, outputWriter)
}
