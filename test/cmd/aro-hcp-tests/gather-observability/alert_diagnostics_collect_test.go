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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/alertdiagnostics"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func diagnosticTestOptions() Options {
	return Options{completedOptions: &completedOptions{
		TimeWindow: timing.TimeWindow{Start: time.Unix(1000, 0), End: time.Unix(2000, 0)},
		cred:       utilizationTestCredential{},
	}}
}

func diagnosticTestAlert(expression string) alert {
	return alert{Alert: alertData{Name: "Down", Expression: expression}, Metadata: alertMetadata{MonitoringWorkspaceType: workspaceSvc}}
}

func diagnosticTestMatrix() *PrometheusResponse {
	return &PrometheusResponse{Status: "success", Data: PrometheusData{ResultType: "matrix", Result: []PrometheusResult{}}}
}

func TestCollectAlertDiagnosticsDeduplication(t *testing.T) {
	t.Parallel()
	o := diagnosticTestOptions()
	const group = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/"
	workspaces := map[string]*workspaceData{
		workspaceSvc: {PromEndpoint: "svc", RuleDefinitions: []alertRuleDefinition{
			{Name: "Down", Expression: "up == 0", Interval: "PT30S", GroupID: group + "fast"},
			{Name: "Down", Expression: "up == 0", Interval: "PT2M", GroupID: group + "slow"},
		}},
		workspaceHcp: {PromEndpoint: "hcp", RuleDefinitions: []alertRuleDefinition{
			{Name: "Down", Expression: "up == 0", Interval: "PT30S"},
		}},
	}
	var alerts []alert
	for i := range 6 {
		a := diagnosticTestAlert("up==0")
		a.Alert.AlertRule = group + "fast"
		start, end := time.Unix(1100+int64(i)*100, 0), time.Unix(1150+int64(i)*100, 0)
		a.Alert.StartsAt, a.Alert.EndsAt = &start, &end
		a.Alert.Labels = map[string]string{"instance": fmt.Sprint(i)}
		a.Metadata.KnownIssue = i%2 == 0
		if i == 4 {
			a.Metadata.MonitoringWorkspaceType = workspaceHcp
			a.Alert.AlertRule = ""
		}
		if i == 5 {
			a.Alert.AlertRule = group + "slow"
		}
		alerts = append(alerts, a)
	}
	before, err := json.Marshal(alerts)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	deps := gatherDependencies{queryRange: func(_ context.Context, client *http.Client, cred azcore.TokenCredential, endpoint, expression string, start, end time.Time, step string) (*PrometheusResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[endpoint+":"+expression+":"+step]++
		if client.Timeout != 30*time.Second || cred != o.cred {
			t.Errorf("query received timeout=%v credential=%v", client.Timeout, cred)
		}
		if !start.Equal(o.TimeWindow.Start) || !end.Equal(o.TimeWindow.End) {
			t.Errorf("query narrowed to firing interval: %v to %v", start, end)
		}
		return diagnosticTestMatrix(), nil
	}}
	report := o.collectAlertDiagnostics(t.Context(), alerts, workspaces, deps, o.TimeWindow.End)
	wantCalls := map[string]int{"svc:up:30s": 1, "hcp:up:30s": 1, "svc:up:120s": 1}
	if !reflect.DeepEqual(calls, wantCalls) || len(report.Queries) != 3 || len(report.Alerts) != len(alerts) {
		t.Fatalf("deduplication: calls=%v queries=%+v alerts=%d", calls, report.Queries, len(report.Alerts))
	}
	for i, entry := range report.Alerts {
		wantIndex := 0
		if i >= 4 {
			wantIndex = i - 3
		}
		if entry.Error != "" || entry.Warning != "" || len(entry.Charts) != 1 {
			t.Fatalf("alert %d lost chart or metadata: %+v", i, entry)
		}
		if want := []alertDiagnosticReference{{Result: wantIndex, Role: "signal"}}; !reflect.DeepEqual(entry.Charts[0].Queries, want) {
			t.Errorf("alert %d references=%+v, want %+v", i, entry.Charts[0].Queries, want)
		}
		query := report.Queries[wantIndex]
		if query.Workspace != alerts[i].Metadata.MonitoringWorkspaceType || query.Expression != "up" {
			t.Errorf("alert %d references wrong query: %+v", i, query)
		}
	}
	after, err := json.Marshal(alerts)
	if err != nil || string(before) != string(after) {
		t.Fatalf("diagnostics mutated alert classification, labels, or firing intervals: %s, %v", after, err)
	}
}

