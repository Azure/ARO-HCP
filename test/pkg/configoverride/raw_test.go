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

package configoverride

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseRawOverrideValues(t *testing.T) {
	tests := []struct {
		entry string
		path  []string
		want  interface{}
	}{
		{"mgmt.aks.userAgentPool.vmSize=Standard_D16ads_v6", []string{"mgmt", "aks", "userAgentPool", "vmSize"}, "Standard_D16ads_v6"},
		{"a.minCount=3", []string{"a", "minCount"}, 3},
		{"a.enabled=true", []string{"a", "enabled"}, true},
		{`a.quoted="3"`, []string{"a", "quoted"}, "3"},
		{`a.empty=""`, []string{"a", "empty"}, ""},
		{"a.cleared=null", []string{"a", "cleared"}, nil},
		{"a.zones=[1, 2, 3]", []string{"a", "zones"}, []interface{}{1, 2, 3}},
		{"a.nested={x: 1}", []string{"a", "nested"}, map[string]interface{}{"x": 1}},
		// Only the first "=" separates path from value: AKS tags contain "=".
		{"  svc.aks.tags=clusterType=svc-cluster,jobID.aro-hcp-ci.redhat.com=42  ", []string{"svc", "aks", "tags"}, "clusterType=svc-cluster,jobID.aro-hcp-ci.redhat.com=42"},
	}
	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			overrides, err := ParseRawOverrides([]string{tt.entry})
			if err != nil {
				t.Fatal(err)
			}
			if len(overrides) != 1 || !reflect.DeepEqual(overrides[0].Path, tt.path) || !reflect.DeepEqual(overrides[0].Value, tt.want) {
				t.Fatalf("got %#v, want path %v value %#v", overrides, tt.path, tt.want)
			}
		})
	}
}

