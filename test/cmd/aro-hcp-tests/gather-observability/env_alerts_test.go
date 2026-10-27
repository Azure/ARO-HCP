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
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestKQLStringList(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		values []string
		want   string
	}{
		{name: "empty", values: nil, want: "()"},
		{name: "single", values: []string{"rg-1"}, want: "('rg-1')"},
		{name: "multiple", values: []string{"b", "a"}, want: "('b','a')"},
		{name: "escapes quotes and backslashes", values: []string{`o'b\rien`}, want: `('o\'b\\rien')`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := kqlStringList(tt.values); got != tt.want {
				t.Fatalf("kqlStringList(%q) = %s, want %s", tt.values, got, tt.want)
			}
		})
	}
}

func TestEnvQueries(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 11, 39, 36, 0, time.UTC)
	end := time.Date(2026, 10, 27, 13, 7, 21, 0, time.UTC)

	t.Run("cluster names", func(t *testing.T) {
		t.Parallel()
		q := envClusterNamesQuery([]string{"rg-1", "rg-2"}, start, end)
		for _, want := range []string{
			"frontendLogs",
			"datetime('2026-10-27T11:39:36Z') .. datetime('2026-10-27T13:07:21Z')",
			"resource_group in ('rg-1','rg-2')",
			`extract(@"hcpopenshiftclusters/([a-z0-9-]+)", 1, tostring(request_path))`,
			"distinct cluster_name, resource_group",
		} {
			if !strings.Contains(q, want) {
				t.Errorf("cluster names query missing %q:\n%s", want, q)
			}
		}
	})

	t.Run("cluster cids", func(t *testing.T) {
		t.Parallel()
		q := envClusterCIDsQuery([]string{"rg-1"}, []string{"cluster-a"}, start, end)
		for _, want := range []string{
			"clustersServiceLogs",
			"isnotempty(cid)",
			"rg in ('rg-1')",
			"cn in ('cluster-a')",
			"distinct cn, tostring(cid)",
		} {
			if !strings.Contains(q, want) {
				t.Errorf("cids query missing %q:\n%s", want, q)
			}
		}
	})

	t.Run("alerts", func(t *testing.T) {
		t.Parallel()
		q := envAlertsQuery([]string{"cid123"}, start, end)
		for _, want := range []string{
			"alertEvents",
			"datetime('2026-10-27T13:07:21Z') + 15m",
			"ns has_any ('cid123')",
			"tostring(alertContext.labels.alertname)",
			"column_ifexists('severity', '')",
			"column_ifexists('monitorCondition', '')",
			"format_datetime(firedDateTime, \"yyyy-MM-ddTHH:mm:ssZ\")",
		} {
			if !strings.Contains(q, want) {
				t.Errorf("alerts query missing %q:\n%s", want, q)
			}
		}
	})
}

