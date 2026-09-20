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

package nodemitigation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStrictConfiguration(t *testing.T) {
	valid, err := json.Marshal(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data string
		ok   bool
	}{
		{"empty disables", "", true},
		{"explicit disabled", "mode: disabled", true},
		{"complete enforce", string(valid), true},
		{"unknown field", "mode: disabled\nresuce: true", false},
		{"duplicate key", "mode: disabled\nmode: enforce", false},
		{"unknown mode", "mode: enabled", false},
		{"implicit limits", "mode: enforce", false},
		{"implicit cleanup delay", strings.Replace(string(valid), `"cleanupDelay":"0s",`, "", 1), false},
		{"invalid duration", strings.Replace(string(valid), `"window":"1h0m0s"`, `"window":"invalid"`, 1), false},
		{"oversized", strings.Repeat(" ", 64*1024+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.data))
			if (err == nil) != test.ok {
				t.Fatalf("Parse error %v, want success=%v", err, test.ok)
			}
		})
	}
}

func TestConfigurationReloadAndDeploymentGate(t *testing.T) {
	f := newFixture(t, 1, 0)
	before, revision := f.controller.configuration()
	f.controller.OnConfigMap(&corev1.ConfigMap{Data: map[string]string{"config.yaml": "mode: audit\nunknown: true"}}, "config.yaml")
	after, afterRevision := f.controller.configuration()
	if !reflect.DeepEqual(before, after) || revision != afterRevision {
		t.Fatal("invalid reload replaced the last valid configuration")
	}
	f.controller.OnConfigMap(&corev1.ConfigMap{}, "config.yaml")
	after, afterRevision = f.controller.configuration()
	if after.Mode != Disabled || afterRevision == revision {
		t.Fatal("missing configuration did not disable and fence actions")
	}
	f.controller.AllowConfiguration(false)
	if err := f.controller.SetConfig(before); err == nil {
		t.Fatal("runtime configuration bypassed the deployment gate")
	}
	after, _ = f.controller.configuration()
	if after.Mode != Disabled {
		t.Fatal("rejected configuration enabled mitigation")
	}
}

func TestConfigurationOwnsSelectors(t *testing.T) {
	f := swiftFixture(t)
	before, _ := f.controller.configuration()
	want := before.Workloads[0].NamespaceSelector.MatchLabels["app"]
	f.cfg.Workloads[0].NamespaceSelector.MatchLabels["app"] = "broader"
	f.cfg.Mitigators[0] = "unknown"
	after, _ := f.controller.configuration()
	if after.Workloads[0].NamespaceSelector.MatchLabels["app"] != want || after.Mitigators[0] != "swift" {
		t.Fatal("caller mutated the accepted configuration")
	}
	cfg := testConfig()
	cfg.Workloads = []WorkloadPolicy{{NamespaceSelector: metav1.LabelSelector{}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty workload selectors were accepted")
	}
	cfg = testConfig()
	cfg.Mitigators = []string{"swift", "swift"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate mitigators were accepted")
	}
}
