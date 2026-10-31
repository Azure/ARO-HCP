// Copyright 2026 Microsoft Corporation
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
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Components map[string]ComponentConfig `yaml:"components"`
}

type ComponentConfig struct {
	Provider              string   `yaml:"provider"`
	Paused                bool     `yaml:"paused"`
	Locations             []string `yaml:"locations"`
	Targets               []Target `yaml:"targets"`
	KubernetesVersionPath string   `yaml:"kubernetesVersionPath"`
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("config file %s must contain exactly one YAML document", path)
	}
	if len(cfg.Components) == 0 {
		return nil, fmt.Errorf("at least one component is required")
	}

	for name, comp := range cfg.Components {
		if err := validateComponent(name, comp); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

func validateComponent(name string, comp ComponentConfig) error {
	if len(comp.Targets) != 1 {
		return fmt.Errorf("component %q: exactly one target is supported", name)
	}
	for i, t := range comp.Targets {
		if t.JsonPath == "" {
			return fmt.Errorf("component %q target[%d]: jsonPath is required", name, i)
		}
		if t.FilePath == "" {
			return fmt.Errorf("component %q target[%d]: filePath is required", name, i)
		}
	}

	switch comp.Provider {
	case "azure-istio":
		if len(comp.Locations) == 0 {
			return fmt.Errorf("component %q: at least one location is required", name)
		}
		if comp.KubernetesVersionPath == "" {
			return fmt.Errorf("component %q: kubernetesVersionPath is required", name)
		}
		seen := map[string]bool{}
		for _, location := range comp.Locations {
			if strings.TrimSpace(location) == "" || seen[location] {
				return fmt.Errorf("component %q: empty or duplicate location %q", name, location)
			}
			seen[location] = true
		}
		return nil
	case "":
		return fmt.Errorf("component %q: unsupported provider %q", name, comp.Provider)
	default:
		// Unknown providers are rejected at load time so typos fail closed.
		// Register new providers in providers.New and add a case here for
		// provider-specific required fields (locations, compatibility paths, etc.).
		return fmt.Errorf("component %q: unsupported provider %q", name, comp.Provider)
	}
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
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
		return "", fmt.Errorf("path %q in %s must be a nonempty string scalar", jsonPath, filePath)
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

func WriteVersion(filePath, jsonPath, expected, newValue string) error {
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
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value != expected {
		return fmt.Errorf("path %q changed since discovery or is not a string scalar", jsonPath)
	}
	if node.Value == newValue {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	lineIdx := node.Line - 1
	if lineIdx < 0 || lineIdx >= len(lines) {
		return fmt.Errorf("line %d out of range in %s", node.Line, filePath)
	}

	line := []rune(lines[lineIdx])
	start := node.Column - 1
	if start < 0 || start >= len(line) {
		return fmt.Errorf("invalid scalar position for %q", jsonPath)
	}
	oldToken, newToken := node.Value, newValue
	switch node.Style {
	case yaml.DoubleQuotedStyle:
		oldToken, newToken = strconv.Quote(node.Value), strconv.Quote(newValue)
	case yaml.SingleQuotedStyle:
		oldToken = "'" + strings.ReplaceAll(node.Value, "'", "''") + "'"
		newToken = "'" + strings.ReplaceAll(newValue, "'", "''") + "'"
	case 0:
	default:
		return fmt.Errorf("unsupported scalar style for %q", jsonPath)
	}
	if !strings.HasPrefix(string(line[start:]), oldToken) {
		return fmt.Errorf("unsupported scalar encoding for %q", jsonPath)
	}
	lines[lineIdx] = string(line[:start]) + newToken + string(line[start+len([]rune(oldToken)):])
	updated := []byte(strings.Join(lines, "\n"))
	var verified yaml.Node
	if err := yaml.Unmarshal(updated, &verified); err != nil {
		return fmt.Errorf("validating edited YAML: %w", err)
	}
	value := findNode(&verified, strings.Split(jsonPath, "."))
	if value == nil || value.Tag != "!!str" || value.Value != newValue {
		return fmt.Errorf("edited YAML does not contain expected version at %q", jsonPath)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(filePath), ".component-updater-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if _, err := temp.Write(updated); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, filePath); err != nil {
		return fmt.Errorf("replacing %s: %w", filePath, err)
	}
	return nil
}
