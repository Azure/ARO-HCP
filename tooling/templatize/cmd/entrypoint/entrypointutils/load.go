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
	"fmt"
	"path/filepath"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"
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

	pipelines[root.ServiceGroup] = pipe

	for _, child := range root.Children {
		if err := LoadPipelines(&child, topologyDir, pipelines, cfg); err != nil {
			return err
		}
	}
	return nil
}
