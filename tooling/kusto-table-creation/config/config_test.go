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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/kusto-table-creation/internal/testutil"
	"github.com/stretchr/testify/assert/yaml"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{name: "valid_config"},
		{name: "duplicate_definition", wantErr: "duplicate definition name"},
		{name: "unknown_definition_reference", wantErr: "unknown definition"},
		{name: "duplicate_column_in_resolved_table", wantErr: "duplicate column"},
		{name: "duplicate_column_between_definition_and_inline", wantErr: "duplicate column"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "validate_config", tt.name+".yaml"))
			if err != nil {
				t.Fatalf("failed to read fixture: %v", err)
			}
			var cfg Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("failed to unmarshal fixture: %v", err)
			}

			err = validate(&cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := NewConfigFromFile("../tables.yaml")
	if err != nil {
		t.Fatalf("failed to parse tables.yaml: %v", err)
	}

	if len(cfg.Definitions) == 0 {
		t.Fatal("expected at least one definition")
	}
	if len(cfg.Tables) == 0 {
		t.Fatal("expected at least one table")
	}
}

func TestResolveColumns(t *testing.T) {
	data, err := os.ReadFile("testdata/resolve_columns_input.yaml")
	if err != nil {
		t.Fatalf("failed to read input fixture: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to unmarshal input fixture: %v", err)
	}

	columns := cfg.ResolveTableColumns(cfg.Tables[0])
	testutil.CompareWithFixture(t, columns)
}
