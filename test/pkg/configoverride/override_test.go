// Copyright 2026 Microsoft Corporation
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

package configoverride

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func fixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func generated(t *testing.T, input Inputs, paths Paths) (Result, map[string]interface{}) {
	t.Helper()
	result, err := Generate(input, paths)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(result.YAML, &document); err != nil {
		t.Fatalf("generated invalid YAML: %v", err)
	}
	return result, document
}

func defaultsAt(t *testing.T, document map[string]interface{}, environment string) map[string]interface{} {
	t.Helper()
	current := document
	for _, key := range []string{"clouds", "dev", "environments", environment, "defaults"} {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			t.Fatalf("missing map %q in %#v", key, current)
		}
		current = next
	}
	return current
}

func TestImageMappingsAndLoginOrder(t *testing.T) {
	// These expected mappings are intentionally independent of the production table.
	components := []struct{ variable, component string }{
		{"BACKEND_IMAGE", "backend"}, {"FRONTEND_IMAGE", "frontend"}, {"ADMIN_API_IMAGE", "adminApi"},
		{"SESSIONGATE_IMAGE", "sessiongate"}, {"HCP_RECOVERY_IMAGE", "hcpRecovery"}, {"FLEET_IMAGE", "fleet"},
		{"MGMT_AGENT_IMAGE", "mgmtAgent"}, {"SWIFT_RECORDER_IMAGE", "swiftRecorder"}, {"KUBE_APPLIER_IMAGE", "kubeApplier"},
		{"EXPORTER_IMAGE", "customExporter"},
	}
	for _, existing := range []string{"", "existing.registry", " existing.registry  another.registry "} {
		t.Run("existing="+existing, func(t *testing.T) {
			environment := map[string]string{"DEPLOY_ENV": "ci01", "LEASED_MSI_CONTAINERS": "leased", "USE_OC_LOGIN_REGISTRIES": existing}
			wantDefaults := map[string]interface{}{}
			wantLogin := existing
			for i, component := range components {
				// Repeated registries must remain repeated; repository tags and ports survive.
				registry := fmt.Sprintf("registry-%d.example:5000", i%3)
				repository := "nested/path/" + component.component + ":tag"
				digest := fmt.Sprintf("sha256:%064d", i)
				environment[component.variable] = registry + "/" + repository + "@" + digest
				wantDefaults[component.component] = map[string]interface{}{"image": map[string]interface{}{
					"registry": registry, "repository": repository, "digest": digest,
				}}
				if wantLogin != "" {
					wantLogin += " "
				}
				wantLogin += registry
			}
			var environ []string
			for name, value := range environment {
				environ = append(environ, name+"="+value)
			}
			input, err := InputsFromEnvironment(environ)
			if err != nil {
				t.Fatal(err)
			}
			result, document := generated(t, input, Paths{})
			if result.OCLoginRegistries != wantLogin {
				t.Fatalf("login registries = %q, want %q", result.OCLoginRegistries, wantLogin)
			}
			if got := defaultsAt(t, document, "ci01"); !reflect.DeepEqual(got, wantDefaults) {
				t.Fatalf("defaults = %#v, want %#v", got, wantDefaults)
			}
		})
	}
	for _, ref := range []string{"registry/repo:tag", "registry/repo@", "/repo@sha256:x", "registry/@sha256:x", "repo@sha256:x", "registry/repo@sha256:x@extra"} {
		t.Run("invalid="+ref, func(t *testing.T) {
			result, err := Generate(Inputs{DeployEnvironment: "ci01", Images: map[string]string{"BACKEND_IMAGE": ref}}, Paths{})
			if err == nil || result.YAML != nil || result.OCLoginRegistries != "" {
				t.Fatalf("Generate = %#v, %v; expected no result on error", result, err)
			}
		})
	}
}

