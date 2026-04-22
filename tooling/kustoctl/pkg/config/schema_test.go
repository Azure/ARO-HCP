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
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaValidation(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "valid_minimal"},
		{name: "valid_with_definitions"},
		{name: "invalid_column_type", wantErr: true},
		{name: "no_databases", wantErr: true},
		{name: "empty_databases", wantErr: true},
		{name: "empty_column_name", wantErr: true},
		{name: "empty_mapping", wantErr: true},
		{name: "empty_definition_name", wantErr: true},
		{name: "additional_property_rejected", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "schema_validation", tt.name+".yaml"))
			if err != nil {
				t.Fatalf("failed to read fixture: %v", err)
			}
			err = validateSchema(data)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