func TestParseRawOverrideErrors(t *testing.T) {
	tests := []struct {
		entry string
		want  string
	}{
		{"no-separator", "path.to.key=value"},
		{"=value", "dot-separated keys"},
		{"a..b=1", "dot-separated keys"},
		{"a.b.=1", "dot-separated keys"},
		{"a b.c=1", "dot-separated keys"},
		{"a.b=", "value is empty"},
		{"a.b=  ", "value is empty"},
		{"a.b=[unclosed", "not valid YAML"},
		{"a.b=1\n---\n2", "not valid YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			_, err := ParseRawOverrides([]string{tt.entry})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRawOverridesFromEnvironment(t *testing.T) {
	// Multi-line step-registry values arrive with blank lines and indentation.
	input, err := InputsFromEnvironment([]string{
		RawConfigOverridesEnv + "=\n  a.b=1\n\nc.d=two\n",
		"DEPLOY_ENV=ci00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(input.RawOverrides) != 2 || input.RawOverrides[0].Entry != "a.b=1" || input.RawOverrides[1].Entry != "c.d=two" {
		t.Fatalf("overrides = %#v", input.RawOverrides)
	}
	if _, err := InputsFromEnvironment([]string{RawConfigOverridesEnv + "=garbage"}); err == nil || !strings.Contains(err.Error(), RawConfigOverridesEnv) {
		t.Fatalf("err = %v, want it to name %s", err, RawConfigOverridesEnv)
	}
}

func TestGenerateRawOverrides(t *testing.T) {
	overrides := func(t *testing.T, entries ...string) []RawOverride {
		t.Helper()
		parsed, err := ParseRawOverrides(entries)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	const defaultsPath = "clouds.dev.environments.ci00.defaults."

	t.Run("merges beside generated healthcheck sizing and other keys", func(t *testing.T) {
		input := Inputs{DeployEnvironment: "ci00", RawOverrides: overrides(t, defaultsPath+"mgmt.aks.userAgentPool.vmSize=Standard_D16ads_v6")}
		_, document := generated(t, input, Paths{})
		pool := defaultsAt(t, document, "ci00")["mgmt"].(map[string]interface{})["aks"].(map[string]interface{})["userAgentPool"]
		want := map[string]interface{}{"minCount": 1, "vmSize": "Standard_D16ads_v6"}
		if !reflect.DeepEqual(pool, want) {
			t.Fatalf("userAgentPool = %#v, want %#v", pool, want)
		}
	})

	t.Run("wins over generated values", func(t *testing.T) {
		input := Inputs{
			DeployEnvironment: "ci00", MSIContainers: "leased", BuildID: "123", RunCostTagValue: "BUILD_ID",
			RawOverrides: overrides(t, defaultsPath+"mgmt.aks.tags=custom"),
		}
		_, document := generated(t, input, Paths{})
		aks := defaultsAt(t, document, "ci00")["mgmt"].(map[string]interface{})["aks"].(map[string]interface{})
		if aks["tags"] != "custom" {
			t.Fatalf("tags = %#v, want custom", aks["tags"])
		}
	})

	t.Run("wins over the Hypershift overlay", func(t *testing.T) {
		overlay := fixture(t, "overlay.yaml", "clouds:\n  dev:\n    environments:\n      ci00:\n        defaults:\n          hypershift:\n            replicas: 1\n")
		input := Inputs{DeployEnvironment: "ci00", MSIContainers: "leased", RawOverrides: overrides(t, defaultsPath+"hypershift.replicas=3")}
		_, document := generated(t, input, Paths{HypershiftOverlay: overlay})
		if got := defaultsAt(t, document, "ci00")["hypershift"]; !reflect.DeepEqual(got, map[string]interface{}{"replicas": 3}) {
			t.Fatalf("hypershift = %#v", got)
		}
	})

	t.Run("targets a region overlay chosen by the author", func(t *testing.T) {
		input := Inputs{DeployEnvironment: "ci00", MSIContainers: "leased", RawOverrides: overrides(t, "clouds.dev.environments.ci00.regions.westus3.mgmt.aks.userAgentPool.vmSize=Standard_D16ads_v6")}
		_, document := generated(t, input, Paths{})
		region := document["clouds"].(map[string]interface{})["dev"].(map[string]interface{})["environments"].(map[string]interface{})["ci00"].(map[string]interface{})["regions"]
		want := map[string]interface{}{"westus3": map[string]interface{}{"mgmt": map[string]interface{}{"aks": map[string]interface{}{"userAgentPool": map[string]interface{}{"vmSize": "Standard_D16ads_v6"}}}}}
		if !reflect.DeepEqual(region, want) {
			t.Fatalf("regions = %#v, want %#v", region, want)
		}
	})

	t.Run("null clears a generated key", func(t *testing.T) {
		input := Inputs{DeployEnvironment: "ci00", RawOverrides: overrides(t, defaultsPath+"mgmt=null")}
		_, document := generated(t, input, Paths{})
		if got, present := defaultsAt(t, document, "ci00")["mgmt"]; !present || got != nil {
			t.Fatalf("mgmt = %#v (present=%v), want explicit null", got, present)
		}
	})

	t.Run("no overrides leaves the document unchanged", func(t *testing.T) {
		with := Inputs{DeployEnvironment: "ci00", MSIContainers: "leased"}
		without := with
		without.RawOverrides = []RawOverride{}
		a, _ := generated(t, with, Paths{})
		b, _ := generated(t, without, Paths{})
		if string(a.YAML) != string(b.YAML) {
			t.Fatalf("YAML differs: %q vs %q", a.YAML, b.YAML)
		}
	})
}

func TestGenerateRawOverrideConflicts(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    string
	}{
		{"duplicate key", []string{"a.b=1", "a.b=2"}, "overlap"},
		{"parent after child", []string{"a.b.c=1", "a.b=2"}, "overlap"},
		{"child after parent", []string{"a.b=2", "a.b.c=1"}, "overlap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overrides, err := ParseRawOverrides(tt.entries)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Generate(Inputs{DeployEnvironment: "ci00", RawOverrides: overrides}, Paths{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || len(result.YAML) != 0 {
				t.Fatalf("result = %q, err = %v, want error containing %q", result.YAML, err, tt.want)
			}
		})
	}

	t.Run("sibling keys do not overlap", func(t *testing.T) {
		overrides, err := ParseRawOverrides([]string{"a.b=1", "a.bc=2", "a.c.d=3"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(Inputs{DeployEnvironment: "ci00", RawOverrides: overrides}, Paths{}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cannot descend into a generated scalar", func(t *testing.T) {
		overrides, err := ParseRawOverrides([]string{"clouds.dev.environments.ci00.defaults.miMockClientId.x=1"})
		if err != nil {
			t.Fatal(err)
		}
		catalog := fixture(t, "msi.yaml", "miMockPool:\n  lease-1:\n    clientId: c\n    principalId: p\n    certName: n\n")
		_, err = Generate(Inputs{DeployEnvironment: "ci00", MSIMockLease: "lease-1", RawOverrides: overrides}, Paths{MSIMockCatalog: catalog})
		if err == nil || !strings.Contains(err.Error(), "not a mapping") {
			t.Fatalf("err = %v, want not-a-mapping error", err)
		}
	})
}
