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
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestGatherIndependentFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{
		"", "alerts:svc", "alerts:hcp", "metricRules:svc", "rules:hcp", "rules:svc",
		"endpoint:hcp", "endpoint:svc", "query:svc", "metrics", "render:First",
		"render:alerts", "render:utilization", "write:alerts.json", "write:utilization.json",
		"write:junit", "write:page", "setup:cosmos", "setup:known", "setup:queries",
	} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			o := Options{completedOptions: &completedOptions{
				Workspaces: map[string]azcorearm.ResourceID{
					workspaceSvc: *mustParseResourceID("sub", "svc", "svc"),
					workspaceHcp: *mustParseResourceID("sub", "hcp", "hcp"),
				},
				MetricResources:   map[string]azcorearm.ResourceID{resourceCosmosDB: *mustParseResourceID("sub", "svc", "cosmos")},
				SeverityThreshold: -1,
				TimeWindow:        timing.TimeWindow{Start: time.Unix(1000, 0), End: time.Unix(2000, 0)},
				Queries: &QueriesConfig{Panels: []PanelSpec{
					{Title: "First", Queries: []QuerySpec{{Title: "svc", Workspace: workspaceSvc, Query: "up"}, {Title: "hcp", Workspace: workspaceHcp, Query: "up"}}},
					{Title: "Second", Queries: []QuerySpec{{Title: "cosmos", Source: sourceAzureMonitor, Resource: resourceCosmosDB}}},
				}},
			}}
			injected := errors.New("injected <script>failure</script>")
			switch failure {
			case "setup:cosmos":
				o.cosmosError = injected
			case "setup:known":
				o.knownIssuesError = injected
			case "setup:queries":
				o.queriesError = injected
				o.Queries = nil
			}
			calls := map[string]int{}
			call := func(name string) error {
				calls[name]++
				if name == failure {
					return injected
				}
				return nil
			}
			var output alertsOutput
			var suites *junit.TestSuites
			var tabs []observabilityTab
			deps := gatherDependencies{
				fetchAlerts: func(_ context.Context, _ azcore.TokenCredential, scope string, _, _ time.Time) ([]alert, error) {
					name := filepath.Base(scope)
					ws := o.Workspaces[name]
					return []alert{{Alert: alertData{Name: "available-" + name}, Metadata: alertMetadata{MonitoringWorkspace: ws.String()}}}, call("alerts:" + name)
				},
				fetchMetricAlertRules: func(_ context.Context, _ azcore.TokenCredential, _, rg string) ([]string, error) {
					return []string{"metric-rule-" + rg}, call("metricRules:" + rg)
				},
				fetchAlertRules: func(_ context.Context, _ azcore.TokenCredential, _, _ string, ws azcorearm.ResourceID) ([]string, error) {
					return []string{"quiet"}, call("rules:" + ws.Name)
				},
				lookupEndpoint: func(_ context.Context, _ azcore.TokenCredential, _, _, name string) (string, error) {
					return name, call("endpoint:" + name)
				},
				queryRange: func(_ context.Context, _ *http.Client, _ azcore.TokenCredential, endpoint, _ string, _, _ time.Time, _ string) (*PrometheusResponse, error) {
					return &PrometheusResponse{}, call("query:" + endpoint)
				},
				queryMetrics: func(context.Context, azcore.TokenCredential, azcorearm.ResourceID, QuerySpec, time.Time, time.Time, autoscaleMaxLookup) ([]PrometheusResult, string, error) {
					return nil, "", call("metrics")
				},
				renderAlerts: func(data any) ([]byte, error) {
					output = data.(alertsOutput)
					return []byte("alerts partial"), call("render:alerts")
				},
				renderPanel: func(data panelPageData) ([]byte, error) {
					return []byte(data.Title + " partial"), call("render:" + data.Title)
				},
				collectUtilization: func(_ context.Context, workspaces map[string]*workspaceData) utilizationReport {
					_ = call("utilization")
					if (workspaces[workspaceSvc].PromError != nil) != (failure == "endpoint:svc") {
						t.Error("service endpoint error was not passed independently to utilization")
					}
					return utilizationReport{}
				},
				renderUtilization: func(utilizationReport) ([]byte, error) {
					return []byte("utilization partial"), call("render:utilization")
				},
				writeFile: func(path string, data []byte, _ os.FileMode) error {
					if !json.Valid(data) {
						t.Errorf("invalid JSON artifact %s", path)
					}
					return call("write:" + filepath.Base(path))
				},
				writeJUnit: func(_ string, data *junit.TestSuites) error {
					suites = data
					return call("write:junit")
				},
				renderPage: func(_ string, data []observabilityTab) error {
					tabs = data
					return call("write:page")
				},
			}
			err := o.run(logr.NewContext(context.Background(), logr.Discard()), deps)
			fatal := failure != "" && failure != "query:svc" && failure != "metrics" && failure != "render:First"
			if (err != nil) != fatal {
				t.Fatalf("Run error = %v, want fatal = %v", err, fatal)
			}
			if fatal && !errors.Is(err, injected) {
				t.Errorf("fatal aggregate lost injected error: %v", err)
			}
			for _, name := range []string{"alerts:svc", "alerts:hcp", "metricRules:svc", "metricRules:hcp", "rules:svc", "rules:hcp", "endpoint:svc", "endpoint:hcp", "render:alerts", "write:alerts.json", "write:junit", "utilization", "render:utilization", "write:utilization.json", "write:page"} {
				if calls[name] != 1 {
					t.Errorf("independent operation %s attempted %d times, want 1", name, calls[name])
				}
			}
			if failure != "setup:queries" {
				for _, name := range []string{"query:svc", "query:hcp", "metrics", "render:First", "render:Second"} {
					if name == "query:svc" && failure == "endpoint:svc" || name == "query:hcp" && failure == "endpoint:hcp" {
						continue
					}
					if calls[name] != 1 {
						t.Errorf("independent query/render %s attempted %d times, want 1", name, calls[name])
					}
				}
			}
			var titles []string
			for _, tab := range tabs {
				titles = append(titles, tab.Title)
				if strings.Contains(tab.HTML, "<script>failure</script>") {
					t.Errorf("unescaped error in %s tab", tab.Title)
				}
			}
			wantTitles := []string{"Azure Monitor Alerts", "First", "Second", "Utilization"}
			if failure == "setup:queries" {
				wantTitles = []string{"Azure Monitor Alerts", "Metrics", "Utilization"}
			}
			if !reflect.DeepEqual(titles, wantTitles) {
				t.Errorf("tab order = %v, want %v", titles, wantTitles)
			}
			incomplete := strings.HasPrefix(failure, "alerts:") || strings.HasPrefix(failure, "rules:") || strings.HasPrefix(failure, "metricRules:") || failure == "setup:known"
			if (len(output.CollectionErrors) > 0) != incomplete || (suites.Suites[0].NumFailed > 0) != incomplete {
				t.Errorf("collection completeness mismatch: output=%v, failures=%d", output.CollectionErrors, suites.Suites[0].NumFailed)
			}
			if len(output.Alerts) != 2 {
				t.Errorf("partial alert pages were dropped: got %d alerts", len(output.Alerts))
			}
			for _, tc := range suites.Suites[0].TestCases {
				if !strings.Contains(tc.Name, "does not fire") {
					continue
				}
				affected := failure == "setup:known" ||
					strings.Contains(tc.Name, "[svc]") && (failure == "alerts:svc" || failure == "rules:svc") ||
					strings.Contains(tc.Name, "[hcp]") && (failure == "alerts:hcp" || failure == "rules:hcp") ||
					strings.Contains(tc.Name, "[infra]") && (strings.HasPrefix(failure, "alerts:") || strings.HasPrefix(failure, "metricRules:"))
				if (tc.SkipMessage != nil) != affected {
					t.Errorf("scope completeness for %s: skipped=%v, want %v", tc.Name, tc.SkipMessage != nil, affected)
				}
			}
		})
	}
}

