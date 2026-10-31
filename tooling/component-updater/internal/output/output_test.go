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

package output

import (
	"errors"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func TestReportOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name         string
		result       updater.ComponentResult
		want, absent []string
	}{
		{
			name: "paused component",
			result: updater.ComponentResult{
				Name: "istio", Paused: true, Detail: "automatic updates paused in component-updater configuration",
			},
			want:   []string{"Component: istio", "Result: PAUSED", "Detail: automatic updates paused"},
			absent: []string{"Configured version:", "Next candidate:", "Available versions:", "UPDATE AVAILABLE"},
		},
		{
			name:   "no discovered candidate",
			result: updater.ComponentResult{Name: "istio", Current: "asm-1-30"},
			want:   []string{"Next candidate: not identified", "Result: NO CHANGE"},
			absent: []string{"asm-1-31"},
		},
		{
			name: "Istio deferred",
			result: updater.ComponentResult{
				Name: "istio", Current: "asm-1-29", Candidate: "asm-1-30", Compatibility: "AKS 1.35",
				Regions: []providers.RegionResult{{Location: "westus3", Current: "VALID", Candidate: "BLOCKED"}},
			},
			want: []string{"Result: NO CHANGE", "Compatibility constraint: AKS 1.35", "0 ready, 1 blocked"},
		},
		{
			name: "nonregional Grafana proposal",
			result: updater.ComponentResult{
				Name: "grafana", Current: "11", Candidate: "12", Next: "12", Available: []string{"11", "12"},
			},
			want:   []string{"Configured version: 11", "Available versions: 11, 12", "Result: UPDATE AVAILABLE: 11 -> 12"},
			absent: []string{"Compatibility constraint:", "Regions:", "| Location |", "Istio", "AKS"},
		},
		{
			name: "AKS updated",
			result: updater.ComponentResult{
				Name: "aks", Current: "1.34", Candidate: "1.35", Next: "1.35", Updated: true,
				Regions: []providers.RegionResult{{Location: "uksouth", Current: "VALID", Candidate: "READY"}},
			},
			want:   []string{"Result: UPDATED: 1.34 -> 1.35", "1 ready, 0 blocked", "| uksouth |"},
			absent: []string{"check only"},
		},
		{
			name: "Istio discovery failed",
			result: updater.ComponentResult{
				Name: "istio", Current: "asm-1-29", Candidate: "asm-1-30", Error: errors.New("network failure"),
				Regions: []providers.RegionResult{
					{Location: "uksouth", Current: "INVALID", Candidate: "BLOCKED"},
					{Location: "westus3", Current: "ERROR", Candidate: "BLOCKED"},
				},
			},
			want: []string{"Result: FAILED - network failure", "Available versions: unconfirmed", "0 ready, 2 blocked, 1 discovery errors, 1 current invalid"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := FormatMarkdownTable([]updater.ComponentResult{tt.result})
			for _, expected := range tt.want {
				if !strings.Contains(report, expected) {
					t.Fatalf("missing %q in report:\n%s", expected, report)
				}
			}
			for _, unexpected := range tt.absent {
				if strings.Contains(report, unexpected) {
					t.Fatalf("unexpected %q in report:\n%s", unexpected, report)
				}
			}
		})
	}
}
