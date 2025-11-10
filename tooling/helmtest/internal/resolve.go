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

package internal

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"
)

func FindHelmTestFiles(pathToSearch string) ([]string, error) {
	allTests := make([]string, 0)
	err := filepath.WalkDir(pathToSearch, func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), "helmtest_") {
			allTests = append(allTests, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking directory: %v", err)
	}
	return allTests, nil
}

func FindHelmSteps(configPath string) ([]HelmStepWithPath, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("error loading config: %v", err)
	}
	topo, err := topology.Load(filepath.Join(RepoRoot, "topology.yaml"))
	if err != nil {
		return nil, fmt.Errorf("error loading topology: %v", err)
	}
	allSteps := make([]HelmStepWithPath, 0)

	for _, service := range topo.Services {
		helmStepsWithPath, err := recursiveLoadPipelineReturnHelmSteps(service, cfg)
		if err != nil {
			return nil, fmt.Errorf("error loading pipeline: %v", err)
		}
		allSteps = append(allSteps, helmStepsWithPath...)
	}

	return allSteps, nil
}

func recursiveLoadPipelineReturnHelmSteps(service topology.Service, cfg config.Configuration) ([]HelmStepWithPath, error) {
	pipeline, err := types.NewPipelineFromFile(filepath.Join(RepoRoot, service.PipelinePath), cfg)
	if err != nil {
		return nil, fmt.Errorf("error loading pipeline: %v", err)
	}
	allSteps := make([]HelmStepWithPath, 0)
	for _, resourceGroups := range pipeline.ResourceGroups {
		for _, step := range resourceGroups.Steps {
			if helmStep, ok := step.(*types.HelmStep); ok {
				allSteps = append(allSteps, HelmStepWithPath{
					HelmStep:     helmStep,
					PipelinePath: service.PipelinePath,
					AKSCluster:   helmStep.AKSCluster,
				})
			}
		}
	}
	for _, child := range service.Children {
		helmStepsWithPath, err := recursiveLoadPipelineReturnHelmSteps(child, cfg)
		if err != nil {
			return nil, fmt.Errorf("error loading pipeline: %v", err)
		}
		allSteps = append(allSteps, helmStepsWithPath...)
	}
	return allSteps, nil
}