func TestCollectAlertDiagnosticsCharts(t *testing.T) {
	t.Parallel()
	o := diagnosticTestOptions()
	alerts := []alert{diagnosticTestAlert("(up > 1 and errors < 2) or desired != on(namespace) actual"), diagnosticTestAlert("up < 5")}
	deps := gatherDependencies{queryRange: func(context.Context, *http.Client, azcore.TokenCredential, string, string, time.Time, time.Time, string) (*PrometheusResponse, error) {
		return diagnosticTestMatrix(), nil
	}}
	report := o.collectAlertDiagnostics(t.Context(), alerts, map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc"}}, deps, o.TimeWindow.End)
	want := []alertDiagnosticChart{
		{Expression: "up > 1", Path: "root/or:left/and:left", Queries: []alertDiagnosticReference{{Result: 0, Role: "signal"}}, Thresholds: []alertdiagnostics.Threshold{{Operator: ">", Value: 1}}},
		{Expression: "errors < 2", Path: "root/or:left/and:right", Queries: []alertDiagnosticReference{{Result: 1, Role: "signal"}}, Thresholds: []alertdiagnostics.Threshold{{Operator: "<", Value: 2}}},
		{Expression: "desired != on (namespace) actual", Path: "root/or:right", Queries: []alertDiagnosticReference{{Result: 2, Role: "left"}, {Result: 3, Role: "right"}}},
	}
	if !reflect.DeepEqual(report.Alerts[0].Charts, want) {
		t.Errorf("compound charts=%+v, want %+v", report.Alerts[0].Charts, want)
	}
	if len(report.Queries) != 4 {
		t.Fatalf("expected four shared queries, got %+v", report.Queries)
	}
	for i, expression := range []string{"up", "errors", "desired", "actual"} {
		if report.Queries[i].Expression != expression {
			t.Errorf("reference %d resolves to %q, want %q", i, report.Queries[i].Expression, expression)
		}
	}
	if charts := report.Alerts[1].Charts; len(charts) != 1 || !reflect.DeepEqual(charts[0].Queries, []alertDiagnosticReference{{Result: 0, Role: "signal"}}) || !reflect.DeepEqual(charts[0].Thresholds, []alertdiagnostics.Threshold{{Operator: "<", Value: 5}}) {
		t.Errorf("shared signal lost independent threshold: %+v", charts)
	}
}

func TestCollectAlertDiagnosticsRuleMetadata(t *testing.T) {
	t.Parallel()
	const group = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/group"
	for _, tc := range []struct {
		name          string
		definition    alertRuleDefinition
		step, warning string
	}{
		{"normalized match", alertRuleDefinition{Name: "Down", Expression: "up==0", GroupID: strings.ToUpper(group), Interval: "PT1.5S"}, "1500ms", ""},
		{"wrong name", alertRuleDefinition{Name: "Other", Expression: "up == 0", GroupID: group, Interval: "PT30S"}, "60s", "no rule metadata matches"},
		{"changed expression", alertRuleDefinition{Name: "Down", Expression: "up == 1", GroupID: group, Interval: "PT30S"}, "60s", "no rule metadata matches"},
		{"wrong group", alertRuleDefinition{Name: "Down", Expression: "up == 0", GroupID: group + "-other", Interval: "PT30S"}, "60s", "no rule metadata matches"},
		{"invalid interval", alertRuleDefinition{Name: "Down", Expression: "up == 0", GroupID: group, Interval: "invalid"}, "60s", "invalid interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := diagnosticTestOptions()
			a := diagnosticTestAlert("up == 0")
			a.Alert.AlertRule = group
			deps := gatherDependencies{queryRange: func(_ context.Context, _ *http.Client, _ azcore.TokenCredential, _, _ string, _, _ time.Time, step string) (*PrometheusResponse, error) {
				if step != tc.step {
					t.Errorf("query step=%q, want %q", step, tc.step)
				}
				return diagnosticTestMatrix(), nil
			}}
			report := o.collectAlertDiagnostics(t.Context(), []alert{a}, map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc", RuleDefinitions: []alertRuleDefinition{tc.definition}}}, deps, o.TimeWindow.End)
			if len(report.Queries) != 1 || report.Queries[0].Step != tc.step {
				t.Fatalf("report step diverged from request: %+v", report.Queries)
			}
			warning := report.Alerts[0].Warning
			if (warning == "") != (tc.warning == "") || !strings.Contains(warning, tc.warning) {
				t.Errorf("metadata warning=%q, want %q", warning, tc.warning)
			}
		})
	}
}

func TestCollectAlertDiagnosticsUnavailable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expression, workspace, want string
		data                              *workspaceData
	}{
		{name: "no expression", workspace: workspaceSvc, data: &workspaceData{PromEndpoint: "svc"}, want: "no PromQL expression"},
		{name: "blank expression", expression: " \n\t", workspace: workspaceSvc, data: &workspaceData{PromEndpoint: "svc"}, want: "no PromQL expression"},
		{name: "infra", expression: "up > 0", workspace: workspaceInfra, data: &workspaceData{}, want: "workspace endpoint unavailable"},
		{name: "missing workspace", expression: "up > 0", workspace: "missing", want: "workspace endpoint unavailable"},
		{name: "empty endpoint", expression: "up > 0", workspace: workspaceSvc, data: &workspaceData{}, want: "workspace endpoint unavailable"},
		{name: "endpoint error", expression: "up > 0", workspace: workspaceSvc, data: &workspaceData{PromEndpoint: "svc", PromError: errors.New("lookup failed")}, want: "workspace endpoint unavailable: lookup failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := diagnosticTestOptions()
			a := diagnosticTestAlert(tc.expression)
			a.Metadata.MonitoringWorkspaceType = tc.workspace
			deps := gatherDependencies{queryRange: func(context.Context, *http.Client, azcore.TokenCredential, string, string, time.Time, time.Time, string) (*PrometheusResponse, error) {
				t.Error("unavailable diagnostic must not query")
				return diagnosticTestMatrix(), nil
			}}
			report := o.collectAlertDiagnostics(t.Context(), []alert{a}, map[string]*workspaceData{tc.workspace: tc.data}, deps, o.TimeWindow.End)
			if len(report.Alerts) != 1 || len(report.Queries) != 0 || len(report.Alerts[0].Charts) != 0 || !strings.Contains(report.Alerts[0].Error, tc.want) {
				t.Fatalf("missing unavailable pane error %q: %+v", tc.want, report)
			}
		})
	}
}