func TestEnvAlertsFromRows(t *testing.T) {
	t.Parallel()
	rows := []kustoRow{
		{
			"alertName":       "KubePodNotReady",
			"severity":        "Sev3",
			"condition":       "Fired",
			"signalType":      "Metric",
			"alertRule":       "svc-rules/KubePodNotReady",
			"targetResourceId": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Monitor/accounts/amw",
			"fired":           "2026-10-27T11:40:00Z",
			"resolved":        "2026-10-27T11:45:00Z",
			"labels":          `{"alertname":"KubePodNotReady","namespace":"ocm-arohcp-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-x","pod":"hcco-abc","value":42}`,
			"expression":      "kube_pod_status_ready",
		},
		{
			"alertName": "WeirdAlert",
			// unknown severity must fall back to Sev4
			"severity":  "Sev9",
			"condition": "",
			"fired":     "not-a-time",
			"resolved":  "",
			"labels":    "not-json",
		},
	}
	alerts := envAlertsFromRows(rows)
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want 2", len(alerts))
	}
	// sortAlerts places the alert without a parseable start time first.
	var a, b alert
	for _, candidate := range alerts {
		switch candidate.Alert.Name {
		case "KubePodNotReady":
			a = candidate
		case "WeirdAlert":
			b = candidate
		}
	}
	if a.Alert.Name == "" || b.Alert.Name == "" {
		t.Fatalf("expected both test alerts, got %+v", alerts)
	}
	if a.Alert.Name != "KubePodNotReady" {
		t.Errorf("name = %q", a.Alert.Name)
	}
	if a.Alert.Severity != "Sev3" {
		t.Errorf("severity = %q, want Sev3", a.Alert.Severity)
	}
	if a.Alert.Condition != "Fired" {
		t.Errorf("condition = %q", a.Alert.Condition)
	}
	if a.Alert.StartsAt == nil || a.Alert.StartsAt.UTC().Format(time.RFC3339) != "2026-10-27T11:40:00Z" {
		t.Errorf("startsAt = %v", a.Alert.StartsAt)
	}
	if a.Alert.EndsAt == nil || a.Alert.EndsAt.UTC().Format(time.RFC3339) != "2026-10-27T11:45:00Z" {
		t.Errorf("endsAt = %v", a.Alert.EndsAt)
	}
	if a.Alert.Labels["pod"] != "hcco-abc" {
		t.Errorf("labels[pod] = %q", a.Alert.Labels["pod"])
	}
	if a.Alert.Labels["value"] != "42" {
		t.Errorf("labels[value] = %q, want stringified 42", a.Alert.Labels["value"])
	}
	if a.Metadata.MonitoringWorkspaceType != workspaceEnv {
		t.Errorf("workspace type = %q", a.Metadata.MonitoringWorkspaceType)
	}


	if b.Alert.Severity != "Sev4" {
		t.Errorf("unknown severity = %q, want Sev4 fallback", b.Alert.Severity)
	}
	if b.Alert.StartsAt != nil {
		t.Errorf("unparseable fired time must yield nil StartsAt, got %v", b.Alert.StartsAt)
	}
	if b.Alert.EndsAt != nil {
		t.Errorf("empty resolved time must yield nil EndsAt, got %v", b.Alert.EndsAt)
	}
	if b.Alert.Labels != nil {
		t.Errorf("unparseable labels must yield nil, got %v", b.Alert.Labels)
	}
}