func TestCompleteRetainsIndependentSetupResults(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "AzureCLICredential")
	for _, missing := range []string{"cosmos", "hcp", "scaling"} {
		t.Run(missing, func(t *testing.T) {
			dir := t.TempDir()
			cfg := map[string]any{
				"regionRG":   "region",
				"monitoring": map[string]any{"svcWorkspaceName": "svc", "hcpWorkspaceName": "hcp"},
				"frontend": map[string]any{"cosmosDB": map[string]any{
					"name": "cosmos", "resourceContainerMaxScale": 1000,
					"billingContainerMaxScale": 2000, "fleetContainerMaxScale": 3000, "locksContainerMaxScale": 4000,
				}},
				"kubeApplier": map[string]any{"cosmosContainerMaxScale": 5000},
			}
			switch missing {
			case "cosmos":
				delete(cfg, "frontend")
			case "hcp":
				delete(cfg["monitoring"].(map[string]any), "hcpWorkspaceName")
			case "scaling":
				delete(cfg["frontend"].(map[string]any)["cosmosDB"].(map[string]any), "billingContainerMaxScale")
			}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			raw := &RawOptions{TimingInputDir: dir, OutputDir: dir, RenderedConfig: path, SubscriptionID: "sub", StartTimeFallback: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
			validated, err := raw.Validate()
			if err != nil {
				t.Fatal(err)
			}
			o, err := validated.Complete(logr.NewContext(t.Context(), logr.Discard()))
			if err != nil {
				t.Fatalf("branch-local %s failure gated Complete: %v", missing, err)
			}
			if _, ok := o.Workspaces[workspaceSvc]; !ok || o.Queries == nil {
				t.Fatal("independent workspace/query setup was lost")
			}
			if !o.resourceGroups.Has("/subscriptions/sub/resourceGroups/region") {
				t.Fatal("alert collection scope was lost during workspace setup")
			}
			if missing == "hcp" {
				if o.workspaceErrors[workspaceHcp] == nil || o.cosmosError != nil {
					t.Fatalf("wrong branch setup results: workspace=%v cosmos=%v", o.workspaceErrors, o.cosmosError)
				}
			} else if o.cosmosError == nil {
				t.Fatal("Cosmos setup error was lost")
			}
			if missing == "scaling" && (o.cosmosAutoscaleMax("Resources") != 1000 || o.cosmosAutoscaleMax("Manifests-MC-1") != 5000) {
				t.Fatal("missing one autoscale ceiling discarded available values")
			}
		})
	}
}

