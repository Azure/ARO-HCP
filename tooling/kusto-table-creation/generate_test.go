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

package main

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/kusto-table-creation/config"
	"github.com/Azure/ARO-HCP/tooling/kusto-table-creation/internal/testutil"
)

func TestGenerateKQL(t *testing.T) {
	data, err := os.ReadFile("testdata/generate_kql_input.yaml")
	if err != nil {
		t.Fatalf("failed to read input fixture: %v", err)
	}
	var table config.Table
	if err := yaml.Unmarshal(data, &table); err != nil {
		t.Fatalf("failed to unmarshal input fixture: %v", err)
	}

	result := generateKQL(table.Name, table.Columns)
	testutil.CompareWithFixture(t, result, testutil.WithExtension(".kql"))
}
