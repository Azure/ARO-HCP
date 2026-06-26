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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsHelmTemplateDir(t *testing.T) {
	chartDir := filepath.Join("/", "chart")

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "top-level templates file",
			path:     filepath.Join(chartDir, "templates", "deployment.yaml"),
			expected: true,
		},
		{
			name:     "subchart templates file",
			path:     filepath.Join(chartDir, "charts", "sub", "templates", "foo.yaml"),
			expected: true,
		},
		{
			name:     "root values.yaml",
			path:     filepath.Join(chartDir, "values.yaml"),
			expected: false,
		},
		{
			name:     "subchart values.yaml",
			path:     filepath.Join(chartDir, "charts", "sub", "values.yaml"),
			expected: false,
		},
		{
			name:     "Chart.yaml",
			path:     filepath.Join(chartDir, "Chart.yaml"),
			expected: false,
		},
		{
			name:     "dir named templates-backup is not templates",
			path:     filepath.Join(chartDir, "templates-backup", "foo.yaml"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isHelmTemplateDir(chartDir, tc.path))
		})
	}
}

func TestIsValuesFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{name: "values.yaml", filename: "values.yaml", expected: true},
		{name: "values.yml", filename: "values.yml", expected: true},
		{name: "Values.YAML uppercase", filename: "Values.YAML", expected: true},
		{name: "my-values.yaml prefix", filename: "my-values.yaml", expected: true},
		{name: "values-override.yaml suffix", filename: "values-override.yaml", expected: true},
		{name: "my-values-override.yml mixed", filename: "my-values-override.yml", expected: true},
		{name: "Chart.yaml", filename: "Chart.yaml", expected: false},
		{name: "deployment.yaml", filename: "deployment.yaml", expected: false},
		{name: "values.json wrong extension", filename: "values.json", expected: false},
		{name: "values no extension", filename: "values", expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isValuesFile(tc.filename))
		})
	}
}
