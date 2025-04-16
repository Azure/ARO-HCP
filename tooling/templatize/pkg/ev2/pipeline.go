package ev2

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

const precompiledPrefix = "ev2-precompiled-"

func PrecompilePipelineFileForEV2(pipelineFilePath string, cfg config.Configuration) (string, error) {
	precompiledPipeline, err := PrecompilePipelineForEV2(pipelineFilePath, cfg)
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

func PrecompilePipelineForEV2(pipelineFilePath string, cfg config.Configuration) (*pipeline.Pipeline, error) {
	// load the pipeline and referenced files
	originalPipeline, err := pipeline.NewPipelineFromFile(pipelineFilePath, cfg)
	if err != nil {
		return nil, err
	}
	referencedFiles, err := readReferencedPipelineFiles(originalPipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to read referenced files of pipeline %s: %w", originalPipeline.PipelineFilePath(), err)
	}

	// precompile the pipeline and referenced files
	processedPipeline, processedFiles, err := processPipelineForEV2(originalPipeline, referencedFiles, cfg)
	if err != nil {
		return nil, err
	}

	// store the processed files to disk relative to the pipeline directory
	for filePath, content := range processedFiles {
		absFilePath, err := processedPipeline.AbsoluteFilePath(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute file path for %q: %w", filePath, err)
		}
		err = os.WriteFile(absFilePath, content, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write precompiled file %q: %w", filePath, err)
		}
	}

	return processedPipeline, nil
}

func readReferencedPipelineFiles(p *pipeline.Pipeline) (map[string][]byte, error) {
	referencedFiles := make(map[string][]byte)
	for _, rg := range p.ResourceGroups {
		for _, step := range rg.Steps {
			switch concreteStep := step.(type) {
			case *pipeline.ARMStep:
				absFilePath, err := p.AbsoluteFilePath(concreteStep.Parameters)
				if err != nil {
					return nil, fmt.Errorf("failed to get absolute file path for %q: %w", concreteStep.Parameters, err)
				}
				paramFileContent, err := os.ReadFile(absFilePath)
				if err != nil {
					return nil, fmt.Errorf("failed to read parameter file %q: %w", concreteStep.Parameters, err)
				}
				referencedFiles[concreteStep.Parameters] = paramFileContent
			}
		}
	}
	return referencedFiles, nil
}

func processPipelineForEV2(p *pipeline.Pipeline, referencedFiles map[string][]byte, cfg config.Configuration) (*pipeline.Pipeline, map[string][]byte, error) {
	processingPipeline, err := p.DeepCopy(buildPrefixedFilePath(p.PipelineFilePath(), precompiledPrefix))
	if err != nil {
		return nil, nil, err
	}
	processedFiles := make(map[string][]byte)
	_, scopeBoundBicepParamVars := EV2Mapping(cfg, NewBicepParamPlaceholders(), []string{})
	for _, rg := range processingPipeline.ResourceGroups {
		for _, step := range rg.Steps {
			// preprocess the parameters file with scopebinding variables
			switch concreteStep := step.(type) {
			case *pipeline.ARMStep:
				paramFileContent, ok := referencedFiles[concreteStep.Parameters]
				if !ok {
					return nil, nil, fmt.Errorf("parameter file %q not found", concreteStep.Parameters)
				}
				preprocessedBytes, err := config.PreprocessContent(paramFileContent, scopeBoundBicepParamVars)
				if err != nil {
					return nil, nil, err
				}
				newParameterFilePath := buildPrefixedFilePath(concreteStep.Parameters, precompiledPrefix)
				processedFiles[newParameterFilePath] = preprocessedBytes
				concreteStep.Parameters = newParameterFilePath
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
