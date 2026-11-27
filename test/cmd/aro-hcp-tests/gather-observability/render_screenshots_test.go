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

package gatherobservability

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRenderAlertsScreenshots renders the Azure Monitor alerts page from a
// representative fixture and captures a headless-Chrome screenshot, per the
// CONTRIBUTING.md requirement for before/after screenshots of user-visible UI
// changes. It only runs on request:
//
//	GATHER_ALERTS_SCREENSHOTS_DIR=/tmp/shots go test -run TestRenderAlertsScreenshots ./...
//
// and writes alerts.html (to re-render or inspect) alongside alerts.png.
// Metadata is set directly rather than via classifyAlerts so the fixture also
// renders against trees that predate the classifier changes.
func TestRenderAlertsScreenshots(t *testing.T) {
	dir := os.Getenv("GATHER_ALERTS_SCREENSHOTS_DIR")
	if dir == "" {
		t.Skip("GATHER_ALERTS_SCREENSHOTS_DIR not set; skipping screenshot generation")
	}
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	data := alertsOutput{
		TimeWindow: timeWindow{Start: "2026-04-13T06:00:00Z", End: "2026-04-13T08:00:00Z"},
		Summary:    alertsSummary{Total: 3, Known: 1, Unknown: 2},
		Alerts: []alert{
			{
				// Duration-bounded known issue: resolved within the 15m bound.
				Alert: alertData{
					Name: "KubePodNotReady", Severity: "Sev3", Condition: "Resolved",
					StartsAt:    mustTime("2026-04-13T07:00:00Z"),
					EndsAt:      mustTime("2026-04-13T07:12:34Z"),
					Labels:      map[string]string{"namespace": "ocm-arohcpci01-example", "pod": "etcd-0"},
					Description: "Pod has been in a non-ready state for more than 15 minutes.",
				},
				Metadata: alertMetadata{
					KnownIssue: true, KnownIssueReason: "Control-plane operand pods are transiently not-ready during HCP bootstrap",
					MonitoringWorkspace: "example-hcp", MonitoringWorkspaceType: "hcp",
				},
			},
			{
				// Same alert still firing: must NOT be suppressed by the bound.
				Alert: alertData{
					Name: "KubePodNotReady", Severity: "Sev3", Condition: "Fired",
					StartsAt:    mustTime("2026-04-13T07:05:00Z"),
					Labels:      map[string]string{"namespace": "ocm-arohcpci02-example", "pod": "kube-apiserver-1"},
					Description: "Pod has been in a non-ready state for more than 15 minutes.",
				},
				Metadata: alertMetadata{MonitoringWorkspace: "example-hcp", MonitoringWorkspaceType: "hcp"},
			},
			{
				// Unrelated unknown alert with annotations.
				Alert: alertData{
					Name: "TargetDown", Severity: "Sev2", Condition: "Resolved",
					StartsAt: mustTime("2026-04-13T07:00:00Z"), EndsAt: mustTime("2026-04-13T07:12:34Z"),
					Labels:      map[string]string{"namespace": "aro-hcp"},
					Description: "Azure alert description",
					Expression:  `sum(up{namespace="aro-hcp"}) < 1`,
					Annotations: map[string]string{
						"description": "Targets unavailable",
						"summary":     "Check targets & pods",
						"runbook_url": "https://example.com/runbook?alert=down&env=test",
					},
				},
				Metadata: alertMetadata{MonitoringWorkspace: "example-svc", MonitoringWorkspaceType: "svc"},
			},
		},
	}

	html, err := renderAlertsHTML(data)
	if err != nil {
		t.Fatalf("renderAlertsHTML: %v", err)
	}
	page := filepath.Join(dir, "alerts.html")
	if err := os.WriteFile(page, html, 0o600); err != nil {
		t.Fatal(err)
	}

	shot := filepath.Join(dir, "alerts.png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser,
		"--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
		"--disable-background-networking", "--no-first-run", "--hide-scrollbars",
		"--virtual-time-budget=5000", "--window-size=1440,1200",
		"--user-data-dir="+filepath.Join(dir, "chrome-profile"),
		"--screenshot="+shot, "file://"+page)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("screenshot: %v\n%s", err, stderr.String())
	}
	if _, err := os.Stat(shot); err != nil {
		t.Fatalf("screenshot not written: %v\n%s", err, stderr.String())
	}
	t.Logf("wrote %s and %s", page, shot)
}
