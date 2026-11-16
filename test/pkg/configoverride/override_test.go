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

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
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
			environment := map[string]string{"DEPLOY_ENV": "ci01", leaseenv.MSIContainers: "leased", "USE_OC_LOGIN_REGISTRIES": existing}
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
		DeployEnvironment:            "overlay-env",
		MSIContainers:                "container-one container-two",
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

func TestCIRPAllowedSource(t *testing.T) {
	for _, source := range []string{"", "192.0.2.42"} {
		t.Run("source="+source, func(t *testing.T) {
			input, err := InputsFromEnvironment([]string{
				"DEPLOY_ENV=ci01",
				"LEASED_MSI_CONTAINERS=leased",
				"CI_RP_ALLOWED_SOURCE=" + source,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, document := generated(t, input, Paths{})
			if source == "" {
				if len(document) != 0 {
					t.Fatalf("empty source generated overrides: %#v", document)
				}
				return
			}
			defaults := defaultsAt(t, document, "ci01")
			encoded := defaults["svc"].(map[string]interface{})["aks"].(map[string]interface{})["additionalIngressRules"].(string)
			want := "allow-istio-ingress=1000:Tcp:" + source + ":*:443"
			if encoded != want {
				t.Fatalf("ingress rules = %q, want %q", encoded, want)
			}
		})
	}
}

func TestInvalidCIRPAllowedSource(t *testing.T) {
	for _, source := range []string{"not-an-ip", "192.0.2.1/32", "192.0.2.1 192.0.2.2", " 192.0.2.1", "192.0.2.1\n", "192.000.2.1", "256.0.0.1", "2001:db8::1", "::ffff:192.0.2.1"} {
		t.Run(source, func(t *testing.T) {
			input, err := InputsFromEnvironment([]string{"DEPLOY_ENV=ci01", "CI_RP_ALLOWED_SOURCE=" + source})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []Inputs{input, {DeployEnvironment: "ci01", CIRPAllowedSource: source}} {
				result, err := Generate(candidate, Paths{})
				if err == nil || !strings.Contains(err.Error(), "CI_RP_ALLOWED_SOURCE") {
					t.Fatalf("Generate = %#v, %v; want invalid IPv4 source error", result, err)
				}
				if result.YAML != nil || result.OCLoginRegistries != "" {
					t.Fatalf("invalid source returned partial output: %#v", result)
				}
			}
		})
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
	result, document := generated(t, Inputs{DeployEnvironment: "ci01", Images: map[string]string{"BACKEND_IMAGE": "registry/repo@sha256:123"}}, Paths{HypershiftOverlay: fixture(t, "overlay.yaml", overlay)})
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

func TestRunCostTag(t *testing.T) {
	const tagged = "jobID.aro-hcp-ci.redhat.com=1234567"
	for _, tc := range []struct {
		name      string
		input     Inputs
		svc, mgmt string
	}{
		{"sentinel resolves build ID", Inputs{DeployEnvironment: "ci01", RunCostTagValue: "BUILD_ID", BuildID: "1234567", MSIContainers: "leased"},
			"clusterType=svc-cluster," + tagged, "clusterType=mgmt-cluster," + tagged},
		{"explicit value", Inputs{DeployEnvironment: "dev", RunCostTagValue: "nightly-2026.10.28", MSIContainers: "leased"},
			"clusterType=svc-cluster,jobID.aro-hcp-ci.redhat.com=nightly-2026.10.28", "clusterType=mgmt-cluster,jobID.aro-hcp-ci.redhat.com=nightly-2026.10.28"},
		{"no opt-in", Inputs{DeployEnvironment: "ci01", BuildID: "1234567", MSIContainers: "leased"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, document := generated(t, tc.input, Paths{})
			if tc.svc == "" {
				if len(document) != 0 {
					t.Fatalf("overrides generated without opt-in: %#v", document)
				}
				return
			}
			defaults := defaultsAt(t, document, tc.input.DeployEnvironment)
			for _, kind := range []struct{ name, want string }{{"svc", tc.svc}, {"mgmt", tc.mgmt}} {
				var got interface{}
				if cluster, ok := defaults[kind.name].(map[string]interface{}); ok {
					if aks, ok := cluster["aks"].(map[string]interface{}); ok {
						got = aks["tags"]
					}
				}
				if got != kind.want {
					t.Fatalf("%s aks tags = %#v, want %q", kind.name, got, kind.want)
				}
			}
		})
	}
	// A per-run management environment also sized for healthchecks keeps both
	// overrides.
	t.Run("merges with healthcheck sizing", func(t *testing.T) {
		_, document := generated(t, Inputs{DeployEnvironment: "ci01", RunCostTagValue: "BUILD_ID", BuildID: "1234567"}, Paths{})
		defaults := defaultsAt(t, document, "ci01")
		mgmt := defaults["mgmt"].(map[string]interface{})["aks"].(map[string]interface{})
		if got := mgmt["tags"]; got != "clusterType=mgmt-cluster,"+tagged {
			t.Fatalf("mgmt aks tags = %#v", got)
		}
		if got := mgmt["userAgentPool"].(map[string]interface{})["minCount"]; got != 1 {
			t.Fatalf("healthcheck sizing lost: %#v", got)
		}
	})
}

func TestRunCostTagErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  Inputs
		errMsg string
	}{
		{"sentinel without build ID", Inputs{DeployEnvironment: "ci01", RunCostTagValue: "BUILD_ID"}, "BUILD_ID is not set"},
		{"invalid build ID", Inputs{DeployEnvironment: "ci01", RunCostTagValue: "BUILD_ID", BuildID: "$(injection)"}, "not a valid"},
		{"invalid value", Inputs{DeployEnvironment: "ci01", RunCostTagValue: "has space"}, "not a valid"},
		{"injected value", Inputs{DeployEnvironment: "ci01", RunCostTagValue: "semi;colon"}, "not a valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Generate(tc.input, Paths{})
			if err == nil || !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatalf("Generate = %#v, %v; want %q error", result, err, tc.errMsg)
			}
			if result.YAML != nil {
				t.Fatalf("partial result on failure: %#v", result)
			}
		})
	}
	if input, err := InputsFromEnvironment([]string{"DEPLOY_ENV=ci01", "BUILD_ID=1234567", "RUN_COST_TAG_VALUE=BUILD_ID"}); err != nil ||
		input.BuildID != "1234567" || input.RunCostTagValue != "BUILD_ID" {
		t.Fatalf("InputsFromEnvironment = %#v, %v; want BUILD_ID and RUN_COST_TAG_VALUE captured", input, err)
	}
}
