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

package infrastructure_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run with: BICEP=/path/to/bicep go test infrastructure_identities_test.go
func TestInfrastructureIdentityDeclarations(t *testing.T) {
	bicep := os.Getenv("BICEP")
	if bicep == "" {
		var err error
		bicep, err = exec.LookPath("bicep")
		if err != nil {
			t.Fatal("set BICEP or install bicep to run compiled identity regression tests")
		}
	}
	for _, file := range []string{
		"templates/svc-cluster.bicep",
		"templates/mgmt-infra.bicep",
		"modules/aks-cluster-base.bicep",
		"modules/aks-cluster-post.bicep",
		"modules/managed-identities.bicep",
	} {
		t.Run(file, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "template.json")
			cmd := exec.Command(bicep, "build", file, "--outfile", output)
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, data)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var template identityTemplate
			if err := json.Unmarshal(data, &template); err != nil {
				t.Fatal(err)
			}
			checkIdentityDeclarations(t, template, file)
			if file == "templates/svc-cluster.bicep" {
				want := "[if(parameters('useLeasedInfrastructureIdentities'), parameters('infrastructureIdentityResourceGroup'), resourceGroup().name)]"
				var got string
				if err := json.Unmarshal(template.Outputs["identityResourceGroup"].Value, &got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("Postgres identity RG selection: got %q, want %q", got, want)
				}
			}
		})
	}
}

type identityTemplate struct {
	Resources map[string]struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Existing  bool   `json:"existing"`
		Condition string `json:"condition"`
	} `json:"resources"`
	Outputs map[string]struct {
		Value json.RawMessage `json:"value"`
	} `json:"outputs"`
}

func checkIdentityDeclarations(t *testing.T, template identityTemplate, path string) {
	t.Helper()
	names := map[string]string{}
	for symbol, resource := range template.Resources {
		if resource.Type == "Microsoft.ManagedIdentity/userAssignedIdentities" {
			// Even an existing resource aliases the created resource when its
			// resourceGroup parameter defaults to empty. ARM rejects that template.
			name := strings.ReplaceAll(resource.Name, "copyIndex('"+symbol+"')", "copyIndex()")
			if previous, found := names[name]; found {
				t.Errorf("%s: duplicate identity name %s in %s and %s", path, resource.Name, previous, symbol)
			}
			names[name] = symbol
			if !resource.Existing {
				want := "[not(parameters('useLeasedInfrastructureIdentities'))]"
				if symbol == "uami" {
					want = "[not(parameters('useLeasedIdentities'))]"
				}
				if resource.Condition != want {
					t.Errorf("%s/%s: identity creation must be disabled in leased mode, got %q", path, symbol, resource.Condition)
				}
			}
		}
	}
}

func TestPostgresUsesSelectedIdentityResourceGroup(t *testing.T) {
	data, err := os.ReadFile("svc-pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	steps := strings.Split(string(data), "\n  - name: ")
	for _, name := range []string{"cs-postgres-access", "maestro-postgres-access"} {
		found := false
		for _, step := range steps {
			if !strings.HasPrefix(step, name+"\n") {
				continue
			}
			found = true
			want := "- name: MI_RESOURCE_GROUP\n      input:\n        resourceGroup: service\n        step: cluster\n        name: identityResourceGroup"
			if !strings.Contains(step, want) {
				t.Errorf("%s must consume the service cluster's selected identity RG", name)
			}
			if !strings.Contains(step, "dependsOn:\n    - resourceGroup: service\n      step: cluster") {
				t.Errorf("%s must wait for the identity RG output", name)
			}
		}
		if !found {
			t.Errorf("missing step %s", name)
		}
	}
}
