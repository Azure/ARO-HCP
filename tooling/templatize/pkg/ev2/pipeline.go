package ev2

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

const precompiledPrefix = "ev2-precompiled-"

func PrecompilePipelineFileForEV2(pipelineFilePath string, vars config.Variables) (string, error) {
	precompiledPipeline, err := PrecompilePipelineForEV2(pipelineFilePath, vars)
	if err != nil {
		return "", err
	}

	// store as new file
	pipelineBytes, err := yaml.Marshal(precompiledPipeline)
	if err != nil {
		return "", err
	}
	err = os.WriteFile(precompiledPipeline.PipelineFilePath(), pipelineBytes, 0644)
	if err != nil {
		return "", err
	}

	return precompiledPipeline.PipelineFilePath(), nil
}

func PrecompilePipelineForEV2(pipelineFilePath string, vars config.Variables) (*pipeline.Pipeline, error) {
	// load the pipeline and referenced files
	originalPipeline, err := pipeline.NewPipelineFromFile(pipelineFilePath, vars)
	if err != nil {
		return nil, err
	}
	referencedFiles, err := readReferencedPipelineFiles(originalPipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to read referenced files of pipeline %s: %w", originalPipeline.PipelineFilePath(), err)
	}

	// precompile the pipeline and referenced files
	processedPipeline, processedFiles, err := processPipelineForEV2(originalPipeline, referencedFiles, vars)
	if err != nil {
		return nil, err
	}

	// store the processed files to disk relative to the pipeline directory
	_, restoreDir, err := processedPipeline.EnterPipelineDir()
	if err != nil {
		return nil, fmt.Errorf("failed to enter pipeline directory: %w", err)
	}
	defer restoreDir()
	for filePath, content := range processedFiles {
		err := os.WriteFile(filePath, content, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write precompiled file %q: %w", filePath, err)
		}
	}

	return processedPipeline, nil
}

func readReferencedPipelineFiles(p *pipeline.Pipeline) (map[string][]byte, error) {
	// switch to pipeline directory to ensure relative paths are resolvable
	_, restoreDir, err := p.EnterPipelineDir()
	if err != nil {
		return nil, fmt.Errorf("failed to enter pipeline directory: %w", err)
	}
	defer restoreDir()

	referencedFiles := make(map[string][]byte)
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			if step.Parameters != "" {
				paramFileContent, err := os.ReadFile(step.Parameters)
				if err != nil {
					return nil, fmt.Errorf("failed to read parameter file %q: %w", step.Parameters, err)
				}
				referencedFiles[step.Parameters] = paramFileContent
			}
		}
	}
	return referencedFiles, nil
}

func processPipelineForEV2(p *pipeline.Pipeline, referencedFiles map[string][]byte, vars config.Variables) (*pipeline.Pipeline, map[string][]byte, error) {
	processingPipeline, err := p.DeepCopy(buildPrefixedFilePath(p.PipelineFilePath(), precompiledPrefix))
	if err != nil {
		return nil, nil, err
	}
	processedFiles := make(map[string][]byte)
	_, scopeBoundVars := EV2Mapping(vars, []string{})
	for _, rg := range processingPipeline.ResourceGroups {
		for _, step := range rg.Steps {
			// preprocess the parameters file with scopebinding variables
			if step.Parameters != "" {
				paramFileContent, ok := referencedFiles[step.Parameters]
				if !ok {
					return nil, nil, fmt.Errorf("parameter file %q not found", step.Parameters)
				}
				preprocessedBytes, err := config.PreprocessContent(paramFileContent, scopeBoundVars)
				if err != nil {
					return nil, nil, err
				}
				newParameterFilePath := buildPrefixedFilePath(step.Parameters, precompiledPrefix)
				processedFiles[newParameterFilePath] = preprocessedBytes
				step.Parameters = newParameterFilePath
			}
		}
	}
	return processingPipeline, processedFiles, nil
}

func buildPrefixedFilePath(path, prefix string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	return filepath.Join(dir, prefix+base)
}
