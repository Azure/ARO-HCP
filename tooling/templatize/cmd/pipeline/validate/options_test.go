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

package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
)

func TestValidateHelmStep(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var overBudget strings.Builder
	for i := range 64 {
		fmt.Fprintf(&overBudget, "p%d: __p%d__\n", i, i)
	}
	for name, content := range map[string]string{
		"values.yaml":      "value: '{{ .namespace }}'\n",
		"over-budget.yaml": overBudget.String(),
		"malformed.yaml":   "value: '{{ .namespace'\n",
		"namespace.yaml":   "metadata:\n  name: '{{ .namespace }}'\n",
		"unresolved.yaml":  "metadata:\n  name: '{{ .missing }}'\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
	}

	tests := []struct {
		name          string
		valuesFile    string
		namespaceFile string
		skipBudget    bool
		expectError   bool
	}{
		{
			name:          "over-budget step fails",
			valuesFile:    "over-budget.yaml",
			namespaceFile: "namespace.yaml",
			expectError:   true,
		},
		{
			name:          "explicit skip allows over-budget step",
			valuesFile:    "over-budget.yaml",
			namespaceFile: "namespace.yaml",
			skipBudget:    true,
		},
		{
			name:          "budget enforcement still rejects malformed values template",
			valuesFile:    "malformed.yaml",
			namespaceFile: "namespace.yaml",
			expectError:   true,
		},
		{
			name:          "budget enforcement still rejects unresolved namespace reference",
			valuesFile:    "values.yaml",
			namespaceFile: "unresolved.yaml",
			expectError:   true,
		},
		{
			name:          "skip still rejects malformed values template",
			valuesFile:    "malformed.yaml",
			namespaceFile: "namespace.yaml",
			skipBudget:    true,
			expectError:   true,
		},
		{
			name:          "skip still rejects unresolved namespace reference",
			valuesFile:    "values.yaml",
			namespaceFile: "unresolved.yaml",
			skipBudget:    true,
			expectError:   true,
		},
		{
			name:          "skip still rejects missing namespace file",
			valuesFile:    "values.yaml",
			namespaceFile: "missing.yaml",
			skipBudget:    true,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			step := &types.HelmStep{
				StepMeta:       types.StepMeta{Name: "deploy"},
				ValuesFile:     tt.valuesFile,
				NamespaceFiles: []string{tt.namespaceFile},
			}
			err := validateHelmStep(step, dir, configtypes.Configuration{"namespace": "test"}, tt.skipBudget)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