func TestGatherWorkspaceResolutionAndRuleScope(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "AzureCLICredential")
	for _, tt := range []struct {
		name  string
		svcID string
		hcpID string
	}{
		{name: "default"},
		{name: "external svc only", svcID: mustParseResourceID("pool-sub", "pool-rg", "pooled-svc").String()},
		{name: "external hcp only", hcpID: mustParseResourceID("pool-sub", "pool-rg", "pooled-hcp").String()},
		{name: "both external cross subscription different RG", svcID: mustParseResourceID("pool-sub", "svc-pool-rg", "pooled-svc").String(), hcpID: mustParseResourceID("other-pool-sub", "hcp-pool-rg", "pooled-hcp").String()},
		{name: "same RG name cross subscription", svcID: mustParseResourceID("pool-sub", "job-rg", "svc").String(), hcpID: mustParseResourceID("pool-sub", "job-rg", "hcp").String()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := map[string]any{
				"regionRG": "job-rg",
				"monitoring": map[string]any{
					"svcWorkspaceName": "svc", "hcpWorkspaceName": "hcp",
					"svcWorkspaceResourceId": tt.svcID, "hcpWorkspaceResourceId": tt.hcpID,
				},
			}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			raw := &RawOptions{TimingInputDir: dir, OutputDir: dir, RenderedConfig: path, SubscriptionID: "job-sub", StartTimeFallback: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
			validated, err := raw.Validate()
			if err != nil {
				t.Fatal(err)
			}
			ctx := logr.NewContext(t.Context(), logr.Discard())
			o, err := validated.Complete(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(o.workspaceErrors) != 0 {
				t.Fatalf("workspace setup errors: %v", o.workspaceErrors)
			}
			want := map[string]string{workspaceSvc: tt.svcID, workspaceHcp: tt.hcpID}
			for wsType, id := range want {
				if id == "" {
					want[wsType] = mustParseResourceID("job-sub", "job-rg", wsType).String()
				}
				ws := o.Workspaces[wsType]
				if ws.String() != want[wsType] {
					t.Fatalf("%s workspace = %q, want %q", wsType, ws.String(), want[wsType])
				}
			}
			if !o.resourceGroups.Equal(sets.New("/subscriptions/job-sub/resourceGroups/job-rg")) {
				t.Fatalf("job resource group lost: %v", o.resourceGroups)
			}

			// Exercise routing without Azure calls or unrelated metric collection.
			o.Queries, o.cosmosError = nil, nil
			deps := o.dependencies()
			deps.fetchAlerts = func(context.Context, azcore.TokenCredential, string, time.Time, time.Time) ([]alert, error) {
				return nil, nil
			}
			deps.fetchMetricAlertRules = func(context.Context, azcore.TokenCredential, string, string) ([]string, error) { return nil, nil }
			ruleIDs, endpointIDs := sets.New[string](), sets.New[string]()
			deps.fetchAlertRules = func(_ context.Context, _ azcore.TokenCredential, sub, rg string, ws azcorearm.ResourceID) ([]string, error) {
				if sub != "job-sub" || rg != "job-rg" {
					t.Errorf("rule discovery scope = %s/%s, want job-sub/job-rg", sub, rg)
				}
				ruleIDs.Insert(ws.String())
				return nil, nil
			}
			deps.lookupEndpoint = func(_ context.Context, _ azcore.TokenCredential, sub, rg, name string) (string, error) {
				endpointIDs.Insert(mustParseResourceID(sub, rg, name).String())
				return "https://prometheus.example.com", nil
			}
			deps.collectUtilization = func(context.Context, map[string]*workspaceData) utilizationReport { return utilizationReport{} }
			deps.renderUtilization = func(utilizationReport) ([]byte, error) { return []byte("utilization"), nil }
			if err := o.run(ctx, deps); err != nil {
				t.Fatal(err)
			}
			wantIDs := sets.New(want[workspaceSvc], want[workspaceHcp])
			if !ruleIDs.Equal(wantIDs) || !endpointIDs.Equal(wantIDs) {
				t.Errorf("workspace routing: rule IDs=%v endpoint IDs=%v, want %v", ruleIDs, endpointIDs, wantIDs)
			}
		})
	}
}

