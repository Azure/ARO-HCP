package pipeline

import (
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (p *Pipeline) DeepCopy(newPipelineFilePath string) (*Pipeline, error) {
	data, err := yaml.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline: %v", err)
	}

	copy, err := NewPlainPipelineFromBytes(newPipelineFilePath, data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal pipeline: %v", err)
	}
	return copy, nil
}

func (p *Pipeline) PipelineFilePath() string {
	return p.pipelineFilePath
}

func (p *Pipeline) AbsoluteFilePath(filePath string) (string, error) {
	return filepath.Abs(filepath.Join(filepath.Dir(p.pipelineFilePath), filePath))
}
