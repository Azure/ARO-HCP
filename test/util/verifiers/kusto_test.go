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

package verifiers

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
)

func TestGatheredLogSources(t *testing.T) {
	for _, tt := range []struct {
		name       string
		sources    []any
		missing    string
		counts     string
		containers bool
	}{
		{
			name:       "all arms present",
			sources:    []any{"maestro", "maestro", "hypershift", "acm-agent"},
			containers: true,
		},
		{
			name:       "missing maestro",
			sources:    []any{"hypershift", "acm-agent"},
			missing:    "[maestro]",
			counts:     "maestro=0, hypershift=1, acm-agent=1",
			containers: true,
		},
		{
			name:       "missing hypershift",
			sources:    []any{"maestro", "maestro", "acm-agent"},
			missing:    "[hypershift]",
			counts:     "maestro=2, hypershift=0, acm-agent=1",
			containers: true,
		},
		{
			name:       "missing acm agent",
			sources:    []any{"maestro", "hypershift"},
			missing:    "[acm-agent]",
			counts:     "maestro=1, hypershift=1, acm-agent=0",
			containers: true,
		},
		{
			name:       "all arms absent",
			missing:    "[maestro hypershift acm-agent]",
			counts:     "maestro=0, hypershift=0, acm-agent=0",
			containers: true,
		},
		{
			name:    "all arms present does not replace container checks",
			sources: []any{"maestro", "hypershift", "acm-agent"},
			counts:  "maestro=1, hypershift=1, acm-agent=1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var found gatheredLogSources
			for _, source := range []string{"maestro", "hypershift", "acm-agent"} {
				err := found.record(&mustgather.NormalizedLogLine{
					QueryName: "otherQuery",
					Log:       map[string]any{"source": source, "rows": "100"},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, source := range tt.sources {
				err := found.record(&mustgather.NormalizedLogLine{
					QueryName: "detailedInfraOrchestrationLogs",
					Log:       map[string]any{"source": source, "rows": "1"},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			expected := VerifyMustGatherLogs("subscription", "resource-group").config.ExpectedLogSources
			if tt.containers {
				for _, source := range expected {
					namespace := source.Namespace
					if namespace == "" {
						namespace = source.NamespacePrefix + "-cluster"
					}
					err := found.record(&mustgather.NormalizedLogLine{
						Namespace:     namespace,
						ContainerName: source.ContainerName,
					})
					if err != nil {
						t.Fatal(err)
					}
				}
			}

			err := found.verify(expected)
			if tt.missing == "" && tt.containers {
				if err != nil {
					t.Fatalf("expected all log sources to pass verification: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected missing log sources to fail verification")
			}
			for _, want := range []string{"detailedInfraOrchestrationLogs expected at least one row per source", "observed counts: " + tt.counts} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected failure to contain %q, got: %v", want, err)
				}
			}
			if tt.missing != "" && !strings.Contains(err.Error(), "missing: "+tt.missing) {
				t.Errorf("expected missing orchestration sources %s, got: %v", tt.missing, err)
			}
			if !tt.containers {
				for _, source := range expected {
					if !strings.Contains(err.Error(), source.ContainerName) {
						t.Errorf("expected missing container %q, got: %v", source.ContainerName, err)
					}
				}
			}
		})
	}
}

func TestOrchestrationSummaryCounts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  any
		rows    any
		want    int64
		invalid bool
	}{
		{name: "uses aggregate count", source: "maestro", rows: "1599", want: 1599},
		{name: "zero is not presence", source: "maestro", rows: "0"},
		{name: "raw log is not summary", source: "maestro", invalid: true},
		{name: "malformed count", source: "maestro", rows: "bad", invalid: true},
		{name: "negative count", source: "maestro", rows: "-1", invalid: true},
		{name: "missing source", rows: "1", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var found gatheredLogSources
			err := found.record(&mustgather.NormalizedLogLine{
				QueryName: "detailedInfraOrchestrationLogs",
				Log:       map[string]any{"source": tc.source, "rows": tc.rows},
			})
			if (err != nil) != tc.invalid {
				t.Fatalf("invalid=%t, got error %v", tc.invalid, err)
			}
			if got := found.orchestrationCounts["maestro"]; got != tc.want {
				t.Fatalf("expected aggregate count %d, got %d", tc.want, got)
			}
			if !tc.invalid && tc.want == 0 {
				if err := found.verify(nil); err == nil || !strings.Contains(err.Error(), "missing: [maestro hypershift acm-agent]") {
					t.Fatalf("zero-count summary must not satisfy presence: %v", err)
				}
			}
		})
	}
}
