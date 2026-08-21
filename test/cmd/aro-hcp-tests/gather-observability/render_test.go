// Copyright 2025 Microsoft Corporation
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

package gatherobservability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
)

// TestRenderTemplate_Smoke exercises the embedded alerts.html.tmpl end to
// end (including the category badge and filter additions from ARO-28187) to
// catch template syntax errors that go build/vet cannot see, since
// html/template templates are only parsed at runtime.
func TestRenderTemplate_Smoke(t *testing.T) {
	t.Parallel()

	alerts := []alert{
		{
			Alert: alertData{
				Name: "KubePodNotReady", Severity: armalertsmanagement.SeveritySev2, Condition: "Fired",
				StartsAt: mustTime("2026-04-13T06:00:00Z"), EndsAt: mustTime("2026-04-13T06:10:00Z"),
				Labels: map[string]string{"alertname": "KubePodNotReady", "namespace": "ocm-arohcpdev-abc123-primary"},
			},
			Metadata: alertMetadata{Category: "customer-visible-cluster-outage", CategoryTier: 4, CategoryPolicy: policyFail, MonitoringWorkspaceType: "hcp"},
		},
		{
			Alert: alertData{
				Name: "OrphanedMRGDetected", Severity: armalertsmanagement.SeveritySev4, Condition: "Fired",
				StartsAt: mustTime("2026-04-13T06:05:00Z"),
			},
			Metadata: alertMetadata{Category: "expected-noise-orphaned-mrg", CategoryTier: 0, CategoryPolicy: policyIgnore, CategoryReason: "expected", KnownIssue: true, KnownIssueReason: "expected", MonitoringWorkspaceType: "svc"},
		},
		{
			// No category matched: exercises the "uncategorized" display path.
			Alert: alertData{
				Name: "SomethingUnclassified", Severity: armalertsmanagement.SeveritySev3, Condition: "Fired",
				StartsAt: mustTime("2026-04-13T06:00:00Z"),
			},
		},
	}

	filterKeys, filterOptions := collectFilterOptions(alerts)
	output := alertsOutput{
		Alerts: alerts,
		Summary: alertsSummary{
			Total:      len(alerts),
			Known:      1,
			Unknown:    2,
			BySeverity: map[armalertsmanagement.Severity]int{armalertsmanagement.SeveritySev2: 1, armalertsmanagement.SeveritySev3: 1, armalertsmanagement.SeveritySev4: 1},
			ByCategory: map[string]int{"customer-visible-cluster-outage": 1, "expected-noise-orphaned-mrg": 1, "uncategorized": 1},
		},
		FilterKeys:    filterKeys,
		FilterOptions: filterOptions,
	}
	output.TimeWindow.Start = "2026-04-13T05:00:00Z"
	output.TimeWindow.End = "2026-04-13T08:00:00Z"

	outputPath := filepath.Join(t.TempDir(), "alerts-summary.html")
	if err := renderTemplate(outputPath, output); err != nil {
		t.Fatalf("renderTemplate() failed: %v", err)
	}

	html, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read rendered output: %v", err)
	}
	for _, want := range []string{
		"customer-visible-cluster-outage (tier 4)",
		"expected-noise-orphaned-mrg (tier 0)",
		"uncategorized",
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("rendered HTML missing expected category badge text %q", want)
		}
	}
}
