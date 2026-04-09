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

package verifiers

import (
	"testing"
)

// TestLoadKustoTableSchemas verifies that loadKustoTableSchemas successfully
// loads schemas from tables.yaml and the bicep database mapping.
func TestLoadKustoTableSchemas(t *testing.T) {
	schemas, err := loadKustoTableSchemas()
	if err != nil {
		t.Fatalf("loadKustoTableSchemas failed: %v", err)
	}
	if len(schemas) == 0 {
		t.Fatal("expected at least one table schema")
	}

	// Every schema must have a name, database, and at least one column
	for _, s := range schemas {
		if s.Name == "" {
			t.Error("schema with empty name")
		}
		if s.Database == "" {
			t.Errorf("schema %q has empty database", s.Name)
		}
		if len(s.Columns) == 0 {
			t.Errorf("schema %s.%s has no columns", s.Database, s.Name)
		}
	}
}
