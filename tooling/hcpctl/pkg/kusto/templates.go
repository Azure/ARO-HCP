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

package kusto

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed templates
var templateFS embed.FS

func GetTemplate(name string) string {
	data, err := templateFS.ReadFile(name)
	if err != nil {
		// Panic here, since this should never happen and is obviously a bug
		panic(fmt.Errorf("failed to read template %s: %w", name, err))
	}
	return string(data)
}

// ListTemplatePaths returns all .kql.gotmpl file paths in the embedded templates directory.
func ListTemplatePaths() ([]string, error) {
	var paths []string
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".kql.gotmpl") {
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	})
	return paths, err
}

func LoadBuiltinQueryDefinitions() ([]QueryDefinition, error) {
	data, err := templateFS.ReadFile("templates/builtin/queries.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read builtin queries definition: %w", err)
	}
	var defs []QueryDefinition
	if err := yaml.Unmarshal(data, &defs); err != nil {
		return nil, fmt.Errorf("failed to parse builtin queries definition: %w", err)
	}
	return defs, nil
}

func LoadCustomQueryDefinitions() ([]QueryDefinition, error) {
	data, err := templateFS.ReadFile("templates/custom/queries.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read custom queries definition: %w", err)
	}
	var defs []QueryDefinition
	if err := yaml.Unmarshal(data, &defs); err != nil {
		return nil, fmt.Errorf("failed to parse custom queries definition: %w", err)
	}
	return defs, nil
}
