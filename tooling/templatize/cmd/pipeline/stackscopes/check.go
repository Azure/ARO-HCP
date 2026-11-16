// Copyright 2026 Microsoft Corporation
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

package stackscopes

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/config"
	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
)

// stampsChecked are the stamps rendered for stamped resource groups. Two are enough to prove that a resource group
// name changes per stamp; the real stamp count lives outside this repository.
var stampsChecked = []int{1, 2}

// servicePipeline is a pipeline file to check, together with the service group it belongs to.
type servicePipeline struct {
	ServiceGroup string
	Path         string
}

// renderFunc returns the resolved configuration for one region and stamp of the cloud/environment being checked.
type renderFunc func(region string, stamp int) (configtypes.Configuration, error)

// stackScope identifies one deployment stack within a cloud and environment. Azure identifies a stack by its name and
// the scope it is deployed at: the resource group for ResourceGroup-level stacks, the subscription for
// Subscription-level stacks. The generated stack name is derived from the orchestrated step name (and, after the
// rename, the environment and cloud) - never the region or stamp - so executions that agree on all of these fields
// operate on the same stack.
type stackScope struct {
	Name          string
	Subscription  string
	ResourceGroup string
}

// collision is a stack scope that more than one execution of the same cloud and environment deploys to.
type collision struct {
	Cloud       string
	Environment string
	Scope       stackScope
	Executions  []string
}

func (c collision) String() string {
	scope := fmt.Sprintf("subscription %q", c.Scope.Subscription)
	if c.Scope.ResourceGroup != "" {
		scope += fmt.Sprintf(", resource group %q", c.Scope.ResourceGroup)
	}
	return fmt.Sprintf(
		"%s/%s: deployment stack %q is deployed by %d executions (%s) into the same %s. "+
			"The stack name does not include the region or stamp, so they share one stack and can prune each other's resources. "+
			"Restrict the resource group to one execution per scope with executionConstraints, move the step to a region- or stamp-scoped resource group, or keep the step as action: ARM.",
		c.Cloud, c.Environment, c.Scope.Name,
		len(c.Executions), strings.Join(c.Executions, ", "), scope,
	)
}

// orchestratedStepName mirrors the sdp-pipelines generator, which names a stack after the service group without its
// first four segments, the logical resource group and the step, e.g. "Global.singleton.acrs".
func orchestratedStepName(serviceGroup, resourceGroup, step string) string {
	parts := strings.Split(serviceGroup, ".")
	if len(parts) > 4 {
		parts = parts[4:]
	}
	return fmt.Sprintf("%s.%s.%s", strings.Join(parts, "."), resourceGroup, step)
}

// executionRegions returns the regions in which a resource group executes for the given cloud and environment. It
// mirrors how the deployment manifest generator turns executionConstraints into execution scopes: the first constraint
// matching the cloud and environment wins; several listed regions execute in each of them; no listed regions execute
// in every region; a single listed region, or a singleton, executes once.
func executionRegions(constraints []types.ExecutionConstraint, cloud, environment string, allRegions []string) []string {
	if len(constraints) == 0 {
		return allRegions
	}
	for _, constraint := range constraints {
		matchesCloud := len(constraint.Clouds) == 0 || slices.Contains(constraint.Clouds, cloud)
		matchesEnvironment := len(constraint.Environments) == 0 || slices.Contains(constraint.Environments, environment)
		if !matchesCloud || !matchesEnvironment {
			continue
		}
		switch {
		case len(constraint.Regions) == 1:
			return constraint.Regions
		case constraint.Singleton && len(constraint.Regions) > 1:
			return constraint.Regions[:1]
		case constraint.Singleton:
			return allRegions[:1]
		case len(constraint.Regions) == 0:
			return allRegions
		default:
			return constraint.Regions
		}
	}
	return nil
}

// stackStep is an ARMStack step that deploys (rather than deletes) a deployment stack.
type stackStep struct {
	Name            string
	DeploymentLevel string
}

// stackSteps returns the steps in a resource group that deploy a deployment stack. Steps with operation: delete only
// remove a stack and succeed without changes when it is already gone, so several executions running them is harmless.
func stackSteps(rg *types.ResourceGroup) []stackStep {
	var steps []stackStep
	for _, step := range rg.Steps {
		s, ok := step.(*types.ARMStackStep)
		if !ok || s.IsDelete() {
			continue
		}
		steps = append(steps, stackStep{Name: s.StepName(), DeploymentLevel: s.DeploymentLevel})
	}
	return steps
}

