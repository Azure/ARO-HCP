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

package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Components map[string]ComponentConfig `yaml:"components"`
}

type ComponentConfig struct {
	Provider     string   `yaml:"provider"`
	Locations    []string `yaml:"locations"`
	Targets      []Target `yaml:"targets"`
	BumpStrategy string   `yaml:"bumpStrategy"`
}

type Target struct {
	JsonPath string `yaml:"jsonPath"`
	FilePath string `yaml:"filePath"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	for name, comp := range cfg.Components {
		if comp.Provider == "" {
			return nil, fmt.Errorf("component %q: provider is required", name)
		}
		if len(comp.Locations) == 0 {
			return nil, fmt.Errorf("component %q: at least one location is required", name)
		}
		if len(comp.Targets) == 0 {
			return nil, fmt.Errorf("component %q: at least one target is required", name)
		}
		for i, t := range comp.Targets {
			if t.JsonPath == "" {
				return nil, fmt.Errorf("component %q target[%d]: jsonPath is required", name, i)
			}
			if t.FilePath == "" {
				return nil, fmt.Errorf("component %q target[%d]: filePath is required", name, i)
			}
		}
	}

	return &cfg, nil
}

func ReadCurrentVersion(filePath, jsonPath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filePath, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", fmt.Errorf("parsing %s: %w", filePath, err)
	}

	node := findNode(&root, strings.Split(jsonPath, "."))
	if node == nil {
		return "", fmt.Errorf("path %q not found in %s", jsonPath, filePath)
	}
	return node.Value, nil
}

func findNode(node *yaml.Node, parts []string) *yaml.Node {
	if len(parts) == 0 {
		return node
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		return findNode(node.Content[0], parts)
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == parts[0] {
			return findNode(node.Content[i+1], parts[1:])
		}
	}
	return nil
}

func WriteVersion(filePath, jsonPath, newValue string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", filePath, err)
	}

	node := findNode(&root, strings.Split(jsonPath, "."))
	if node == nil {
		return fmt.Errorf("path %q not found in %s", jsonPath, filePath)
	}

	oldValue := node.Value
	if oldValue == newValue {
		return nil
	}

	lines, err := readLines(filePath)
	if err != nil {
		return err
	}

	lineIdx := node.Line - 1
	if lineIdx < 0 || lineIdx >= len(lines) {
		return fmt.Errorf("line %d out of range in %s", node.Line, filePath)
	}

	line := lines[lineIdx]
	replaced := strings.Replace(line, oldValue, newValue, 1)
	if replaced == line {
		return fmt.Errorf("failed to replace %q with %q on line %d of %s", oldValue, newValue, node.Line, filePath)
	}
	lines[lineIdx] = replaced

	return os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func readLines(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}
	content := strings.TrimSuffix(string(data), "\n")
	return strings.Split(content, "\n"), nil
}
