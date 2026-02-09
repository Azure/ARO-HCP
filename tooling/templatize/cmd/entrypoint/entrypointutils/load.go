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

package entrypointutils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	configtypes "github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"
)

func LoadPipelines(
	root *topology.Service, topologyDir string, pipelines map[string]*types.Pipeline,
	cfg configtypes.Configuration,
) error {
	pipelineConfigFilePath := filepath.Join(topologyDir, root.PipelinePath)
	pipe, err := types.NewPipelineFromFile(pipelineConfigFilePath, cfg)
	if err != nil {
		return fmt.Errorf("failed to precompile pipeline: %w", err)
	}
	if pipe.ServiceGroup != root.ServiceGroup {
		return fmt.Errorf("pipeline loaded from %s is for %s, not %s", pipelineConfigFilePath, pipe.ServiceGroup, root.ServiceGroup)
	}

	// Execute pipeline build step if one is specified
	if pipe.BuildStep != nil {
		if err := runBuildStep(context.Background(), *pipe.BuildStep, filepath.Dir(pipelineConfigFilePath)); err != nil {
			return fmt.Errorf("build step execution failed: %w", err)
		}
	}

	pipelines[root.ServiceGroup] = pipe

	for _, child := range root.Children {
		if err := LoadPipelines(&child, topologyDir, pipelines, cfg); err != nil {
			return err
		}
	}
	return nil
}

func runBuildStep(ctx context.Context, buildStep types.BuildStep, workingDirectory string) error {
	cmd := exec.CommandContext(ctx, buildStep.Command, buildStep.Args...)
	cmd.Dir = workingDirectory
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute build step '%s %s' in directory '%s': %w",
			buildStep.Command, strings.Join(buildStep.Args, " "), workingDirectory, err)
	}

	return nil
}
