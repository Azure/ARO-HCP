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

	for _, tt := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "cluster names",
			query: envClusterNamesQuery([]string{"rg-1", "rg-2"}, start, end),
			want: []string{
				"frontendLogs",
				"datetime('2026-10-27T11:39:36Z') .. datetime('2026-10-27T13:07:21Z')",
				"resource_group in ('rg-1','rg-2')",
				`extract(@"hcpopenshiftclusters/([a-z0-9-]+)", 1, tostring(request_path))`,
			},
		},
		{
			name:  "cluster cids",
			query: envClusterCIDsQuery([]string{"rg-1"}, []string{"cluster-a"}, start, end),
			want:  []string{"clustersServiceLogs", "rg in ('rg-1')", "cn in ('cluster-a')", "distinct cid = tostring(cid)"},
		},
		{
			name:  "alerts",
			query: envAlertsQuery([]string{"cid123"}, start, end),
			want: []string{
				"alertEvents",
				"datetime('2026-10-27T13:07:21Z') + 15m",
				"ns has_any ('cid123')",
				// alertEvents has one row per state change; keep the latest.
				"summarize arg_max(lastChange, *) by alertId",
				"condition = monitorCondition",
				"alertRule",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, want := range tt.want {
				if !strings.Contains(tt.query, want) {
					t.Errorf("query missing %q:\n%s", want, tt.query)
				}
			}
			// Every column exists in the alertEvents schema; a renamed column
			// must fail the query loudly instead of silently projecting ''.
			if strings.Contains(tt.query, "column_ifexists") {
				t.Errorf("query must not mask schema drift with column_ifexists:\n%s", tt.query)
			}
		})
	}
}

func TestEnvAlertsFromRows(t *testing.T) {
	t.Parallel()
	alerts := envAlertsFromRows([]kustoRow{
		{
			"alertName":        "KubePodNotReady",
			"severity":         "Sev3",
			"condition":        "Resolved",
			"signalType":       "Metric",
			"alertRule":        "svc-rules/KubePodNotReady",
			"targetResourceId": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Monitor/accounts/amw",
			// Kusto renders datetimes with fractional seconds.
			"fired":      "2026-10-27T11:40:00.1234567Z",
			"resolved":   "2026-10-27T11:45:00Z",
			"labels":     `{"alertname":"KubePodNotReady","pod":"hcco-abc","value":42,"empty":null}`,
			"expression": "kube_pod_status_ready",
		},
		{"alertName": "WeirdAlert", "severity": "Sev9", "fired": "not-a-time", "resolved": "", "labels": "not-json"},
	})
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want 2", len(alerts))
	}
	byName := map[string]alert{}
	for _, a := range alerts {
		byName[a.Alert.Name] = a
	}

	a := byName["KubePodNotReady"]
	if a.Alert.Severity != "Sev3" || a.Alert.Condition != "Resolved" || a.Alert.AlertRule != "svc-rules/KubePodNotReady" {
		t.Errorf("unexpected alert fields: %+v", a.Alert)
	}
	if a.Alert.StartsAt == nil || !a.Alert.StartsAt.Equal(time.Date(2026, 10, 27, 11, 40, 0, 123456700, time.UTC)) {
		t.Errorf("startsAt = %v", a.Alert.StartsAt)
	}
	if a.Alert.EndsAt == nil || !a.Alert.EndsAt.Equal(time.Date(2026, 10, 27, 11, 45, 0, 0, time.UTC)) {
		t.Errorf("endsAt = %v", a.Alert.EndsAt)
	}
	if a.Alert.Labels["pod"] != "hcco-abc" || a.Alert.Labels["value"] != "42" {
		t.Errorf("labels = %v", a.Alert.Labels)
	}
	if _, ok := a.Alert.Labels["empty"]; ok {
		t.Errorf("null label values must be dropped, got %v", a.Alert.Labels)
	}
	if a.Metadata.MonitoringWorkspaceType != workspaceEnv {
		t.Errorf("workspace type = %q", a.Metadata.MonitoringWorkspaceType)
	}

	b := byName["WeirdAlert"]
	if b.Alert.Severity != "Sev4" {
		t.Errorf("unknown severity = %q, want Sev4 fallback", b.Alert.Severity)
	}
	if b.Alert.StartsAt != nil || b.Alert.EndsAt != nil || b.Alert.Labels != nil {
		t.Errorf("unparseable fields must be nil: %+v", b.Alert)
	}
}

