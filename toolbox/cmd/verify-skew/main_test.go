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
	"fmt"
	"strings"
	"testing"
)

func TestCheckSkew(t *testing.T) {
	for _, test := range []struct {
		name           string
		kubectlVersion string
		svcVersion     string
		mgmtVersion    string
		wantError      string
	}{
		{"exact", "v0.36.6", "1.36", "1.36", ""},
		{"one minor newer", "v0.37.1", "1.36", "1.36", ""},
		{"one minor older", "v0.35.8", "1.36", "1.36", ""},
		{"mixed clusters within skew", "v0.36.6", "1.35", "1.37", ""},
		{"patch version", "v0.37.1", "1.36.10", "1.36.1", ""},
		{"svc too old", "v0.37.1", "1.35", "1.36", "svc AKS 1.35"},
		{"mgmt too old", "v0.37.1", "1.36", "1.35", "mgmt AKS 1.35"},
		{"svc too new", "v0.35.8", "1.37", "1.36", "svc AKS 1.37"},
		{"mgmt too new", "v0.35.8", "1.36", "1.37", "mgmt AKS 1.37"},
		{"missing svc version", "v0.37.1", "", "1.36", "svc.aks.kubernetesVersion"},
		{"missing mgmt version", "v0.37.1", "1.36", "", "mgmt.aks.kubernetesVersion"},
		{"invalid svc version", "v0.37.1", "nightly", "1.36", "svc.aks.kubernetesVersion"},
		{"invalid kubectl version", "v1.37.1", "1.36", "1.36", "invalid kubectl module version"},
		{"non-numeric kubectl minor", "v0.latest.1", "1.36", "1.36", "invalid kubectl module version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configData := []byte(fmt.Sprintf("defaults:\n  svc:\n    aks:\n      kubernetesVersion: %q\n  mgmt:\n    aks:\n      kubernetesVersion: %q\n", test.svcVersion, test.mgmtVersion))
			err := checkSkew(configData, test.kubectlVersion)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("checkSkew() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("checkSkew() = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestCheckSkewInvalidYAML(t *testing.T) {
	if err := checkSkew([]byte("defaults: ["), "v0.37.1"); err == nil || !strings.Contains(err.Error(), "failed to parse AKS configuration") {
		t.Fatalf("checkSkew() = %v, want YAML parse error", err)
	}
}
