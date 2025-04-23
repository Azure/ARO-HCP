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
