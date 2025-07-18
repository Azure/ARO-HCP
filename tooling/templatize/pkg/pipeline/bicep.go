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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func modeFromString(mode string) armresources.DeploymentMode {
	switch mode {
	case "Complete":
		return armresources.DeploymentModeComplete
	default:
		return armresources.DeploymentModeIncremental
	}
}

func transformBicepToARMWhatIfDeployment(ctx context.Context, bicepParameterTemplateFile, deploymentMode, pipelineWorkingDir string, cfg config.Configuration, inputs map[string]any) (*armresources.DeploymentWhatIfProperties, error) {
	template, params, err := transformParameters(ctx, cfg, inputs, bicepParameterTemplateFile, pipelineWorkingDir)
	if err != nil {
		return nil, err
	}
	return &armresources.DeploymentWhatIfProperties{
		Mode:       to.Ptr(modeFromString(deploymentMode)),
		Template:   template,
		Parameters: params,
	}, nil
}

func transformBicepToARMDeployment(ctx context.Context, bicepParameterTemplateFile, deploymentMode, pipelineWorkingDir string, cfg config.Configuration, inputs map[string]any) (*armresources.DeploymentProperties, error) {
	template, params, err := transformParameters(ctx, cfg, inputs, bicepParameterTemplateFile, pipelineWorkingDir)
	if err != nil {
		return nil, err
	}
	return &armresources.DeploymentProperties{
		Mode:       to.Ptr(modeFromString(deploymentMode)),
		Template:   template,
		Parameters: params,
	}, nil
}

func transformParameters(ctx context.Context, cfg config.Configuration, inputs map[string]any, bicepParameterTemplateFile, pipelineWorkingDir string) (map[string]interface{}, map[string]interface{}, error) {
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

	var buildParamsCmd = []string{"az", "bicep", "build-params", "-f", bicepParamFile.Name(), "--stdout"}
	cmd := exec.CommandContext(ctx, buildParamsCmd[0], buildParamsCmd[1:]...)
	var errBuff bytes.Buffer
	cmd.Stderr = &errBuff
	output, err := cmd.Output()
	if err != nil {
		cmdStr := strings.Join(buildParamsCmd, " ")
		cmdErr := errBuff.String()
		return nil, nil, fmt.Errorf("failed to get output from command '%s': %w\n%s", cmdStr, err, cmdErr)
	}

	var result generationResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal output: %w", err)
	}
	template, err := result.Template()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get template: %w", err)
	}
	params, err := result.Parameters()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get parameters: %w", err)
	}
	for k, v := range inputs {
		params[k] = map[string]interface{}{
			"value": v,
		}
	}
	return template, params, nil
}

type generationResult struct {
	ParametersJson string `json:"parametersJson"`
	TemplateJson   string `json:"templateJson"`
}

func hasTemplateResources(template any) bool {
	if templateAsMap, isMap := template.(map[string]interface{}); isMap {
		if val, hasResources := templateAsMap["resources"]; hasResources {
			if res, isList := val.([]any); isList {
				return len(res) > 0
			}
		}
	}
	return false
}

func (gr generationResult) Parameters() (map[string]interface{}, error) {
	var parameters = map[string]interface{}{}
	if err := json.Unmarshal([]byte(gr.ParametersJson), &parameters); err != nil {
		return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
	}
	return parameters["parameters"].(map[string]interface{}), nil
}

func (gr generationResult) Template() (map[string]interface{}, error) {
	var template map[string]interface{}
	if err := json.Unmarshal([]byte(gr.TemplateJson), &template); err != nil {
		return nil, fmt.Errorf("failed to unmarshal template: %w", err)
	}
	return template, nil
}