func TestCollectEnvResourceGroups(t *testing.T) {
	t.Parallel()
	got := collectEnvResourceGroups(map[string]timing.TimingInfo{
		"test-b": {ResourceGroupNames: []string{"rg-2", "rg-1"}},
		"test-a": {ResourceGroupNames: []string{"rg-1", "rg-3"}},
		"test-c": {},
	})
	want := []string{"rg-1", "rg-2", "rg-3"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// fakeKustoQuerier stands in for the environment Kusto clusters.
type fakeKustoQuerier struct {
	handler func(db, query string) ([]kustoRow, error)
	queries []string
	closed  bool
}

func (f *fakeKustoQuerier) Query(_ context.Context, db, query string) ([]kustoRow, error) {
	f.queries = append(f.queries, db+"|"+query)
	return f.handler(db, query)
}

func (f *fakeKustoQuerier) Close() error {
	f.closed = true
	return nil
}

func TestRunEnv(t *testing.T) {
	t.Parallel()

	newOptions := func(t *testing.T, querier kustoQuerier) Options {
		t.Helper()
		return Options{completedOptions: &completedOptions{
			PersistentEnv:     true,
			OutputDir:         t.TempDir(),
			SeverityThreshold: -1,
			TimeWindow: timing.TimeWindow{
				Start: time.Date(2026, 10, 27, 11, 39, 36, 0, time.UTC),
				End:   time.Date(2026, 10, 27, 13, 7, 21, 0, time.UTC),
			},
			testTimingInfo: map[string]timing.TimingInfo{
				"test-a": {ResourceGroupNames: []string{"rg-1"}},
			},
			envKusto: envKustoConfig{
				querier:      querier,
				endpoint:     "https://hcp-int-us.westus3.kusto.windows.net",
				serviceDB:    "ServiceLogs",
				monitoringDB: "MonitoringEvents",
			},
		}}
	}

	// chainQuerier answers the three hops of the attribution chain.
	chainQuerier := func(t *testing.T, cidRows, alertRows []kustoRow, alertErr error) *fakeKustoQuerier {
		t.Helper()
		return &fakeKustoQuerier{handler: func(db, query string) ([]kustoRow, error) {
			switch {
			case db == "ServiceLogs" && strings.Contains(query, "frontendLogs"):
				return []kustoRow{{"cluster_name": "cluster-a", "resource_group": "rg-1"}}, nil
			case db == "ServiceLogs" && strings.Contains(query, "clustersServiceLogs"):
				return cidRows, nil
			case db == "MonitoringEvents" && strings.Contains(query, "alertEvents"):
				if alertErr != nil {
					return nil, alertErr
				}
				return alertRows, nil
			}
			t.Errorf("unexpected query: db=%s query=%s", db, query)
			return nil, nil
		}}
	}

	t.Run("unknown alert fires gates the step", func(t *testing.T) {
		t.Parallel()
		querier := chainQuerier(t,
			[]kustoRow{{"cn": "cluster-a", "cid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			[]kustoRow{{
				"alertName":  "KubePodNotReady",
				"severity":   "Sev3",
				"condition":  "Fired",
				"fired":      "2026-10-27T11:40:00Z",
				"resolved":   "",
				"labels":     `{"alertname":"KubePodNotReady","namespace":"ocm-arohcp-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-cluster-a"}`,
				"expression": "kube_pod_status_ready",
			}}, nil)
		o := newOptions(t, querier)
		ctx := logr.NewContext(t.Context(), logr.Discard())

		if err := o.runEnv(ctx, o.dependencies()); err == nil {
			t.Fatal("expected the step to fail on an un-annotated fired alert")
		}

		// run-clusters.json must carry the full attribution chain including
		// the namespace resolved from the alert.
		var inventory envClusterInventory
		readJSONArtifact(t, filepath.Join(o.OutputDir, "run-clusters.json"), &inventory)
		if len(inventory.Clusters) != 1 {
			t.Fatalf("inventory clusters = %+v, want 1", inventory.Clusters)
		}
		c := inventory.Clusters[0]
		if c.CID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || c.Namespace != "ocm-arohcp-int-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-cluster-a" {
			t.Errorf("inventory cluster = %+v, want cid and namespace resolved", c)
		}

		var output alertsOutput
		readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
		if output.Summary.Total != 1 || output.Summary.Unknown != 1 {
			t.Errorf("summary = %+v, want 1 unknown alert", output.Summary)
		}
		if len(output.CollectionErrors) != 0 {
			t.Errorf("collection errors = %v, want none", output.CollectionErrors)
		}

		suites := readJUnitArtifact(t, filepath.Join(o.OutputDir, "junit_alerts.xml"))
		if suites.Suites[0].NumFailed != 1 {
			t.Errorf("junit failures = %d, want 1", suites.Suites[0].NumFailed)
		}
		if _, err := os.Stat(filepath.Join(o.OutputDir, "observability-summary.html")); err != nil {
			t.Errorf("observability-summary.html not written: %v", err)
		}
		if !querier.closed {
			t.Error("kusto querier was not closed")
		}
	})

	t.Run("known alert does not gate", func(t *testing.T) {
		t.Parallel()
		knownIssues, err := parseKnownIssues([]byte("knownIssues:\n- name: KubePodNotReady\n  reason: tracked noise\n"))
		if err != nil {
			t.Fatal(err)
		}
		querier := chainQuerier(t,
			[]kustoRow{{"cn": "cluster-a", "cid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			[]kustoRow{{
				"alertName": "KubePodNotReady",
				"severity":  "Sev3",
				"condition": "Fired",
				"fired":     "2026-10-27T11:40:00Z",
				"resolved":  "",
			}}, nil)
		o := newOptions(t, querier)
		o.knownIssues = knownIssues

		if err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies()); err != nil {
			t.Fatalf("expected the step to pass with a known-issue alert, got %v", err)
		}
		suites := readJUnitArtifact(t, filepath.Join(o.OutputDir, "junit_alerts.xml"))
		if suites.Suites[0].NumFailed != 0 || suites.Suites[0].NumSkipped != 1 {
			t.Errorf("junit failed=%d skipped=%d, want 0/1", suites.Suites[0].NumFailed, suites.Suites[0].NumSkipped)
		}
	})

	t.Run("no alerts", func(t *testing.T) {
		t.Parallel()
		querier := chainQuerier(t,
			[]kustoRow{{"cn": "cluster-a", "cid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			nil, nil)
		o := newOptions(t, querier)

		if err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies()); err != nil {
			t.Fatalf("expected the step to pass with no alerts, got %v", err)
		}
		suites := readJUnitArtifact(t, filepath.Join(o.OutputDir, "junit_alerts.xml"))
		if suites.Suites[0].NumFailed != 0 {
			t.Errorf("junit failures = %d, want 0", suites.Suites[0].NumFailed)
		}
	})

	t.Run("kusto failure degrades gracefully", func(t *testing.T) {
		t.Parallel()
		querier := &fakeKustoQuerier{handler: func(_, _ string) ([]kustoRow, error) {
			return nil, errors.New("connection refused")
		}}
		o := newOptions(t, querier)

		// The step must NOT fail on the unreachable environment Kusto.
		if err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies()); err != nil {
			t.Fatalf("expected graceful degradation, got %v", err)
		}

		var output alertsOutput
		readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
		if len(output.CollectionErrors) != 1 || !strings.Contains(output.CollectionErrors[0], "connection refused") {
			t.Errorf("collection errors = %v, want the kusto failure recorded", output.CollectionErrors)
		}

		// The inventory artifact is still written (empty).
		var inventory envClusterInventory
		readJSONArtifact(t, filepath.Join(o.OutputDir, "run-clusters.json"), &inventory)
		if len(inventory.Clusters) != 0 {
			t.Errorf("inventory clusters = %+v, want empty", inventory.Clusters)
		}

		suites := readJUnitArtifact(t, filepath.Join(o.OutputDir, "junit_alerts.xml"))
		if suites.Suites[0].NumFailed != 1 {
			t.Errorf("junit failures = %d, want the collection-completeness failure recorded", suites.Suites[0].NumFailed)
		}
	})

	t.Run("kusto unavailable from setup", func(t *testing.T) {
		t.Parallel()
		o := newOptions(t, nil)
		o.envKusto = envKustoConfig{}
		o.envKustoError = errors.New("failed to read rendered config")

		if err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies()); err != nil {
			t.Fatalf("expected graceful degradation, got %v", err)
		}
		var output alertsOutput
		readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
		if len(output.CollectionErrors) != 1 || !strings.Contains(output.CollectionErrors[0], "environment Kusto unavailable") {
			t.Errorf("collection errors = %v, want the setup failure recorded", output.CollectionErrors)
		}
	})

	t.Run("no resource groups in timing input", func(t *testing.T) {
		t.Parallel()
		o := newOptions(t, nil)
		o.envKusto = envKustoConfig{}
		o.testTimingInfo = nil

		if err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies()); err != nil {
			t.Fatalf("expected graceful degradation, got %v", err)
		}
		var output alertsOutput
		readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
		if len(output.CollectionErrors) != 1 || !strings.Contains(output.CollectionErrors[0], "no test resource groups") {
			t.Errorf("collection errors = %v, want the missing timing input recorded", output.CollectionErrors)
		}
	})

	t.Run("no clusters found is a collection error", func(t *testing.T) {
		t.Parallel()
		querier := &fakeKustoQuerier{handler: func(_, _ string) ([]kustoRow, error) {
			return nil, nil
		}}
		o := newOptions(t, querier)

		if err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies()); err != nil {
			t.Fatalf("expected graceful degradation, got %v", err)
		}
		var output alertsOutput
		readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
		if len(output.CollectionErrors) != 1 || !strings.Contains(output.CollectionErrors[0], "no HCP clusters found") {
			t.Errorf("collection errors = %v, want the empty chain recorded", output.CollectionErrors)
		}
	})
}

func readJSONArtifact(t *testing.T, path string, dest any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	if err := json.Unmarshal(content, dest); err != nil {
		t.Fatalf("failed to unmarshal %s: %v", path, err)
	}
}

func readJUnitArtifact(t *testing.T, path string) *junit.TestSuites {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	var suites junit.TestSuites
	if err := xml.Unmarshal(content, &suites); err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	return &suites
}