func TestQueriesKeepFailedChartsAndTabs(t *testing.T) {
	t.Parallel()
	o := Options{completedOptions: &completedOptions{
		Queries: &QueriesConfig{Panels: []PanelSpec{
			{Title: "selectors", Queries: []QuerySpec{
				{Title: "unknown workspace", Workspace: "missing"},
				{Title: "unknown resource", Source: sourceAzureMonitor, Resource: "missing"},
				{Title: "endpoint", Workspace: workspaceHcp},
				{Title: "query error", Workspace: workspaceSvc},
				{Title: "valid", Workspace: workspaceSvc},
			}},
			{Title: "later", Queries: []QuerySpec{{Title: "later", Workspace: workspaceSvc}}},
		}},
	}}
	workspaces := map[string]*workspaceData{workspaceSvc: {PromEndpoint: "svc"}, workspaceHcp: {}}
	var calls int
	deps := gatherDependencies{
		queryRange: func(context.Context, *http.Client, azcore.TokenCredential, string, string, time.Time, time.Time, string) (*PrometheusResponse, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("ordinary query failure")
			}
			return &PrometheusResponse{}, nil
		},
		renderPanel: func(data panelPageData) ([]byte, error) {
			if data.Title == "selectors" {
				if len(data.Charts) != 5 {
					t.Fatalf("failed queries suppressed charts: %d", len(data.Charts))
				}
				for _, c := range data.Charts[:4] {
					if c.Error == "" {
						t.Errorf("chart %s lost error", c.Title)
					}
				}
				return []byte("retained partial"), errors.New("<script>render failure</script>")
			}
			return []byte("later succeeded"), nil
		},
	}
	tabs, err := o.runQueries(logr.NewContext(context.Background(), logr.Discard()), workspaces, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown metric resource") || !strings.Contains(err.Error(), "unknown workspace") || !strings.Contains(err.Error(), "missing Prometheus endpoint") {
		t.Errorf("missing aggregated selector errors: %v", err)
	}
	if calls != 3 || len(tabs) != 2 {
		t.Fatalf("later work suppressed: queries=%d tabs=%d", calls, len(tabs))
	}
	if !strings.Contains(tabs[0].HTML, "retained partial") || !strings.Contains(tabs[0].HTML, "&lt;script&gt;") || strings.Contains(tabs[0].HTML, "<script>") {
		t.Errorf("failed tab lost partial content or escaped error: %s", tabs[0].HTML)
	}
}

