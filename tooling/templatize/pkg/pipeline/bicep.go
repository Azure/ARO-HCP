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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

func modeFromString(mode string) armresources.DeploymentMode {
	switch mode {
	case "Complete":
		return armresources.DeploymentModeComplete
	default:
		return armresources.DeploymentModeIncremental
	}
}

func transformBicepToARMWhatIfDeployment(ctx context.Context, bicepClient *bicep.LSPClient, bicepParameterTemplateFile, deploymentMode, pipelineWorkingDir string, cfg types.Configuration, inputs map[string]any) (*armresources.DeploymentWhatIfProperties, error) {
	template, params, err := transformParameters(ctx, bicepClient, cfg, inputs, bicepParameterTemplateFile, pipelineWorkingDir)
	if err != nil {
		return nil, err
	}
	return &armresources.DeploymentWhatIfProperties{
		Mode:       to.Ptr(modeFromString(deploymentMode)),
		Template:   template,
		Parameters: params,
	}, nil
}

func transformBicepToARMDeployment(ctx context.Context, bicepClient *bicep.LSPClient, bicepParameterTemplateFile, deploymentMode, pipelineWorkingDir string, cfg types.Configuration, inputs map[string]any) (*armresources.DeploymentProperties, error) {
	template, params, err := transformParameters(ctx, bicepClient, cfg, inputs, bicepParameterTemplateFile, pipelineWorkingDir)
	if err != nil {
		return nil, err
	}
	return &armresources.DeploymentProperties{
		Mode:       to.Ptr(modeFromString(deploymentMode)),
		Template:   template,
		Parameters: params,
	}, nil
}

func transformParameters(ctx context.Context, bicepClient *bicep.LSPClient, cfg types.Configuration, inputs map[string]any, bicepParameterTemplateFile, pipelineWorkingDir string) (map[string]interface{}, map[string]interface{}, error) {
	bicepParameterFile := filepath.Join(pipelineWorkingDir, bicepParameterTemplateFile)
	bicepParamContent, err := config.PreprocessFile(bicepParameterFile, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to preprocess file: %w", err)
	}
	bicepParamBaseDir := filepath.Dir(bicepParameterFile)
	bicepParamFile, err := os.CreateTemp(bicepParamBaseDir, "bicep-params-*.bicepparam")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(bicepParamFile.Name())
	_, err = bicepParamFile.Write(bicepParamContent)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write to target file: %w", err)
	}

	// Ensure absolute path for BuildParams (temporary fix for path resolution issue)
	bicepParamAbsPath, err := filepath.Abs(bicepParamFile.Name())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	rawTemplate, rawParams, err := bicepClient.BuildParams(ctx, bicepParamAbsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build params: %w", err)
	}
	var template, fullParams map[string]interface{}
	if err := json.Unmarshal([]byte(rawTemplate), &template); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal template: %w", err)
	}
	if err := json.Unmarshal([]byte(rawParams), &fullParams); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal params: %w", err)
	}
	params, ok := fullParams["parameters"].(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("failed to unmarshal params, found %T at .parameters", fullParams["parameters"])
	}
	for k, v := range inputs {
		params[k] = map[string]interface{}{
			"value": v,
		}
	}
	return template, params, nil
}

func hasTemplateResources(template any) bool {
	if templateAsMap, isMap := template.(map[string]interface{}); isMap {
		if val, hasResources := templateAsMap["resources"]; hasResources {
			if res, isList := val.([]any); isList {
				if len(res) == 0 {
					return false
				}
				return hasNonDeploymentResources(res)
			}
		}
	}
	return false
}

// Some of the resources in an arm template are just nested deployments that also just have existing resources
func hasNonDeploymentResources(resources []any) bool {
	for _, resource := range resources {
		if resourceAsMap, isMap := resource.(map[string]interface{}); isMap {
			if resourceAsMap["type"] != "Microsoft.Resources/deployments" {
				return true
			}
			if propertiesAsMap, isMap := resourceAsMap["properties"].(map[string]interface{}); isMap {
				return hasTemplateResources(propertiesAsMap["template"])
			}
		}
	}
	return false
}
