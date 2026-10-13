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
	"os"
	"regexp"
	"testing"
)

// Run with: go test system_pool_only_test.go
// These source checks intentionally need neither Azure access nor a Bicep compiler.
func TestSystemPoolOnlyBicepStructure(t *testing.T) {
	for _, test := range []struct {
		file     string
		patterns []string
	}{
		{
			file: "modules/aks-cluster-base.bicep",
			patterns: []string{
				`(?m)^param systemPoolOnly bool = false\s*$`,
				`module userAgentPools '[^']*/aks/pool\.bicep' = if \(!systemPoolOnly\) \{`,
				`module infraAgentPools '[^']*/aks/pool\.bicep' = if \(!systemPoolOnly\) \{`,
				`nodeTaints:\s*systemPoolOnly\s*\?\s*\[\s*\]\s*:\s*\[\s*'CriticalAddonsOnly=true:NoSchedule'\s*\]`,
			},
		},
		{
			file: "templates/svc-cluster.bicep",
			patterns: []string{
				`(?m)^param systemPoolOnly bool = false\s*$`,
				`(?s)module svcCluster '../modules/aks-cluster-base\.bicep' = \{.*?params: \{[^}]*\bsystemPoolOnly: systemPoolOnly\b`,
			},
		},
	} {
		t.Run(test.file, func(t *testing.T) {
			source, err := os.ReadFile(test.file)
			if err != nil {
				t.Fatal(err)
			}
			for _, pattern := range test.patterns {
				if !regexp.MustCompile(pattern).Match(source) {
					t.Errorf("missing system-pool-only Bicep structure matching %q", pattern)
				}
			}
		})
	}
}