func TestCollectAlertDiagnosticsFallback(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{"absent(up)", "absent_over_time(up[5m]) == 1", "up >"} {
		t.Run(expression, func(t *testing.T) {
			o := diagnosticTestOptions()
			var calls int
			deps := gatherDependencies{queryRange: func(_ context.Context, _ *http.Client, _ azcore.TokenCredential, _, query string, _, _ time.Time, step string) (*PrometheusResponse, error) {
				calls++
				if query != expression || step != "60s" {
					t.Errorf("fallback query=%q step=%q, want original %q and 60s", query, step, expression)
				}
				return diagnosticTestMatrix(), nil
			}}
			report := o.collectAlertDiagnostics(t.Context(), []alert{diagnosticTestAlert(expression)}, map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc"}}, deps, o.TimeWindow.End)
			if calls != 1 || len(report.Alerts[0].Charts) != 1 || !strings.Contains(report.Alerts[0].Warning, "fallback diagnostic step 60s") {
				t.Fatalf("fallback lost query/chart/step warning: calls=%d entry=%+v", calls, report.Alerts[0])
			}
			chart := report.Alerts[0].Charts[0]
			if chart.Expression != expression || chart.Path != "root" || chart.Fallback == "" || len(chart.Thresholds) != 0 || !reflect.DeepEqual(chart.Queries, []alertDiagnosticReference{{Result: 0, Role: "signal"}}) {
				t.Errorf("fallback chart makes extraction claims or lost original expression: %+v", chart)
			}
		})
	}
}

func TestCollectAlertDiagnosticsWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		now, end int64
		query    bool
	}{
		{"completed", 3000, 2000, true}, {"end capped", 1500, 1500, true},
		{"not started", 900, 900, false}, {"no elapsed time", 1000, 1000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := diagnosticTestOptions()
			o.TimeWindow.Start = o.TimeWindow.Start.In(time.FixedZone("test", 3600))
			var calls int
			deps := gatherDependencies{queryRange: func(_ context.Context, _ *http.Client, _ azcore.TokenCredential, _, _ string, start, end time.Time, _ string) (*PrometheusResponse, error) {
				calls++
				if start != o.TimeWindow.Start.UTC() || end != time.Unix(tc.end, 0).UTC() {
					t.Errorf("query window=%v to %v, want full start and capped end", start, end)
				}
				return diagnosticTestMatrix(), nil
			}}
			now := time.Unix(tc.now, 0).In(time.FixedZone("test", -3600))
			report := o.collectAlertDiagnostics(t.Context(), []alert{diagnosticTestAlert("up > 0")}, map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc"}}, deps, now)
			if report.SchemaVersion != 1 || report.Start != o.TimeWindow.Start.UTC() || report.End != time.Unix(tc.end, 0).UTC() || report.GeneratedAt != now.UTC() {
				t.Errorf("incorrect report metadata: %+v", report)
			}
			if (calls == 1) != tc.query || (!tc.query && (report.Alerts[0].Error != "no elapsed time in the report window" || len(report.Queries) != 0)) {
				t.Errorf("elapsed window handling: calls=%d report=%+v", calls, report)
			}
		})
	}
}

