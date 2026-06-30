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

package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	pipelinetypes "github.com/Azure/ARO-Tools/pipelines/types"
)

type rawPipelineParams struct {
	ResourceGroups []struct {
		Steps []struct {
			Action     string `json:"action"`
			Parameters string `json:"parameters,omitempty"`
		} `json:"steps"`
	} `json:"resourceGroups"`
}

func (opts *ValidationOptions) ValidateBicepparamTemplates(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	bicepparamFiles := sets.New[string]()
	for _, service := range opts.Topology.Services {
		if err := collectBicepparamFiles(opts.Topology, service, bicepparamFiles); err != nil {
			return err
		}
	}

	sortedFiles := bicepparamFiles.UnsortedList()
	slices.Sort(sortedFiles)
	var errors []string
	for _, file := range sortedFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read bicepparam file %s: %w", file, err)
		}
		if err := config.ValidateSimpleFieldAccess(content); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", file, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("bicepparam template validation failed:\n%s", strings.Join(errors, "\n"))
	}

	logger.V(3).Info("All bicepparam templates passed validation.", "count", len(bicepparamFiles))
	return nil
}

func collectBicepparamFiles(t *topology.CombinedTopology, service topology.Service, files sets.Set[string]) error {
	if len(service.PipelinePath) > 0 {
		baseDir, err := t.GetTopologyDirForServiceGroup(service.ServiceGroup)
		if err != nil {
			return fmt.Errorf("%s: failed to get topology dir: %w", service.ServiceGroup, err)
		}

		pipelineFile := filepath.Join(baseDir, service.PipelinePath)
		raw, err := os.ReadFile(pipelineFile)
		if err != nil {
			return fmt.Errorf("%s: failed to read pipeline %s: %w", service.ServiceGroup, pipelineFile, err)
		}

		var pipeline rawPipelineParams
		if err := yaml.Unmarshal(raw, &pipeline); err != nil {
			return fmt.Errorf("%s: failed to parse pipeline %s: %w", service.ServiceGroup, pipelineFile, err)
		}

		pipelineDir := filepath.Dir(pipelineFile)
		for _, rg := range pipeline.ResourceGroups {
			for _, step := range rg.Steps {
				if step.Action != pipelinetypes.StepActionARM && step.Action != pipelinetypes.StepActionARMStack {
					continue
				}
				if len(step.Parameters) == 0 {
					continue
				}
				files.Insert(filepath.Join(pipelineDir, step.Parameters))
			}
		}
	}

	for _, child := range service.Children {
		if err := collectBicepparamFiles(t, child, files); err != nil {
			return err
		}
	}
	return nil
}
