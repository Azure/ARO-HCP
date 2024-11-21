package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func transformBicepToARM(ctx context.Context, bicepParameterTemplateFile string, vars config.Variables) (*armresources.DeploymentProperties, error) {
	// preprocess bicep parameter file and store it
	bicepParamContent, error := config.PreprocessFile(bicepParameterTemplateFile, vars)
	if error != nil {
		return nil, fmt.Errorf("failed to preprocess file: %w", error)
	}
	bicepParamBaseDir := filepath.Dir(bicepParameterTemplateFile)
	bicepParamFile, err := os.CreateTemp(bicepParamBaseDir, "bicep-params-*.bicepparam")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(bicepParamFile.Name())
	_, err = bicepParamFile.Write(bicepParamContent)
	if err != nil {
		return nil, fmt.Errorf("failed to write to target file: %w", err)
	}

	// transform to json
	cmd := exec.CommandContext(ctx, "az", "bicep", "build-params", "-f", bicepParamFile.Name(), "--stdout")
	output, err := cmd.Output()
	if err != nil {
		combinedOutput, _ := cmd.CombinedOutput()
		return nil, fmt.Errorf("failed to get output from command: %w\n%s", err, string(combinedOutput))
	}

	// parse json and build DeploymentProperties
	var result generationResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal output: %w", err)
	}
	template, err := result.Template()
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	params, err := result.Parameters()
	if err != nil {
		return nil, fmt.Errorf("failed to get parameters: %w", err)
	}
	return &armresources.DeploymentProperties{
		Mode:       to.Ptr(armresources.DeploymentModeIncremental),
		Template:   template,
		Parameters: params,
	}, nil
}

type generationResult struct {
	ParametersJson string `json:"parametersJson"`
	TemplateJson   string `json:"templateJson"`
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
