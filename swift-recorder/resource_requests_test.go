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
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"sigs.k8s.io/yaml"
)

func TestResourceRequests(t *testing.T) {
	raw, err := os.ReadFile("testdata/helmtest_resources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		TestData map[string]any
	}
	if err := yaml.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../config/rendered/dev/dev/westus3.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	recorder := cfg["swiftRecorder"].(map[string]any)
	resources := fixture.TestData["swiftRecorder"].(map[string]any)["k8s"].(map[string]any)["resources"]
	recorder["k8s"].(map[string]any)["resources"] = resources
	recorder["enabled"] = true
	recorder["useMgmtAgentImage"] = false
	recorder["image"].(map[string]any)["digest"] = "sha256:1234567890"
	valuesTemplate, err := template.New("values.yaml").Option("missingkey=error").ParseFiles("values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values bytes.Buffer
	if err := valuesTemplate.Execute(&values, cfg); err != nil {
		t.Fatal(err)
	}
	var renderedValues map[string]any
	if err := yaml.Unmarshal(values.Bytes(), &renderedValues); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resources, renderedValues["resources"]) {
		t.Fatalf("resource config not preserved in Helm values: got %v, want %v", renderedValues["resources"], resources)
	}
	for _, sentinel := range []string{"", "NONE", "unlimited"} {
		name := sentinel
		if name == "" {
			name = "mixed quantities"
		}
		t.Run(name, func(t *testing.T) {
			args := []string{"template", "swift-recorder", "deploy", "--namespace=swift-recorder", "--values=-"}
			if sentinel != "" {
				for _, field := range []string{"requests.cpu", "requests.memory", "limits.cpu", "limits.memory"} {
					args = append(args, "--set-string", "resources."+field+"="+sentinel)
				}
			}
			cmd := exec.CommandContext(t.Context(), "helm", args...)
			cmd.Stdin = bytes.NewReader(values.Bytes())
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("helm: %v: %s", err, output)
			}
			for _, document := range strings.Split(string(output), "\n---") {
				var object struct {
					Kind string
					Spec struct {
						Template struct {
							Spec struct {
								Containers []map[string]any
							}
						}
					}
				}
				if err := yaml.Unmarshal([]byte(document), &object); err != nil {
					t.Fatal(err)
				}
				if object.Kind != "DaemonSet" {
					continue
				}
				if len(object.Spec.Template.Spec.Containers) != 1 {
					t.Fatalf("expected one recorder container, got %v", object.Spec.Template.Spec.Containers)
				}
				got, present := object.Spec.Template.Spec.Containers[0]["resources"]
				if sentinel != "" {
					if present {
						t.Fatalf("all-%s resources must be omitted, got %v", sentinel, got)
					}
				} else {
					want := map[string]any{
						"requests": map[string]any{"memory": "96Mi"},
						"limits":   map[string]any{"cpu": "100m", "memory": "512Mi"},
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("resources = %v, want %v", got, want)
					}
				}
				return
			}
			t.Fatal("rendered manifest has no DaemonSet")
		})
	}
}