func TestCollectAlertDiagnosticsResponses(t *testing.T) {
	t.Parallel()
	series := []PrometheusResult{
		{Metric: map[string]string{"instance": "firing"}, Values: [][]any{{json.Number("1000.125"), "NaN"}, {json.Number("1060"), "+Inf"}, {json.Number("1120"), "-Inf"}}},
		{Metric: map[string]string{"instance": "not-firing"}, Values: [][]any{{json.Number("1000"), "0"}, {json.Number("1060"), "-0.0001"}}},
		{Metric: map[string]string{"instance": "empty"}, Values: [][]any{}},
	}
	for _, tc := range []struct {
		name     string
		response *PrometheusResponse
		err      error
		want     string
	}{
		{name: "all series and warnings", response: &PrometheusResponse{Status: "success", Data: PrometheusData{ResultType: "matrix", Result: series}, Warnings: []string{"partial backend"}, Infos: []string{"query info"}}},
		{name: "empty matrix", response: diagnosticTestMatrix()},
		{name: "nil response", want: "expected a Prometheus matrix response"},
		{name: "vector", response: &PrometheusResponse{Status: "success", Data: PrometheusData{ResultType: "vector"}}, want: "expected a Prometheus matrix response"},
		{name: "missing type", response: &PrometheusResponse{Status: "success"}, want: "expected a Prometheus matrix response"},
		{name: "query error", err: errors.New("backend unavailable"), want: "backend unavailable"},
		{name: "response with error", response: diagnosticTestMatrix(), err: errors.New("partial request failed"), want: "partial request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := diagnosticTestOptions()
			deps := gatherDependencies{queryRange: func(_ context.Context, _ *http.Client, _ azcore.TokenCredential, _, expression string, _, _ time.Time, _ string) (*PrometheusResponse, error) {
				if expression == "other" {
					return diagnosticTestMatrix(), nil
				}
				return tc.response, tc.err
			}}
			a := diagnosticTestAlert("up > 0")
			a.Alert.Labels = map[string]string{"instance": "firing"}
			report := o.collectAlertDiagnostics(t.Context(), []alert{a, diagnosticTestAlert("other > 0")}, map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc"}}, deps, o.TimeWindow.End)
			if len(report.Queries) != 2 || report.Queries[0].Error != tc.want || report.Queries[1].Error != "" || report.Queries[1].Series == nil {
				t.Fatalf("failure did not remain query-local: %+v", report.Queries)
			}
			for i, entry := range report.Alerts {
				if entry.Error != "" || len(entry.Charts) != 1 || entry.Charts[0].Queries[0].Result != i {
					t.Errorf("query failure dropped alert chart/reference: %+v", entry)
				}
			}
			if tc.want == "" {
				if !reflect.DeepEqual(report.Queries[0].Series, tc.response.Data.Result) || !reflect.DeepEqual(report.Queries[0].Warnings, append(tc.response.Warnings, tc.response.Infos...)) {
					t.Errorf("series or warnings were filtered: %+v", report.Queries[0])
				}
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "all series and warnings" {
					for _, value := range []string{`"NaN"`, `"+Inf"`, `"-Inf"`, `1000.125`, `"not-firing"`, `"empty"`} {
						if !strings.Contains(string(data), value) {
							t.Errorf("JSON lost raw value %s: %s", value, data)
						}
					}
				}
			}
		})
	}
}

func TestCollectAlertDiagnosticsHTTPStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"success", `{"status":"success","data":{"resultType":"matrix","result":[]},"warnings":["partial"],"infos":["info"]}`, "", http.StatusOK},
		{"api error", `{"status":"error","errorType":"execution","error":"backend failed","data":{"resultType":"matrix","result":[]}}`, "prometheus query error (execution): backend failed", http.StatusOK},
		{"missing status", `{"data":{"resultType":"matrix","result":[]}}`, "prometheus query error", http.StatusOK},
		{"http error", `unavailable`, "returned 503", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/query_range" || r.URL.Query().Get("query") != "up" || r.URL.Query().Get("start") != "1000" || r.URL.Query().Get("end") != "2000" || r.URL.Query().Get("step") != "60s" {
					t.Errorf("unexpected HTTP query: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			o := diagnosticTestOptions()
			deps := gatherDependencies{queryRange: func(ctx context.Context, client *http.Client, cred azcore.TokenCredential, endpoint, expression string, start, end time.Time, step string) (*PrometheusResponse, error) {
				if client.Timeout != 30*time.Second {
					t.Errorf("actual HTTP client timeout=%v, want 30s", client.Timeout)
				}
				return queryRange(ctx, client, cred, endpoint, expression, start, end, step)
			}}
			report := o.collectAlertDiagnostics(t.Context(), []alert{diagnosticTestAlert("up == 0")}, map[string]*workspaceData{workspaceSvc: {PromEndpoint: server.URL}}, deps, o.TimeWindow.End)
			got := report.Queries[0]
			if tc.want == "" {
				if got.Error != "" || !reflect.DeepEqual(got.Warnings, []string{"partial", "info"}) {
					t.Errorf("HTTP success lost warnings: %+v", got)
				}
			} else if !strings.Contains(got.Error, tc.want) {
				t.Errorf("HTTP failure=%q, want %q", got.Error, tc.want)
			}
		})
	}
}

func TestCollectAlertDiagnosticsConcurrencyAndCancellation(t *testing.T) {
	t.Parallel()
	for _, cancelInFlight := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelInFlight), func(t *testing.T) {
			o := diagnosticTestOptions()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var active, maximum, calls atomic.Int32
			entered := make(chan struct{}, 6)
			release := make(chan struct{})
			deps := gatherDependencies{queryRange: func(ctx context.Context, client *http.Client, _ azcore.TokenCredential, _, _ string, _, _ time.Time, _ string) (*PrometheusResponse, error) {
				calls.Add(1)
				n := active.Add(1)
				defer active.Add(-1)
				for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
				}
				if client.Timeout != 30*time.Second {
					t.Errorf("request timeout=%v, want 30s", client.Timeout)
				}
				entered <- struct{}{}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return diagnosticTestMatrix(), nil
				}
			}}
			var alerts []alert
			for i := range 6 {
				alerts = append(alerts, diagnosticTestAlert(fmt.Sprintf("metric_%d > 0", i)))
			}
			done := make(chan *alertDiagnosticsReport, 1)
			go func() {
				done <- o.collectAlertDiagnostics(ctx, alerts, map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc"}}, deps, o.TimeWindow.End)
			}()
			// Both workers must enter before either can finish or cancellation occurs.
			watchdog := time.NewTimer(5 * time.Second)
			defer watchdog.Stop()
			for range 2 {
				select {
				case <-entered:
				case <-watchdog.C:
					t.Fatal("two diagnostic queries did not start concurrently")
				}
			}
			if cancelInFlight {
				cancel()
			} else {
				close(release)
			}
			var report *alertDiagnosticsReport
			select {
			case report = <-done:
			case <-watchdog.C:
				t.Fatal("collector did not finish after release/cancellation")
			}
			wantCalls := int32(6)
			if cancelInFlight {
				wantCalls = 2
			}
			if maximum.Load() != 2 || active.Load() != 0 || calls.Load() != wantCalls || len(report.Queries) != 6 {
				t.Fatalf("worker bounds: max=%d active=%d calls=%d queries=%d", maximum.Load(), active.Load(), calls.Load(), len(report.Queries))
			}
			for i, query := range report.Queries {
				want := ""
				if cancelInFlight {
					want = context.Canceled.Error()
					if i >= 2 {
						want = "diagnostics not collected: " + want
					}
				}
				if query.Error != want {
					t.Errorf("query %d error=%q, want %q", i, query.Error, want)
				}
			}
		})
	}
}
