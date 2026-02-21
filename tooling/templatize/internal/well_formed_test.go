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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/entrypoint/entrypointutils"
)

func TestStepsWellFormed(t *testing.T) {
	repoRootDir := "../../../"
	configFile := filepath.Join(repoRootDir, "config", "config.yaml")
	configProvider, err := config.NewConfigProvider(configFile)
	if err != nil {
		t.Fatalf("failed to create config provider %s: %v", configFile, err)
	}

	// we don't particularly care what the values in the config are, we just need to create a config to resolve pipelines
	regionalEv2Cfg, err := ev2config.ResolveConfig("public", "uksouth")
	if err != nil {
		t.Fatalf("failed to retrieve embedded Ev2 configuration from ARO-Tools: %v", err)
	}
	cloud, environment := "dev", "dev"
	resolver, err := configProvider.GetResolver(&config.ConfigReplacements{
		RegionReplacement:      "string",
		RegionShortReplacement: "string",
		StampReplacement:       "string",
		CloudReplacement:       cloud,
		EnvironmentReplacement: environment,
		Ev2Config:              regionalEv2Cfg,
	})
	if err != nil {
		t.Fatalf("failed to get resolver: %v", err)
	}
	cfg, err := resolver.GetConfiguration()
	if err != nil {
		t.Fatalf("failed to get configuration from %s: %v", configFile, err)
	}

	topologyFile := filepath.Join(repoRootDir, "topology.yaml")
	rawTopology, err := os.ReadFile(topologyFile)
	if err != nil {
		t.Fatalf("failed to read input file %s: %v", topologyFile, err)
	}

	var topo topology.Topology
	if err := yaml.Unmarshal(rawTopology, &topo); err != nil {
		t.Fatalf("failed to unmarshal topology: %v", err)
	}

	if err := topo.Validate(); err != nil {
		t.Fatalf("failed to validate topology: %v", err)
	}

	for _, entrypoint := range topo.Entrypoints {
		t.Run(entrypoint.Identifier, func(t *testing.T) {
			service, err := topo.Lookup(entrypoint.Identifier)
			if err != nil {
				t.Fatalf("failed to look up entrypoint %s in topology: %v", entrypoint.Identifier, err)
			}

			pipelines := map[string]*types.Pipeline{}
			if err := entrypointutils.LoadPipelines(service, repoRootDir, pipelines, cfg); err != nil {
				t.Fatalf("failed to load pipelines: %v", err)
			}

			executionGraph, graphConstructionErr := graph.ForEntrypoint(&topo, &entrypoint, pipelines)
			if graphConstructionErr != nil {
				t.Fatalf("failed to construct graph: %v", graphConstructionErr)
			}

			illFormed := map[string]map[string]map[string]string{}
			for serviceGroupName, resourceGroups := range executionGraph.Steps {
				for resourceGroupName, steps := range resourceGroups {
					for stepName, step := range steps {
						if !step.IsWellFormedOverInputs() {
							reason := "unknown"
							switch s := step.(type) {
							case *types.ShellStep:
								if strings.Contains(s.Command, "make") && strings.Contains(s.Command, "deploy") {
									reason = "helm step needing migration"
								} else if s.WorkingDir == "" {
									reason = "raw shell step needing working directory"
								}
							}

							if _, exists := illFormed[serviceGroupName]; !exists {
								illFormed[serviceGroupName] = map[string]map[string]string{}
							}
							if _, exists := illFormed[serviceGroupName][resourceGroupName]; !exists {
								illFormed[serviceGroupName][resourceGroupName] = map[string]string{}
							}
							illFormed[serviceGroupName][resourceGroupName][stepName] = reason
							t.Errorf("%s/%s/%s: step is ill-formed over inputs: %v", serviceGroupName, resourceGroupName, stepName, reason)
						}
					}
				}
			}
		})
	}
}