func TestUnknownImageInputs(t *testing.T) {
	for _, environ := range [][]string{
		{"DEPLOY_ENV=ci01", "ZZ_IMAGE=registry/repo@sha256:1", "AA_IMAGE=", "ZZ_IMAGE=again"},
		{"AA_IMAGE=", "DEPLOY_ENV=ci01", "ZZ_IMAGE=registry/repo@sha256:1"},
	} {
		input, err := InputsFromEnvironment(environ)
		if err == nil || !strings.HasPrefix(err.Error(), "unknown image override keys: AA_IMAGE, ZZ_IMAGE;") || !strings.Contains(err.Error(), "BACKEND_IMAGE") {
			t.Fatalf("InputsFromEnvironment = %#v, %v; want sorted unknown keys and supported alternatives", input, err)
		}
		if !reflect.DeepEqual(input, Inputs{}) {
			t.Fatalf("invalid environment returned partial inputs: %#v", input)
		}
	}
	for _, images := range []map[string]string{
		{"AA_IMAGE": "", "ZZ_IMAGE": "registry/repo@sha256:1", "BACKEND_IMAGE": "registry/backend@sha256:1"},
		{"backend": "registry/backend@sha256:1"},
	} {
		result, err := Generate(Inputs{DeployEnvironment: "ci01", Images: images, OCLoginRegistries: "existing.registry"}, Paths{})
		if err == nil || !strings.Contains(err.Error(), "unknown image override keys:") {
			t.Fatalf("Generate = %#v, %v; want unknown direct map key error", result, err)
		}
		if _, ok := images["AA_IMAGE"]; ok && !strings.HasPrefix(err.Error(), "unknown image override keys: AA_IMAGE, ZZ_IMAGE;") {
			t.Fatalf("unsorted unknown image keys: %v", err)
		}
		if result.YAML != nil || result.OCLoginRegistries != "" {
			t.Fatalf("invalid image map returned partial output: %#v", result)
		}
	}
}

func TestRecognizedEnvironmentInputs(t *testing.T) {
	input, err := InputsFromEnvironment([]string{
		"DEPLOY_ENV=overlay-env", "ARO_HCP_DEPLOY_ENV=runtime-env",
		"LEASED_MSI_MOCK_SP=mock", "LEASED_ARM_HELPER_SP=one \t two\n",
		"LEASED_MSI_CONTAINERS=container-one container-two",
		"LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE={\"literal\":\"a=b\"}",
		"USE_OC_LOGIN_REGISTRIES= existing.registry ", "BACKEND_IMAGE=",
		"UNRELATED=value", "IMAGE_FORMAT=ignored",
	})
	want := Inputs{
		DeployEnvironment: "overlay-env", MSIMockLease: "mock",
		ARMHelperLeases: []string{"one", "two"}, MSIContainers: "container-one container-two",
		InfrastructureIdentityBundle: `{"literal":"a=b"}`,
		OCLoginRegistries:            " existing.registry ", Images: map[string]string{"BACKEND_IMAGE": ""},
	}
	if err != nil || !reflect.DeepEqual(input, want) {
		t.Fatalf("InputsFromEnvironment = %#v, %v; want %#v", input, err, want)
	}
	empty, err := InputsFromEnvironment([]string{"DEPLOY_ENV=ci01", "BACKEND_IMAGE=", "ARO_HCP_DEPLOY_ENV=other"})
	if err != nil {
		t.Fatal(err)
	}
	_, document := generated(t, empty, Paths{})
	defaults := defaultsAt(t, document, "ci01")
	if _, ok := defaults["backend"]; ok {
		t.Fatal("empty optional image generated an override")
	}
}