func TestIncompleteAlertJUnitAndHTML(t *testing.T) {
	t.Parallel()
	issues, err := parseKnownIssues([]byte("knownIssues:\n- name: known\n  reason: tracked\n"))
	if err != nil {
		t.Fatal(err)
	}
	ws := buildWorkspaceAlertData(workspaceSvc, *mustParseResourceID("sub", "svc", "svc"), []alert{
		{Alert: alertData{Name: "known"}, Metadata: alertMetadata{MonitoringWorkspace: mustParseResourceID("sub", "svc", "svc").String()}},
		{Alert: alertData{Name: "unknown"}, Metadata: alertMetadata{MonitoringWorkspace: mustParseResourceID("sub", "svc", "svc").String()}},
	}, -1, issues)
	ws.AlertRules = []string{"quiet", "known", "unknown"}
	ws.CollectionError = errors.New("second page <script>failed</script>")
	suite := workspaceDataToJUnit(logr.Discard(), ws, timing.TimeWindow{})
	if suite.NumFailed != 2 || suite.NumSkipped != 2 || suite.NumTests != 4 {
		t.Fatalf("incomplete collection should fail explicitly, skip quiet/known, and fail unknown: %+v", suite)
	}
	for _, tc := range suite.TestCases {
		if tc.FailureOutput == nil && tc.SkipMessage == nil {
			t.Errorf("incomplete scope incorrectly passed %s", tc.Name)
		}
	}
	for _, alerts := range [][]alert{ws.FiredAlerts, nil} {
		output := alertsOutput{Alerts: alerts, Summary: alertsSummary{Total: len(alerts)}, CollectionErrors: []string{ws.CollectionError.Error()}}
		html, err := renderAlertsHTML(output)
		if err != nil {
			t.Fatal(err)
		}
		page := string(html)
		if !strings.Contains(page, "Alert collection incomplete") || strings.Contains(page, "<script>failed</script>") || strings.Contains(page, "No alerts fired during the test window.") {
			t.Errorf("incomplete collection lacks safe, truthful warning: %s", page)
		}
		if len(alerts) > 0 && !strings.Contains(page, "unknown") {
			t.Error("available alerts dropped from incomplete report")
		}
	}
}