func TestCollectEnvResourceGroups(t *testing.T) {
	t.Parallel()
	got := collectEnvResourceGroups(map[string]timing.TimingInfo{
		"test-b": {ResourceGroupNames: []string{"rg-2", "rg-1"}},
		"test-a": {ResourceGroupNames: []string{"rg-1", "rg-3"}},
		"test-c": {},
	})
	if want := []string{"rg-1", "rg-2", "rg-3"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestValidatePersistentEnv(t *testing.T) {
	t.Parallel()
	if _, err := (&RawOptions{PersistentEnv: true, OutputDir: "out", RenderedConfig: "cfg"}).Validate(); err != nil {
		t.Errorf("persistent-env must not require --subscription-id: %v", err)
	}
	if _, err := (&RawOptions{OutputDir: "out", RenderedConfig: "cfg"}).Validate(); err == nil {
		t.Error("dev mode must still require --subscription-id")
	}
	if _, err := (&RawOptions{PersistentEnv: true, AMWOnly: true, OutputDir: "out", RenderedConfig: "cfg"}).Validate(); err == nil {
		t.Error("--amw-only and --persistent-env must be mutually exclusive")
	}
}

func TestCompletePersistentEnvSkipsDevSetup(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "AzureCLICredential")
	dir := t.TempDir()
	// Only kusto.* keys: no regionRG, workspaces or Cosmos config.
	path := filepath.Join(dir, "config.yaml")
	cfg := "kusto:\n  kustoName: hcp-int-us\n  location: westus3\n  serviceLogsDatabase: ServiceLogs\n  monitoringEventsDatabase: MonitoringEvents\n"
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	raw := &RawOptions{PersistentEnv: true, TimingInputDir: dir, OutputDir: dir, RenderedConfig: path, StartTimeFallback: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	validated, err := raw.Validate()
	if err != nil {
		t.Fatal(err)
	}
	o, err := validated.Complete(logr.NewContext(t.Context(), logr.Discard()))
	if err != nil {
		t.Fatalf("persistent-env Complete must not need dev-only config: %v", err)
	}
	if o.envKustoError != nil {
		t.Fatalf("unexpected env Kusto setup error: %v", o.envKustoError)
	}
	if o.envKusto.endpoint != "https://hcp-int-us.westus3.kusto.windows.net" || o.envKusto.serviceDB != "ServiceLogs" || o.envKusto.monitoringDB != "MonitoringEvents" {
		t.Errorf("env Kusto config = %+v", o.envKusto)
	}
	if err := o.envKusto.querier.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// fakeKustoQuerier stands in for the environment Kusto clusters.
type fakeKustoQuerier struct {
	handler func(db, query string) ([]kustoRow, error)
	closed  bool
}

func (f *fakeKustoQuerier) Query(_ context.Context, db, query string) ([]kustoRow, error) {
	return f.handler(db, query)
}

func (f *fakeKustoQuerier) Close() error {
	f.closed = true
	return nil
}

// chainQuerier answers the three hops of the attribution chain.
func chainQuerier(t *testing.T, cidRows, alertRows []kustoRow, alertErr error) *fakeKustoQuerier {
	t.Helper()
	return &fakeKustoQuerier{handler: func(db, query string) ([]kustoRow, error) {
		switch {
		case db == "ServiceLogs" && strings.HasPrefix(query, "frontendLogs"):
			return []kustoRow{{"cluster_name": "cluster-a"}}, nil
		case db == "ServiceLogs" && strings.HasPrefix(query, "clustersServiceLogs"):
			return cidRows, nil
		case db == "MonitoringEvents" && strings.HasPrefix(query, "alertEvents"):
			return alertRows, alertErr
		}
		t.Errorf("unexpected query: db=%s query=%s", db, query)
		return nil, nil
	}}
}

var firedAlertRow = kustoRow{
	"alertName": "KubePodNotReady",
	"severity":  "Sev3",
	"condition": "Fired",
	"fired":     "2026-10-27T11:40:00Z",
	"labels":    `{"alertname":"KubePodNotReady","namespace":"ocm-arohcpint-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-cluster-a"}`,
}

var cidRow = kustoRow{"cid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}

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
			testTimingInfo: map[string]timing.TimingInfo{"test-a": {ResourceGroupNames: []string{"rg-1"}}},
			envKusto: envKustoConfig{
				querier:      querier,
				endpoint:     "https://hcp-int-us.westus3.kusto.windows.net",
				serviceDB:    "ServiceLogs",
				monitoringDB: "MonitoringEvents",
			},
		}}
	}
	knownPodNotReady, err := parseKnownIssues([]byte("knownIssues:\n- name: KubePodNotReady\n  reason: tracked noise\n"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name    string
		mutate  func(*Options)
		querier func(*testing.T) *fakeKustoQuerier
		// wantErr is whether the step fails.
		wantErr bool
		// wantCollectionError is a substring of the recorded collection
		// error; empty means collection must be complete.
		wantCollectionError string
		wantUnknown         int
		wantJUnitFailed     uint
		wantJUnitSkipped    uint
	}{
		{
			name: "un-annotated alert fails the step",
			querier: func(t *testing.T) *fakeKustoQuerier {
				return chainQuerier(t, []kustoRow{cidRow}, []kustoRow{firedAlertRow}, nil)
			},
			wantErr:         true,
			wantUnknown:     1,
			wantJUnitFailed: 1,
		},
		{
			name:   "known-issue alert passes",
			mutate: func(o *Options) { o.knownIssues = knownPodNotReady },
			querier: func(t *testing.T) *fakeKustoQuerier {
				return chainQuerier(t, []kustoRow{cidRow}, []kustoRow{firedAlertRow}, nil)
			},
			wantJUnitSkipped: 1,
		},
		{
			name:    "no alerts passes",
			querier: func(t *testing.T) *fakeKustoQuerier { return chainQuerier(t, []kustoRow{cidRow}, nil, nil) },
		},
		{
			name: "unreachable Kusto degrades",
			querier: func(t *testing.T) *fakeKustoQuerier {
				return &fakeKustoQuerier{handler: func(_, _ string) ([]kustoRow, error) { return nil, errors.New("connection refused") }}
			},
			wantCollectionError: "connection refused",
			wantJUnitFailed:     1,
		},
		{
			name: "alert hop failure degrades",
			querier: func(t *testing.T) *fakeKustoQuerier {
				return chainQuerier(t, []kustoRow{cidRow}, nil, errors.New("throttled"))
			},
			wantCollectionError: "throttled",
			wantJUnitFailed:     1,
		},
		{
			name:                "Kusto setup failure degrades",
			mutate:              func(o *Options) { o.envKusto = envKustoConfig{}; o.envKustoError = errors.New("no kusto.kustoName") },
			wantCollectionError: "environment Kusto unavailable",
			wantJUnitFailed:     1,
		},
		{
			name:                "missing timing input degrades",
			mutate:              func(o *Options) { o.testTimingInfo = nil },
			querier:             func(t *testing.T) *fakeKustoQuerier { return chainQuerier(t, nil, nil, nil) },
			wantCollectionError: "no test resource groups",
			wantJUnitFailed:     1,
		},
		{
			name:                "clusters without cids degrade",
			querier:             func(t *testing.T) *fakeKustoQuerier { return chainQuerier(t, nil, nil, nil) },
			wantCollectionError: "no cids found",
			wantJUnitFailed:     1,
		},
		{
			name:            "broken known-issues file fails the step",
			mutate:          func(o *Options) { o.knownIssuesError = errors.New("bad yaml") },
			querier:         func(t *testing.T) *fakeKustoQuerier { return chainQuerier(t, []kustoRow{cidRow}, nil, nil) },
			wantErr:         true,
			wantJUnitFailed: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var querier *fakeKustoQuerier
			if tt.querier != nil {
				querier = tt.querier(t)
			}
			o := newOptions(t, querier)
			if querier == nil {
				o.envKusto.querier = nil
			}
			if tt.mutate != nil {
				tt.mutate(&o)
			}

			err := o.runEnv(logr.NewContext(t.Context(), logr.Discard()), o.dependencies())
			if (err != nil) != tt.wantErr {
				t.Fatalf("runEnv error = %v, wantErr %v", err, tt.wantErr)
			}

			var output alertsOutput
			readJSONArtifact(t, filepath.Join(o.OutputDir, "alerts.json"), &output)
			if output.Summary.Unknown != tt.wantUnknown {
				t.Errorf("unknown alerts = %d, want %d", output.Summary.Unknown, tt.wantUnknown)
			}
			switch {
			case tt.wantCollectionError == "" && len(output.CollectionErrors) != 0:
				t.Errorf("collection errors = %v, want none", output.CollectionErrors)
			case tt.wantCollectionError != "" && (len(output.CollectionErrors) != 1 || !strings.Contains(output.CollectionErrors[0], tt.wantCollectionError)):
				t.Errorf("collection errors = %v, want %q", output.CollectionErrors, tt.wantCollectionError)
			}

			suites := readJUnitArtifact(t, filepath.Join(o.OutputDir, "junit_alerts.xml"))
			if got := suites.Suites[0]; got.NumFailed != tt.wantJUnitFailed || got.NumSkipped != tt.wantJUnitSkipped {
				t.Errorf("junit failed=%d skipped=%d, want %d/%d", got.NumFailed, got.NumSkipped, tt.wantJUnitFailed, tt.wantJUnitSkipped)
			}
			if _, err := os.Stat(filepath.Join(o.OutputDir, "observability-summary.html")); err != nil {
				t.Errorf("observability-summary.html not written: %v", err)
			}
			if querier != nil && o.envKusto.querier != nil && !querier.closed {
				t.Error("kusto querier was not closed")
			}
		})
	}
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