func TestHealthcheckAndEnvironmentKeys(t *testing.T) {
	for _, tc := range []struct {
		name, containers string
		sizing           bool
	}{
		{"absent", "", true}, {"one lease", "lease-one", false}, {"two leases", "lease-one lease-two", false}, {"present whitespace", " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, document := generated(t, Inputs{DeployEnvironment: "ci01", MSIContainers: tc.containers}, Paths{})
			if !tc.sizing {
				if len(document) != 0 {
					t.Fatalf("unrelated overrides generated: %#v", document)
				}
				return
			}
			want := map[string]interface{}{"mgmt": map[string]interface{}{"aks": map[string]interface{}{"userAgentPool": map[string]interface{}{"minCount": 1}}}}
			if got := defaultsAt(t, document, "ci01"); !reflect.DeepEqual(got, want) {
				t.Fatalf("defaults = %#v, want %#v", got, want)
			}
		})
	}
	for _, environment := range []string{"ci_01-dev", `ci01 | .other = "injection"`, "name.with.dots"} {
		t.Run(environment, func(t *testing.T) {
			_, document := generated(t, Inputs{DeployEnvironment: environment}, Paths{})
			defaultsAt(t, document, environment)
		})
	}
	if _, err := Generate(Inputs{}, Paths{}); err == nil {
		t.Fatal("empty deployment environment accepted")
	}
}

const mockCatalog = `miMockPool:
  selected:
    clientId: mock-client
    principalId: mock-principal
    certName: mock-cert
  other:
    clientId: other-client
    principalId: other-principal
    certName: other-cert
`

const helperCatalog = `armHelperPool:
  backend:
    clientId: backend-client
    principalId: backend-principal
    certName: backend-cert
  clusters-service:
    clientId: clusters-client
    principalId: clusters-principal
    certName: clusters-cert
`

func TestSelectedCatalogs(t *testing.T) {
	paths := Paths{MSIMockCatalog: fixture(t, "mock.yaml", mockCatalog), ARMHelperCatalog: fixture(t, "helper.yaml", helperCatalog)}
	for _, tc := range []struct {
		name  string
		input Inputs
		want  map[string]interface{}
	}{
		{"mock selected", Inputs{MSIMockLease: "selected"}, map[string]interface{}{"miMockClientId": "mock-client", "miMockPrincipalId": "mock-principal", "miMockCertName": "mock-cert"}},
		{"single helper fallback", Inputs{ARMHelperLeases: []string{"backend"}}, map[string]interface{}{"armHelperClientId": "backend-client", "armHelperCertName": "backend-cert", "clustersServiceArmHelperClientId": "backend-client", "clustersServiceArmHelperCertName": "backend-cert"}},
		{"two independent helpers", Inputs{ARMHelperLeases: []string{"backend", "clusters-service"}}, map[string]interface{}{"armHelperClientId": "backend-client", "armHelperCertName": "backend-cert", "clustersServiceArmHelperClientId": "clusters-client", "clustersServiceArmHelperCertName": "clusters-cert"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.input.DeployEnvironment, tc.input.MSIContainers = "ci01", "leased"
			_, document := generated(t, tc.input, paths)
			got := defaultsAt(t, document, "ci01")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("defaults = %#v, want %#v", got, tc.want)
			}
			if _, exists := got["armHelperFPAPrincipalId"]; exists {
				t.Fatal("FPA principal was overwritten")
			}
		})
	}
}

