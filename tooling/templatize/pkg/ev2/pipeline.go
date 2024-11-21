package ev2

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func PrecompilePipelineForEV2(pipelineFilePath string, vars config.Variables) (string, error) {
	// switch to the pipeline file dir so all relative paths are resolved correctly
	originalDir, err := os.Getwd()
	if err != nil {
		return "", nil
	}
	pipelineDir := filepath.Dir(pipelineFilePath)
	err = os.Chdir(pipelineDir)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	// precompile the pipeline file
	pipelineFileName := filepath.Base(pipelineFilePath)
	p, err := pipeline.NewPipelineFromFile(pipelineFileName, vars)
	if err != nil {
		return "", err
	}
	err = processPipelineForEV2(p, vars)
	if err != nil {
		return "", err
	}

	// store as new file
	pipelineBytes, err := yaml.Marshal(p)
	if err != nil {
		return "", err
	}
	newPipelineFileName := "ev2-precompiled-" + pipelineFileName
	err = os.WriteFile(newPipelineFileName, pipelineBytes, 0644)
	if err != nil {
		return "", err
	}

	return filepath.Join(pipelineDir, newPipelineFileName), nil
}

func processPipelineForEV2(p *pipeline.Pipeline, vars config.Variables) error {
	_, scopeBindedVars := EV2Mapping(vars, []string{})
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.Parameters != "" {
				newParameterFilePath, err := precompileFileAndStore(step.Parameters, scopeBindedVars)
				if err != nil {
					return err
				}
				step.Parameters = newParameterFilePath
			}
		}
	}
	return nil
}

func precompileFileAndStore(filePath string, vars map[string]interface{}) (string, error) {
	preprocessedBytes, err := config.PreprocessFile(filePath, vars)
	if err != nil {
		return "", err
	}
	newFilePath := buildPrefixedFilePath(filePath, "ev2-precompiled-")
	err = os.WriteFile(newFilePath, preprocessedBytes, 0644)
	if err != nil {
		return "", err
	}
	return newFilePath, nil
}

func buildPrefixedFilePath(path, prefix string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	return filepath.Join(dir, prefix+base)
}
