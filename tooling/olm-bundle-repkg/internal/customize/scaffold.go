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

package customize

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/yaml"
)

// LoadScaffoldTemplates loads manifest templates from the scaffold directory
// It only loads files that can be parsed as valid YAML (i.e., NOT Helm templates)
// Files in the "templates/" subdirectory are skipped as they should be raw Helm templates
func LoadScaffoldTemplates(scaffoldDir string) ([]unstructured.Unstructured, error) {
	if scaffoldDir == "" {
		return []unstructured.Unstructured{}, nil
	}

	if _, err := os.Stat(scaffoldDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("scaffold directory does not exist: %s", scaffoldDir)
	}

	var manifests []unstructured.Unstructured
	templatesDir := filepath.Join(scaffoldDir, "templates")
	valuesFile := filepath.Join(scaffoldDir, "values.yaml")

	err := filepath.Walk(scaffoldDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && path == templatesDir {
			return filepath.SkipDir
		}

		// Skip values.yaml - it's handled separately by LoadScaffoldValues
		if path == valuesFile {
			return nil
		}

		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			fileContent, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mapContent := make(map[string]interface{})
			err = yaml.Unmarshal(fileContent, &mapContent)
			if err != nil {
				return err
			}

			manifests = append(manifests, convertMapToUnstructured(mapContent))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifests, nil
}

// GetScaffoldTemplateFiles returns a map of scaffold template files to copy verbatim
// These are files from the scaffold/templates/ directory that should be copied as-is
func GetScaffoldTemplateFiles(scaffoldDir string) (map[string][]byte, error) {
	if scaffoldDir == "" {
		return make(map[string][]byte), nil
	}

	templatesDir := filepath.Join(scaffoldDir, "templates")

	if _, err := os.Stat(templatesDir); os.IsNotExist(err) {
		return make(map[string][]byte), nil // Not an error - no templates directory
	}

	templates := make(map[string][]byte)
	err := filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil
		}

		fileContent, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(templatesDir, path)
		if err != nil {
			return err
		}

		if strings.HasPrefix(relPath, "..") {
			return fmt.Errorf("invalid template path: %s (resolves to %s)", path, relPath)
		}

		templates[relPath] = fileContent
		return nil
	})
	if err != nil {
		return nil, err
	}
	return templates, nil
}