func TestCatalogErrorsAndDemandedReads(t *testing.T) {
	for _, pool := range []struct{ name, root, lease string }{
		{"mock", "miMockPool", "selected"}, {"helper", "armHelperPool", "backend"}, {"second helper", "armHelperPool", "clusters-service"},
	} {
		t.Run(pool.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, body string
				missing    bool
			}{
				{"missing file", "", true}, {"malformed YAML", "[", false}, {"wrong root", "otherPool: {}", false},
				{"wrong pool type", pool.root + ": []", false}, {"missing entry", pool.root + ": {}", false},
				{"scalar entry", pool.root + ":\n  " + pool.lease + ": bad", false},
				{"multiple documents", pool.root + ": {}\n---\nother: value", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "missing.yaml")
					if !tc.missing {
						path = fixture(t, "catalog.yaml", tc.body)
					}
					input := Inputs{DeployEnvironment: "ci01"}
					paths := Paths{MSIMockCatalog: path, ARMHelperCatalog: path}
					if pool.name == "mock" {
						input.MSIMockLease = pool.lease
					} else {
						input.ARMHelperLeases = []string{pool.lease}
					}
					if result, err := Generate(input, paths); err == nil || result.YAML != nil {
						t.Fatalf("Generate = %#v, %v; expected error", result, err)
					}
					// Neither missing nor malformed catalogs matter without selected leases.
					generated(t, Inputs{DeployEnvironment: "ci01"}, paths)
				})
			}
			for _, field := range []string{"clientId", "principalId", "certName"} {
				for _, value := range []string{"", `""`, "null", `"null"`, "42", "true", "[]", "{}"} {
					t.Run(field+"="+value, func(t *testing.T) {
						fields := map[string]interface{}{"clientId": "client", "principalId": "principal", "certName": "cert"}
						body := pool.root + ":\n  " + pool.lease + ":\n"
						for _, key := range []string{"clientId", "principalId", "certName"} {
							if key == field {
								if value != "" {
									body += "    " + key + ": " + value + "\n"
								}
								continue
							}
							body += "    " + key + ": " + fields[key].(string) + "\n"
						}
						input := Inputs{DeployEnvironment: "ci01"}
						if pool.name == "mock" {
							input.MSIMockLease = pool.lease
						} else {
							input.ARMHelperLeases = []string{pool.lease}
							if pool.name == "second helper" {
								body += "  backend:\n    clientId: valid-client\n    principalId: valid-principal\n    certName: valid-cert\n"
								input.ARMHelperLeases = []string{"backend", pool.lease}
							}
						}
						path := fixture(t, "catalog.yaml", body)
						if result, err := Generate(input, Paths{MSIMockCatalog: path, ARMHelperCatalog: path}); err == nil || result.YAML != nil {
							t.Fatalf("Generate = %#v, %v; expected error", result, err)
						}
					})
				}
			}
		})
	}
	if _, err := Generate(Inputs{DeployEnvironment: "ci01", ARMHelperLeases: []string{"one", "two", "three"}}, Paths{}); err == nil || !strings.Contains(err.Error(), "at most two") {
		t.Fatalf("expected helper count error, got %v", err)
	}
}