// scopeFor returns the stack scope of a step deployed into the given subscription and resource group. Azure names are
// case-insensitive, so the scope is lowercased to compare it.
func scopeFor(name, deploymentLevel, subscription, resourceGroup string) stackScope {
	name, subscription, resourceGroup = strings.ToLower(name), strings.ToLower(subscription), strings.ToLower(resourceGroup)
	switch strings.ToLower(deploymentLevel) {
	case "subscription":
		return stackScope{Name: name, Subscription: subscription}
	case "managementgroup", "tenant":
		return stackScope{Name: name}
	default:
		return stackScope{Name: name, Subscription: subscription, ResourceGroup: resourceGroup}
	}
}

// findCollisions renders every pipeline for each execution of the given cloud and environment and reports the stack
// scopes that more than one execution deploys to.
func findCollisions(cloud, environment string, allRegions []string, pipelines []servicePipeline, render renderFunc) ([]collision, error) {
	if len(allRegions) == 0 {
		return nil, nil
	}

	configs := map[string]configtypes.Configuration{}
	raws := map[string][]byte{}
	// parse returns nil when the pipeline, rendered for this region and stamp, has no ARMStack step. Rendering the
	// template text is cheap; parsing and schema-validating every pipeline for every region is not.
	parse := func(p servicePipeline, region string, stamp int) (*types.Pipeline, error) {
		key := fmt.Sprintf("%s/%d", region, stamp)
		cfg, ok := configs[key]
		if !ok {
			var err error
			if cfg, err = render(region, stamp); err != nil {
				return nil, fmt.Errorf("%s/%s/%s stamp %d: failed to render configuration: %w", cloud, environment, region, stamp, err)
			}
			configs[key] = cfg
		}
		raw, ok := raws[p.Path]
		if !ok {
			var err error
			if raw, err = os.ReadFile(p.Path); err != nil {
				return nil, fmt.Errorf("%s: failed to read pipeline %s: %w", p.ServiceGroup, p.Path, err)
			}
			raws[p.Path] = raw
		}
		text, err := config.PreprocessContent(raw, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s/%s/%s stamp %d: %s: failed to render pipeline %s: %w", cloud, environment, region, stamp, p.ServiceGroup, p.Path, err)
		}
		if !strings.Contains(string(text), types.StepActionARMStack) {
			return nil, nil
		}
		parsed, err := types.NewPipelineFromBytes(raw, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s/%s/%s stamp %d: %s: failed to parse pipeline %s: %w", cloud, environment, region, stamp, p.ServiceGroup, p.Path, err)
		}
		return parsed, nil
	}

	executions := map[stackScope]sets.Set[string]{}
	for _, p := range pipelines {
		// Pipelines are templates: actions, steps, resource groups and constraints can differ per region or stamp, so
		// read them from each execution's own rendering.
		for _, region := range allRegions {
			for _, stamp := range stampsChecked {
				rendered, err := parse(p, region, stamp)
				if err != nil {
					return nil, err
				}
				if rendered == nil {
					continue
				}
				for _, rg := range rendered.ResourceGroups {
					if stamp != stampsChecked[0] && !rg.Stamped {
						continue
					}
					if !slices.Contains(executionRegions(rg.ExecutionConstraints, cloud, environment, allRegions), region) {
						continue
					}
					execution := region
					if rg.Stamped {
						execution = fmt.Sprintf("%s stamp %d", region, stamp)
					}
					for _, step := range stackSteps(rg) {
						name := orchestratedStepName(p.ServiceGroup, rg.Name, step.Name)
						scope := scopeFor(name, step.DeploymentLevel, rg.Subscription, rg.ResourceGroup)
						if executions[scope] == nil {
							executions[scope] = sets.New[string]()
						}
						executions[scope].Insert(fmt.Sprintf("%s: %s/%s/%s", execution, p.ServiceGroup, rg.Name, step.Name))
					}
				}
			}
		}
	}

	var collisions []collision
	for scope, execs := range executions {
		if execs.Len() > 1 {
			collisions = append(collisions, collision{Cloud: cloud, Environment: environment, Scope: scope, Executions: sets.List(execs)})
		}
	}
	sort.Slice(collisions, func(i, j int) bool {
		return collisions[i].String() < collisions[j].String()
	})
	return collisions, nil
}