func TestGatherWritesArtifactsWithUtilizationWarnings(t *testing.T) {
	t.Parallel()
	o := Options{completedOptions: &completedOptions{
		TimeWindow: timing.TimeWindow{Start: time.Unix(1000, 0), End: time.Unix(2000, 0)},
	}}
	// No workspace endpoints: the real collector must produce warning artifacts
	// without network access, including when unrelated writes/setup also fail.
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "warnings only", true: "aggregate fatal errors"}[fail], func(t *testing.T) {
			o.OutputDir = t.TempDir()
			deps := o.dependencies()
			writeErr := errors.New("alerts JSON write failed")
			setupErr := errors.New("query configuration failed")
			if fail {
				o.queriesError = setupErr
				deps.writeFile = func(path string, data []byte, mode os.FileMode) error {
					if filepath.Base(path) == "alerts.json" {
						return writeErr
					}
					return os.WriteFile(path, data, mode)
				}
			}
			err := o.run(logr.NewContext(t.Context(), logr.Discard()), deps)
			if !fail && err != nil {
				t.Fatalf("utilization warnings must not gate: %v", err)
			}
			if fail && (!errors.Is(err, writeErr) || !errors.Is(err, setupErr)) {
				t.Fatalf("fatal aggregate lost independent errors: %v", err)
			}
			for _, name := range []string{"alerts.json", "junit_alerts.xml", "observability-summary.html", "utilization.json"} {
				if fail && name == "alerts.json" {
					continue
				}
				data, err := os.ReadFile(filepath.Join(o.OutputDir, name))
				if err != nil {
					t.Fatalf("missing independent artifact %s: %v", name, err)
				}
				if name == "utilization.json" {
					var report utilizationReport
					if err := json.Unmarshal(data, &report); err != nil || len(report.Warnings) == 0 {
						t.Errorf("missing utilization collection warnings: %s, error %v", data, err)
					}
				}
				if name == "observability-summary.html" && !strings.Contains(string(data), `"title":"Utilization"`) {
					t.Error("real combined page omitted Utilization tab")
				}
			}
		})
	}
}

func TestMissingWorkspaceSetupDoesNotGateAlertScope(t *testing.T) {
	t.Parallel()
	o := Options{completedOptions: &completedOptions{
		OutputDir:         t.TempDir(),
		resourceGroups:    sets.New("/subscriptions/sub/resourceGroups/region"),
		SeverityThreshold: -1,
		workspaceErrors:   map[string]error{workspaceSvc: errors.New("missing svc"), workspaceHcp: errors.New("missing hcp")},
	}}
	deps := o.dependencies()
	var alertsCalled, rulesCalled bool
	deps.fetchAlerts = func(context.Context, azcore.TokenCredential, string, time.Time, time.Time) ([]alert, error) {
		alertsCalled = true
		return []alert{{Alert: alertData{Name: "retained"}, Metadata: alertMetadata{MonitoringWorkspace: mustParseResourceID("sub", "region", "unknown").String()}}}, nil
	}
	deps.fetchMetricAlertRules = func(context.Context, azcore.TokenCredential, string, string) ([]string, error) {
		rulesCalled = true
		return nil, nil
	}
	if err := o.run(logr.NewContext(t.Context(), logr.Discard()), deps); err == nil {
		t.Fatal("missing workspace setup must remain fatal")
	}
	if !alertsCalled || !rulesCalled {
		t.Fatalf("workspace setup gated region alerts: alerts=%v rules=%v", alertsCalled, rulesCalled)
	}
	data, err := os.ReadFile(filepath.Join(o.OutputDir, "alerts.json"))
	if err != nil || !strings.Contains(string(data), "retained") {
		t.Fatalf("unresolved workspace alert was discarded: %s, %v", data, err)
	}
}
