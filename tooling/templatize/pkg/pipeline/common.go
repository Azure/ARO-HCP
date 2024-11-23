package pipeline

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (p *Pipeline) DeepCopy(newPipelineFilePath string) (*Pipeline, error) {
	copy := new(Pipeline)
	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline: %v", err)
	}
	err = yaml.Unmarshal(data, copy)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal pipeline: %v", err)
	}
	copy.pipelineFilePath = newPipelineFilePath
	return copy, nil
}

func (p *Pipeline) PipelineFilePath() string {
	return p.pipelineFilePath
}

func (p *Pipeline) EnterPipelineDir() (string, func(), error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}

	pipelineDir, err := filepath.Abs(filepath.Dir(p.pipelineFilePath))
	if err != nil {
		return "", nil, err
	}
	err = os.Chdir(pipelineDir)
	if err != nil {
		return "", nil, err
	}

	return pipelineDir, func() {
		_ = os.Chdir(currentDir)
	}, nil
}
