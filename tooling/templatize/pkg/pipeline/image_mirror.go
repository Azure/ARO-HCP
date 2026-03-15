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

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	imagemirrorcmd "github.com/Azure/ARO-Tools/tools/imagemirror/cmd"
)

func runImageMirrorStep(id graph.Identifier, ctx context.Context, step *types.ImageMirrorStep, options *StepRunOptions, state *ExecutionState, _ io.Writer) error {
	// resolve step variables using the same mechanism as shell steps
	resolvedVars, err := resolveImageMirrorVariables(id, step, options, state)
	if err != nil {
		return fmt.Errorf("failed to resolve image mirror variables: %w", err)
	}

	opts := &imagemirrorcmd.RawMirrorOptions{
		TargetACR:      resolvedVars["TARGET_ACR"],
		SourceRegistry: resolvedVars["SOURCE_REGISTRY"],
		Repository:     resolvedVars["REPOSITORY"],
		Digest:         resolvedVars["DIGEST"],
		PullSecretKV:   resolvedVars["PULL_SECRET_KV"],
		PullSecretName: resolvedVars["PULL_SECRET"],
		DryRun:         options.DryRun,
	}

	return opts.Run(ctx)
}

// resolveImageMirrorVariables resolves the step's variable references (configRef, input)
// to concrete string values, using the same mechanism as shell steps.
func resolveImageMirrorVariables(id graph.Identifier, step *types.ImageMirrorStep, options *StepRunOptions, state *ExecutionState) (map[string]string, error) {
	variables := []types.Variable{
		{Name: "TARGET_ACR", Value: step.TargetACR},
		{Name: "SOURCE_REGISTRY", Value: step.SourceRegistry},
		{Name: "REPOSITORY", Value: step.Repository},
		{Name: "DIGEST", Value: step.Digest},
		{Name: "PULL_SECRET_KV", Value: step.PullSecretKeyVault},
		{Name: "PULL_SECRET", Value: step.PullSecretName},
	}

	state.RLock()
	resolved, err := mapStepVariables(id.ServiceGroup, variables, options.Configuration, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve variables: %w", err)
	}

	// validate required variables (pull secret vars are optional for anonymous registries)
	for _, name := range []string{"TARGET_ACR", "SOURCE_REGISTRY", "REPOSITORY", "DIGEST"} {
		if resolved[name] == "" {
			return nil, fmt.Errorf("required variable %s is not set", name)
		}
	}

	return resolved, nil
}
