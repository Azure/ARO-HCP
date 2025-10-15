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
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Update represents a single line update in a YAML file
type Update struct {
	Name      string // Image/component name
	OldDigest string // Current digest value
	NewDigest string // New digest value
	FilePath  string // Path to the YAML file
	JsonPath  string // JSON path to the value in the YAML
	Line      int    // Line number in the file
}

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

// GetUpdate retrieves the update(line + text) required at the specified path
func (e *Editor) GetUpdate(path string) (int, string, error) {
	parts := strings.Split(path, ".")
	node := e.root

	for _, part := range parts {
		node = e.findChild(node, part)
		if node == nil {
			return 0, "", fmt.Errorf("path %s not found", path)
		}
	}

	if node.Kind != yaml.ScalarNode {
		return 0, "", fmt.Errorf("path %s does not point to a scalar value", path)
	}

	line := node.Line
	return line, node.Value, nil
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

// ApplyUpdates applies a list of updates to the file
func (e *Editor) ApplyUpdates(updates []Update) error {
	if len(updates) == 0 {
		return nil
	}

	// Sort updates by line number before applying
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].Line < updates[j].Line
	})

	// Read the original file and copy to a temp file, applying updates on the fly
	// Then replace the original file with the temp file
	// This preserves the original formatting instead of rewriting with a yaml library
	file, err := os.Open(e.filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", e.filePath, err)
	}
	defer file.Close()

	tempFile, err := os.CreateTemp("/tmp", strings.Split(e.filePath, "/")[len(strings.Split(e.filePath, "/"))-1])
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %v", e.filePath, err)
	}
	defer tempFile.Close()

	scanner := bufio.NewScanner(file)
	writer := bufio.NewWriter(tempFile)

	lineNum := 1
	updateIndex := 0
	for scanner.Scan() {
		line := scanner.Text()
		if updateIndex < len(updates) && updates[updateIndex].Line == lineNum {
			line = strings.Replace(line, updates[updateIndex].OldDigest, updates[updateIndex].NewDigest, 1)
			updateIndex++
		}
		writer.WriteString(line + "\n")
		lineNum++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file %s: %v", e.filePath, err)
	}

	// Flush and sync before closing
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush temp file: %v", err)
	}

	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %v", err)
	}

	// Close files before renaming
	tempFile.Close()
	file.Close()

	if err := os.Rename(tempFile.Name(), e.filePath); err != nil {
		return fmt.Errorf("failed to replace original file %s with updated content: %v", e.filePath, err)
	}

	return nil
}
