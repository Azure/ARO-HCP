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

package yaml

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Editor provides functionality to edit YAML files while preserving structure
type Editor struct {
	filePath string
	root     *yaml.Node
}

// NewEditor creates a new YAML editor for the specified file
func NewEditor(filePath string) (*Editor, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %s: %w", filePath, err)
	}

	return &Editor{
		filePath: filePath,
		root:     &root,
	}, nil
}

// GetValue retrieves the value at the specified path
func (e *Editor) GetValue(path string) (string, error) {
	parts := strings.Split(path, ".")
	node := e.root

	for _, part := range parts {
		node = e.findChild(node, part)
		if node == nil {
			return "", fmt.Errorf("path %s not found", path)
		}
	}

	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("path %s does not point to a scalar value", path)
	}

	return node.Value, nil
}

// SetValue updates the value at the specified path
func (e *Editor) SetValue(path, value string) error {
	parts := strings.Split(path, ".")
	node := e.root

	for _, part := range parts {
		node = e.findChild(node, part)
		if node == nil {
			return fmt.Errorf("path %s not found", path)
		}
	}

	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("path %s does not point to a scalar value", path)
	}

	node.Value = value

	return nil
}

// Save writes the YAML back to the file
func (e *Editor) Save() error {
	data, err := yaml.Marshal(e.root)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(e.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", e.filePath, err)
	}

	return nil
}

// findChild finds a child node with the specified key
func (e *Editor) findChild(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind == yaml.DocumentNode && len(parent.Content) > 0 {
		parent = parent.Content[0]
	}

	if parent.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(parent.Content); i += 2 {
		keyNode := parent.Content[i]
		valueNode := parent.Content[i+1]

		if keyNode.Value == key {
			return valueNode
		}
	}

	return nil
}