func TestCatalogKeysAndValuesAreLiteral(t *testing.T) {
	lease := `name" | .injected = true | "`
	value := `$(touch should-not-exist); " | .injected = true`
	catalog, err := yaml.Marshal(map[string]interface{}{"miMockPool": map[string]interface{}{
		lease: map[string]interface{}{"clientId": value, "principalId": "principal", "certName": "certificate"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, document := generated(t, Inputs{DeployEnvironment: "ci01", MSIMockLease: lease, MSIContainers: "leased"}, Paths{MSIMockCatalog: fixture(t, "catalog.yaml", string(catalog))})
	want := map[string]interface{}{"miMockClientId": value, "miMockPrincipalId": "principal", "miMockCertName": "certificate"}
	if got := defaultsAt(t, document, "ci01"); !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults = %#v, want %#v", got, want)
	}
}

func TestOverlayMerge(t *testing.T) {
	for _, tc := range []struct {
		name                string
		base, overlay, want map[string]interface{}
	}{
		{"recursive maps", map[string]interface{}{"a": map[string]interface{}{"keep": 1, "replace": "old"}}, map[string]interface{}{"a": map[string]interface{}{"replace": "new", "add": true}}, map[string]interface{}{"a": map[string]interface{}{"keep": 1, "replace": "new", "add": true}}},
		{"arrays replace", map[string]interface{}{"a": []interface{}{1, 2}}, map[string]interface{}{"a": []interface{}{3}}, map[string]interface{}{"a": []interface{}{3}}},
		{"empty arrays replace", map[string]interface{}{"a": []interface{}{1}}, map[string]interface{}{"a": []interface{}{}}, map[string]interface{}{"a": []interface{}{}}},
		{"map replaces scalar", map[string]interface{}{"a": 1}, map[string]interface{}{"a": map[string]interface{}{"b": 2}}, map[string]interface{}{"a": map[string]interface{}{"b": 2}}},
		{"scalar replaces map", map[string]interface{}{"a": map[string]interface{}{"b": 2}}, map[string]interface{}{"a": false}, map[string]interface{}{"a": false}},
		{"null replaces map", map[string]interface{}{"a": map[string]interface{}{"b": 2}}, map[string]interface{}{"a": nil}, map[string]interface{}{"a": nil}},
		{"map replaces null", map[string]interface{}{"a": nil}, map[string]interface{}{"a": map[string]interface{}{"b": 2}}, map[string]interface{}{"a": map[string]interface{}{"b": 2}}},
		{"empty map preserves entries", map[string]interface{}{"a": map[string]interface{}{"b": 2}}, map[string]interface{}{"a": map[string]interface{}{}}, map[string]interface{}{"a": map[string]interface{}{"b": 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeMaps(tc.base, tc.overlay)
			if !reflect.DeepEqual(tc.base, tc.want) {
				t.Fatalf("merged = %#v, want %#v", tc.base, tc.want)
			}
		})
	}
	overlay := `clouds:
  dev:
    environments:
      ci01:
        defaults:
          backend:
            image:
              digest: hypershift-digest
          mgmt:
            aks:
              userAgentPool:
                minCount: 7
          armHelperFPAPrincipalId: unchanged-fpa
          explicitNull: null
          enabled: false
          list: [one, two]
`
	result, document := generated(t, Inputs{DeployEnvironment: "ci01", Images: map[string]string{"BACKEND_IMAGE": "registry/repo@sha256:123"}, ARMHelperLeases: []string{"backend"}}, Paths{ARMHelperCatalog: fixture(t, "helper.yaml", helperCatalog), HypershiftOverlay: fixture(t, "overlay.yaml", overlay)})
	defaults := defaultsAt(t, document, "ci01")
	wantImage := map[string]interface{}{"registry": "registry", "repository": "repo", "digest": "hypershift-digest"}
	if image := defaults["backend"].(map[string]interface{})["image"]; !reflect.DeepEqual(image, wantImage) {
		t.Fatalf("merged image = %#v", image)
	}
	if defaults["mgmt"].(map[string]interface{})["aks"].(map[string]interface{})["userAgentPool"].(map[string]interface{})["minCount"] != 7 {
		t.Fatal("overlay did not win over healthcheck sizing")
	}
	if defaults["armHelperFPAPrincipalId"] != "unchanged-fpa" || defaults["explicitNull"] != nil || defaults["enabled"] != false || !reflect.DeepEqual(defaults["list"], []interface{}{"one", "two"}) {
		t.Fatalf("overlay scalar/array values changed: %#v", defaults)
	}
	if result.OCLoginRegistries != "registry" {
		t.Fatalf("overlay changed registry login list: %q", result.OCLoginRegistries)
	}
}

func TestOverlayErrors(t *testing.T) {
	for _, tc := range []struct {
		name, content                 string
		missing, directory, wantError bool
	}{
		{name: "missing optional file", missing: true},
		{name: "directory IO error", directory: true, wantError: true},
		{name: "malformed", content: "[", wantError: true},
		{name: "empty document", content: "", wantError: true},
		{name: "sequence root", content: "[a, b]", wantError: true},
		{name: "multiple documents", content: "{}\n---\n{}", wantError: true},
		{name: "duplicate map key", content: "key: first\nkey: second", wantError: true},
		{name: "empty map", content: "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing.yaml")
			if tc.directory {
				path = t.TempDir()
			} else if !tc.missing {
				path = fixture(t, "overlay.yaml", tc.content)
			}
			result, err := Generate(Inputs{DeployEnvironment: "ci01"}, Paths{HypershiftOverlay: path})
			if (err != nil) != tc.wantError {
				t.Fatalf("Generate error = %v, want error %v", err, tc.wantError)
			}
			if err != nil && (result.YAML != nil || result.OCLoginRegistries != "") {
				t.Fatalf("partial result on failure: %#v", result)
			}
		})
	}
}
