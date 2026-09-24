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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

const amwTestWorkspace = "/subscriptions/sub-a/resourceGroups/rg/providers/Microsoft.Monitor/accounts/workspace"
const amwTestDCR = "/subscriptions/sub-a/resourceGroups/other-rg/providers/Microsoft.Insights/dataCollectionRules/rule"

var amwTestStart = time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("test", 3600))
var amwTestEnd = amwTestStart.Add(2 * time.Minute)

func TestAMWCollectGatherGraceWindow(t *testing.T) {
	ctx := logr.NewContext(context.Background(), logr.Discard())
	start := time.Now().Add(-time.Hour)
	window, err := timing.ComputeTimeWindow(ctx, clock.RealClock{}, nil, nil, &start)
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	report := collectAMW(ctx, amwTestCredential{t: t}, []string{amwTestWorkspace}, window.Start, window.End, amwTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return amwTestResponse(r, 200, `{"value":[]}`)
	}))
	if requests == 0 || !report.RequestedEnd.Equal(window.End) || report.End.After(time.Now().Add(time.Minute)) || !report.End.Before(window.End) {
		t.Fatalf("future gather grace was not clamped with requested bounds preserved: %+v, requests=%d", report, requests)
	}
}

type amwTestCredential struct {
	t   *testing.T
	err error
}

func (c amwTestCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.t.Helper()
	if !slices.Equal(options.Scopes, []string{"https://management.core.windows.net//.default"}) {
		c.t.Fatalf("unexpected token scope (must never use Prometheus): %v", options.Scopes)
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > amwRequestTimeout {
		c.t.Fatalf("credential request lacks bounded deadline: %v", deadline)
	}
	return azcore.AccessToken{Token: "secret-token-never-persist", ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

type amwTestTransport func(*http.Request) (*http.Response, error)

func (f amwTestTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

func amwTestResponse(request *http.Request, status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func amwTestMetricBody(request *http.Request) string {
	name := request.URL.Query().Get("metricnames")
	aggregation := strings.ToLower(request.URL.Query().Get("aggregation"))
	metadata := `[{"name":{"value":"StampColor"},"value":"blue"}]`
	if strings.HasSuffix(name, "Dropped") {
		metadata = `[{"name":{"value":"StampColor"},"value":"blue"},{"name":{"value":"Reason"},"value":"limit"}]`
	}
	if name == "MetricIngestionRequest_Count" {
		metadata = `[{"name":{"value":"InputStreamId"},"value":"stream"},{"name":{"value":"ResponseCode"},"value":"429"}]`
	}
	return fmt.Sprintf(`{"interval":"PT1M","value":[{"name":{"value":%q},"errorCode":"Success","timeseries":[{"metadatavalues":%s,"data":[{"timeStamp":"2026-01-02T02:05:00Z",%q:7},{"timeStamp":"2026-01-02T02:06:00Z"}]}]}]}`, name, metadata, aggregation)
}

func amwTestCollect(t *testing.T, ids []string, handler amwTestTransport) amwReport {
	t.Helper()
	return collectAMW(context.Background(), amwTestCredential{t: t}, ids, amwTestStart, amwTestEnd, amwTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "management.azure.com" || request.URL.Scheme != "https" {
			t.Fatalf("non-ARM or mutating request: %s %s", request.Method, request.URL)
		}
		if deadline, ok := request.Context().Deadline(); !ok || time.Until(deadline) > amwRequestTimeout {
			t.Fatalf("request lacks bounded deadline: %v", deadline)
		}
		return handler(request)
	}))
}

func TestAMWCollectARMOnly(t *testing.T) {
	lists, definitions, metrics := map[string]int{}, map[string]int{}, map[string]int{}
	workspaceMetrics := 0
	workspaceB := strings.Replace(amwTestWorkspace, "workspace", "workspace-b", 1)
	workspaceC := strings.Replace(amwTestWorkspace, "sub-a", "sub-b", 1)
	report := amwTestCollect(t, []string{workspaceC, workspaceB, amwTestWorkspace, strings.ToUpper(amwTestWorkspace)}, func(request *http.Request) (*http.Response, error) {
		path, query := request.URL.Path, request.URL.Query()
		switch {
		case strings.HasSuffix(strings.ToLower(path), "/datacollectionrules"):
			if workspaceMetrics != 18 {
				t.Fatalf("discovery started before all workspace metrics: %d", workspaceMetrics)
			}
			lists[path]++
			if strings.Contains(path, "sub-b") {
				return amwTestResponse(request, 200, `{"value":[]}`)
			}
			body := fmt.Sprintf(`{"value":[null,{}, {"id":%q,"properties":{"destinations":{"monitoringAccounts":[null,{"accountResourceId":%q},{"accountResourceId":%q}]}}},{"id":"unrelated","properties":{"destinations":{"monitoringAccounts":[{"accountResourceId":"unselected"}]}}}]}`, amwTestDCR, amwTestWorkspace, workspaceB)
			return amwTestResponse(request, 200, body)
		case strings.HasSuffix(path, "/metricDefinitions"):
			if workspaceMetrics != 18 || metrics[amwTestDCR+"/providers/Microsoft.Insights/metrics"] != 1 {
				t.Fatal("definitions started before workspace and DCR metrics")
			}
			definitions[path]++
			return amwTestResponse(request, 200, `{"value":[{"name":{"value":"definition-evidence"}}]}`)
		case strings.HasSuffix(path, "/metrics"):
			if query.Get("metricnamespace") == amwWorkspaceNamespace {
				workspaceMetrics++
			}
			metrics[path]++
			name := query.Get("metricnames")
			wantFilter, wantAggregation, wantNamespace := "StampColor eq '*'", "Maximum", amwWorkspaceNamespace
			if strings.HasSuffix(name, "Dropped") {
				wantFilter += " and Reason eq '*'"
			}
			if name == "MetricIngestionRequest_Count" {
				wantFilter, wantAggregation, wantNamespace = "InputStreamId eq '*' and ResponseCode eq '*'", "Total", amwDCRNamespace
			}
			for key, want := range map[string]string{"$filter": wantFilter, "aggregation": wantAggregation, "metricnamespace": wantNamespace, "interval": "PT1M", "top": "1000", "AutoAdjustTimegrain": "false", "ValidateDimensions": "true", "timespan": "2026-01-02T02:04:00Z/2026-01-02T02:07:00Z"} {
				if got := query.Get(key); got != want {
					t.Errorf("%s request %s = %q, want %q", name, key, got, want)
				}
			}
			return amwTestResponse(request, 200, amwTestMetricBody(request))
		default:
			t.Fatalf("unexpected request: %s", request.URL)
			return nil, errors.New("unexpected request")
		}
	})
	if len(report.Errors) != 0 {
		t.Fatalf("valid collection returned errors: %v", report.Errors)
	}
	if len(lists) != 2 || len(definitions) != 4 || len(metrics) != 4 || len(report.Resources) != 4 {
		t.Fatalf("unexpected discovery/definition/metric/resource counts: %v %v %v %d", lists, definitions, metrics, len(report.Resources))
	}
	for _, count := range lists {
		if count != 1 {
			t.Errorf("subscription listed %d times", count)
		}
	}
	for _, count := range definitions {
		if count != 1 {
			t.Errorf("definitions fetched %d times", count)
		}
	}
	if len(report.Discovery) != 2 || len(report.Discovery[0].Responses[0].Value) != 4 {
		t.Fatalf("raw DCR discovery evidence lost: %+v", report.Discovery)
	}
	if !slices.IsSortedFunc(report.Resources, func(a, b amwResource) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatal("resources are not sorted")
	}
	for _, resource := range report.Resources {
		wantMetrics := 6
		if resource.Kind == "dcr" {
			wantMetrics = 1
			if len(resource.Workspaces) != 2 {
				t.Fatalf("DCR destination links lost: %v", resource.Workspaces)
			}
		}
		if len(resource.Metrics) != wantMetrics || resource.Definitions == nil || len(resource.Definitions.Value) != 1 {
			t.Fatalf("missing resource metrics or definitions: %+v", resource)
		}
		if !slices.IsSortedFunc(resource.Metrics, func(a, b amwMetric) int { return strings.Compare(a.Name, b.Name) }) {
			t.Fatal("metrics are not sorted")
		}
		for _, metric := range resource.Metrics {
			points := metric.Response.Value[0].Timeseries[0].Data
			if len(points) != 2 || points[1].Maximum != nil || points[1].Total != nil {
				t.Fatalf("raw missing sample was lost or zero-filled: %+v", points)
			}
		}
	}
	if report.Start.Location() != time.UTC || report.End.Location() != time.UTC {
		t.Fatal("window was not normalized to UTC")
	}
}

func TestAMWCollectInvalidWindows(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end time.Time
	}{
		{"zero", time.Time{}, amwTestEnd}, {"equal", amwTestStart, amwTestStart},
		{"reverse", amwTestEnd, amwTestStart}, {"long", amwTestStart, amwTestStart.Add(24 * time.Hour)},
		{"future", time.Now().Add(time.Hour), time.Now().Add(2 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := collectAMW(context.Background(), amwTestCredential{t: t}, []string{amwTestWorkspace}, tc.start, tc.end, amwTestTransport(func(*http.Request) (*http.Response, error) { t.Fatal("invalid window made a request"); return nil, nil }))
			if len(report.Errors) == 0 || len(report.Resources) != 0 {
				t.Fatalf("invalid window accepted: %+v", report)
			}
		})
	}
}

func TestAMWCollectMetricValidation(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"empty", `{"interval":"PT1M","value":[]}`, "exactly once"},
		{"nil", `{"interval":"PT1M","value":[null]}`, "exactly once"},
		{"wrong metric", `{"interval":"PT1M","value":[{"name":{"value":"wrong"}}]}`, "exactly once"},
		{"duplicate", `{"interval":"PT1M","value":[{"name":{"value":"ActiveTimeSeries"}},{"name":{"value":"ActiveTimeSeries"}}]}`, "exactly once"},
		{"wrong interval", `{"interval":"PT5M","value":[]}`, "not PT1M"},
		{"missing interval", `{"value":[]}`, "not PT1M"},
		{"empty series", `{"interval":"PT1M","value":[{"name":{"value":"ActiveTimeSeries"}}]}`, "empty timeseries"},
		{"nil series", `{"interval":"PT1M","value":[{"name":{"value":"ActiveTimeSeries"},"timeseries":[null]}]}`, "nil timeseries"},
		{"no values", `{"interval":"PT1M","value":[{"name":{"value":"ActiveTimeSeries"},"timeseries":[{"data":[null,{"timeStamp":"2026-01-02T02:05:00Z"}]}]}]}`, "no samples"},
		{"no dimension", `{"interval":"PT1M","value":[{"name":{"value":"ActiveTimeSeries"},"timeseries":[{"data":[{"timeStamp":"2026-01-02T02:05:00Z","maximum":0}]}]}]}`, "dimension StampColor"},
		{"metric error", `{"interval":"PT1M","value":[{"name":{"value":"ActiveTimeSeries"},"errorCode":"Failed","errorMessage":"partial","timeseries":[]}]}`, "individual metric error"},
		{"malformed JSON", `{`, "decoding error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
				if request.URL.Query().Get("metricnames") == "ActiveTimeSeries" {
					return amwTestResponse(request, 200, tc.body)
				}
				if strings.HasSuffix(request.URL.Path, "/metrics") {
					return amwTestResponse(request, 200, amwTestMetricBody(request))
				}
				return amwTestResponse(request, 200, `{"value":[]}`)
			})
			for _, metric := range report.Resources[0].Metrics {
				if metric.Name == "ActiveTimeSeries" {
					if !strings.Contains(metric.Error, tc.want) || metric.Response == nil {
						t.Fatalf("missing validation error/raw response: %+v", metric)
					}
				} else if metric.Error != "" {
					t.Errorf("individual failure affected %s: %s", metric.Name, metric.Error)
				}
			}
		})
	}
}

func TestAMWCollectCaps(t *testing.T) {
	t.Run("pages", func(t *testing.T) {
		pages := 0
		report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/dataCollectionRules") {
				pages++
				return amwTestResponse(request, 200, fmt.Sprintf(`{"value":[],"nextLink":"https://management.azure.com/subscriptions/sub-a/providers/Microsoft.Insights/dataCollectionRules?page=%d"}`, pages))
			}
			if strings.HasSuffix(request.URL.Path, "/metrics") {
				return amwTestResponse(request, 200, amwTestMetricBody(request))
			}
			return amwTestResponse(request, 200, `{"value":[]}`)
		})
		if pages != 10 || len(report.Discovery[0].Responses) != 10 || !strings.Contains(report.Discovery[0].Error, "page cap") {
			t.Fatalf("pagination unbounded or warning missing: %d %+v", pages, report.Discovery)
		}
	})
	t.Run("workspaces and DCRs", func(t *testing.T) {
		var ids []string
		for i := 0; i < 6; i++ {
			ids = append(ids, fmt.Sprintf("%s-%d", amwTestWorkspace, i))
		}
		lists := 0
		report := amwTestCollect(t, ids, func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/dataCollectionRules") {
				lists++
				var rules []string
				for i := 0; i < 35; i++ {
					rules = append(rules, fmt.Sprintf(`{"id":%q,"properties":{"destinations":{"monitoringAccounts":[{"accountResourceId":%q}]}}}`, fmt.Sprintf("%s-%d", amwTestDCR, i), ids[0]))
				}
				return amwTestResponse(request, 200, `{"value":[`+strings.Join(rules, ",")+`]}`)
			}
			if strings.HasSuffix(request.URL.Path, "/metrics") {
				return amwTestResponse(request, 200, amwTestMetricBody(request))
			}
			return amwTestResponse(request, 200, `{"value":[]}`)
		})
		if lists != 1 || len(report.Resources) != 36 || !strings.Contains(strings.Join(report.Errors, " "), "workspace cap") || !strings.Contains(strings.Join(report.Errors, " "), "DCR cap") {
			t.Fatalf("resource bounds or warnings wrong: lists=%d resources=%d errors=%v", lists, len(report.Resources), report.Errors)
		}
	})
	t.Run("series", func(t *testing.T) {
		report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/metrics") {
				var response armmonitor.MetricsClientListResponse
				if err := json.Unmarshal([]byte(amwTestMetricBody(request)), &response); err != nil {
					t.Fatal(err)
				}
				series := response.Value[0].Timeseries[0]
				response.Value[0].Timeseries = make([]*armmonitor.TimeSeriesElement, 1000)
				for i := range response.Value[0].Timeseries {
					response.Value[0].Timeseries[i] = series
				}
				body, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				return amwTestResponse(request, 200, string(body))
			}
			return amwTestResponse(request, 200, `{"value":[]}`)
		})
		for _, metric := range report.Resources[0].Metrics {
			if !strings.Contains(metric.Error, "timeseries cap") || len(metric.Response.Value[0].Timeseries) != 1000 {
				t.Fatalf("series cap not detected or evidence lost: %s", metric.Error)
			}
		}
	})
}

func TestAMWCollectFailuresAndCancellation(t *testing.T) {
	t.Run("no retries or error leaks", func(t *testing.T) {
		requests := 0
		report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
			requests++
			return amwTestResponse(request, 429, `{"error":{"code":"secret-code","message":"secret-token-never-persist"}}`)
		})
		if requests != 8 {
			t.Fatalf("unexpected request count (retries must be disabled): %d", requests)
		}
		body, _ := json.Marshal(report)
		if strings.Contains(string(body), "secret-") || !strings.Contains(string(body), "HTTP 429") {
			t.Fatalf("unsafe or missing error diagnostics: %s", body)
		}
		for _, metric := range report.Resources[0].Metrics {
			if metric.Error == "" {
				t.Fatalf("failed request not marked: %+v", metric)
			}
		}
	})
	t.Run("credential error", func(t *testing.T) {
		report := collectAMW(context.Background(), amwTestCredential{t: t, err: errors.New("secret-credential")}, []string{amwTestWorkspace}, amwTestStart, amwTestEnd, amwTestTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("credential failure reached transport")
			return nil, nil
		}))
		body, _ := json.Marshal(report)
		if len(report.Errors) == 0 || strings.Contains(string(body), "secret-") {
			t.Fatalf("unsafe credential error report: %s", body)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		requests := 0
		report := collectAMW(ctx, amwTestCredential{t: t}, []string{amwTestWorkspace}, amwTestStart, amwTestEnd, amwTestTransport(func(request *http.Request) (*http.Response, error) {
			requests++
			cancel()
			return nil, request.Context().Err()
		}))
		if requests != 1 || !strings.Contains(strings.Join(report.Errors, " "), "canceled") {
			t.Fatalf("cancellation not honored: requests=%d errors=%v", requests, report.Errors)
		}
		for _, metric := range report.Resources[0].Metrics {
			if metric.Error == "" {
				t.Fatalf("canceled metric not marked: %+v", metric)
			}
		}
	})
	t.Run("parent deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		requests := 0
		report := collectAMW(ctx, amwTestCredential{t: t}, []string{amwTestWorkspace}, amwTestStart, amwTestEnd, amwTestTransport(func(request *http.Request) (*http.Response, error) {
			requests++
			<-request.Context().Done()
			return nil, request.Context().Err()
		}))
		if requests > 1 || !strings.Contains(strings.Join(report.Errors, " "), "deadline exceeded") {
			t.Fatalf("parent deadline not honored: requests=%d errors=%v", requests, report.Errors)
		}
	})
	t.Run("partial discovery", func(t *testing.T) {
		pages := 0
		report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
			if strings.HasSuffix(request.URL.Path, "/dataCollectionRules") {
				pages++
				if pages == 2 {
					return amwTestResponse(request, 403, `{"error":{"message":"secret-error"}}`)
				}
				return amwTestResponse(request, 200, fmt.Sprintf(`{"value":[{"id":%q,"properties":{"destinations":{"monitoringAccounts":[{"accountResourceId":%q}]}}}],"nextLink":"https://management.azure.com/subscriptions/sub-a/providers/Microsoft.Insights/dataCollectionRules?page=2"}`, amwTestDCR, amwTestWorkspace))
			}
			if strings.HasSuffix(request.URL.Path, "/metrics") {
				return amwTestResponse(request, 200, amwTestMetricBody(request))
			}
			return amwTestResponse(request, 200, `{"value":[]}`)
		})
		if pages != 2 || len(report.Resources) != 2 || len(report.Discovery[0].Responses) != 1 || !strings.Contains(report.Discovery[0].Error, "HTTP 403") {
			t.Fatalf("partial discovery not retained: %+v", report)
		}
	})
	t.Run("blocked pagination host", func(t *testing.T) {
		requests := 0
		report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
			requests++
			if strings.HasSuffix(request.URL.Path, "/dataCollectionRules") {
				return amwTestResponse(request, 200, `{"value":[],"nextLink":"https://example.prometheus.monitor.azure.com/api/v1/query"}`)
			}
			if strings.HasSuffix(request.URL.Path, "/metrics") {
				return amwTestResponse(request, 200, amwTestMetricBody(request))
			}
			return amwTestResponse(request, 200, `{"value":[]}`)
		})
		if requests != 8 || report.Discovery[0].Error == "" {
			t.Fatalf("unsafe next link was not blocked: %d %+v", requests, report.Discovery)
		}
	})
}

func TestAMWCollectMissingWorkspace(t *testing.T) {
	for _, o := range []Options{{}, {completedOptions: &completedOptions{workspaceErrors: map[string]error{"svc": errors.New("secret-error")}}}} {
		report := o.collectAMW(context.Background())
		if len(report.Errors) == 0 {
			t.Fatal("missing workspace/options errors not surfaced")
		}
		if strings.Contains(strings.Join(report.Errors, " "), "secret-") {
			t.Fatal("workspace discovery error leaked")
		}
		if o.completedOptions != nil && !strings.Contains(strings.Join(report.Errors, " "), "workspace svc") {
			t.Fatal("workspaceErrors not surfaced")
		}
	}
	for _, ids := range [][]string{nil, {"https://prometheus.example/api/v1/query"}, {"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm"}} {
		report := amwTestCollect(t, ids, func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid/missing workspace made a request")
			return nil, nil
		})
		if len(report.Errors) == 0 {
			t.Fatal("invalid/missing workspace accepted")
		}
	}
	// Nil credentials must be reported, not panic, while retaining planned metrics.
	report := collectAMW(context.Background(), nil, []string{amwTestWorkspace}, amwTestStart, amwTestEnd, nil)
	if len(report.Errors) == 0 || len(report.Resources[0].Metrics) != 6 {
		t.Fatalf("nil credentials not handled defensively: %+v", report)
	}
}

func TestAMWCollectRawPartialMetric(t *testing.T) {
	response := &armmonitor.MetricsClientListResponse{Response: armmonitor.Response{
		Interval: to.Ptr("PT1M"),
		Value: []*armmonitor.Metric{{
			Name:      &armmonitor.LocalizableString{Value: to.Ptr("ActiveTimeSeries")},
			ErrorCode: to.Ptr("PartialFailure"),
			Timeseries: []*armmonitor.TimeSeriesElement{{
				Metadatavalues: []*armmonitor.MetadataValue{{Name: &armmonitor.LocalizableString{Value: to.Ptr("StampColor")}, Value: to.Ptr("blue")}},
				Data:           []*armmonitor.MetricValue{{TimeStamp: to.Ptr(amwTestStart), Maximum: to.Ptr(float64(0))}},
			}},
		}},
	}}
	metric := amwMetric{Name: "ActiveTimeSeries", Aggregation: "Maximum", Response: response}
	if err := amwValidateMetric(metric, []string{"StampColor"}); !strings.Contains(err, "individual metric error") {
		t.Fatalf("partial metric error ignored: %s", err)
	}
	if metric.Response != response || *response.Value[0].Timeseries[0].Data[0].Maximum != 0 {
		t.Fatal("partial response modified")
	}
}

func TestAMWCollectWorkspacePriority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metrics, requests := 0, 0
	report := collectAMW(ctx, amwTestCredential{t: t}, []string{amwTestWorkspace}, amwTestStart, amwTestEnd, amwTestTransport(func(request *http.Request) (*http.Response, error) {
		requests++
		if strings.HasSuffix(request.URL.Path, "/metrics") {
			metrics++
			return amwTestResponse(request, 200, amwTestMetricBody(request))
		}
		if !strings.HasSuffix(request.URL.Path, "/dataCollectionRules") || metrics != 6 {
			t.Fatalf("supplementary collection started before workspace metrics: %s, metrics=%d", request.URL.Path, metrics)
		}
		cancel()
		return nil, context.Canceled
	}))
	if requests != 7 || len(report.Discovery) != 1 || !strings.Contains(report.Discovery[0].Error, "canceled") {
		t.Fatalf("discovery cancellation not surfaced or more requests started: requests=%d discovery=%+v", requests, report.Discovery)
	}
	for _, metric := range report.Resources[0].Metrics {
		if metric.Error != "" || metric.Response == nil {
			t.Fatalf("discovery failure lost core metric: %+v", metric)
		}
	}
	if report.Resources[0].Definitions != nil {
		t.Fatal("definitions should not be requested after cancellation")
	}
}

func TestAMWCollectResponseLimits(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(fmt.Sprintf("response HTTP %d", status), func(t *testing.T) {
			oversized := bytes.NewReader(bytes.Repeat([]byte(" "), 2*amwMaxResponseBytes))
			report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
				if request.URL.Query().Get("metricnames") == "ActiveTimeSeries" {
					response, _ := amwTestResponse(request, status, "")
					response.Body = io.NopCloser(oversized)
					return response, nil
				}
				if strings.HasSuffix(request.URL.Path, "/metrics") {
					return amwTestResponse(request, 200, amwTestMetricBody(request))
				}
				return amwTestResponse(request, 200, `{"value":[]}`)
			})
			if oversized.Len() != amwMaxResponseBytes-1 {
				t.Fatalf("reader consumed more than the response cap plus one probe byte: unread=%d", oversized.Len())
			}
			for _, metric := range report.Resources[0].Metrics {
				if metric.Name == "ActiveTimeSeries" {
					if !strings.Contains(metric.Error, "8 MiB byte cap") {
						t.Fatalf("oversized response not surfaced: %+v", metric)
					}
				} else if metric.Error != "" {
					t.Fatalf("oversized response prevented subsequent core metric: %+v", metric)
				}
			}
		})
	}
	for _, size := range []int{amwMaxResponseBytes, amwMaxResponseBytes - 1} {
		t.Run(fmt.Sprintf("total with %d byte responses", size), func(t *testing.T) {
			requests := 0
			report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
				requests++
				body := amwTestMetricBody(request)
				padded := append([]byte(body), bytes.Repeat([]byte(" "), size-len(body))...)
				response, _ := amwTestResponse(request, 200, "")
				response.Body = io.NopCloser(bytes.NewReader(padded))
				return response, nil
			})
			wantRequests := 4
			if size < amwMaxResponseBytes {
				wantRequests++
			}
			if requests != wantRequests || !strings.Contains(strings.Join(report.Errors, " "), "32 MiB") {
				t.Fatalf("total byte limit not enforced: requests=%d errors=%v", requests, report.Errors)
			}
			for i, metric := range report.Resources[0].Metrics {
				if i < 4 && (metric.Error != "" || metric.Response == nil) {
					t.Fatalf("under-limit response lost: %+v", metric)
				}
				if i >= 4 && !strings.Contains(metric.Error, "32 MiB") {
					t.Fatalf("exhausted budget not surfaced on metric: %+v", metric)
				}
			}
		})
	}
}

func TestAMWCollectDefinitionContinuation(t *testing.T) {
	definitions := 0
	report := amwTestCollect(t, []string{amwTestWorkspace}, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/metricDefinitions") {
			definitions++
			return amwTestResponse(request, 200, `{"value":[{"name":{"value":"evidence"}}],"nextLink":"https://management.azure.com/secret-continuation"}`)
		}
		if strings.HasSuffix(request.URL.Path, "/metrics") {
			return amwTestResponse(request, 200, amwTestMetricBody(request))
		}
		return amwTestResponse(request, 200, `{"value":[]}`)
	})
	if definitions != 1 || report.Resources[0].Definitions == nil || len(report.Resources[0].Definitions.Value) != 1 {
		t.Fatalf("first definition page not retained: %+v", report.Resources[0])
	}
	if !strings.Contains(strings.Join(report.Errors, " "), "further pages unknown") || strings.Contains(strings.Join(report.Errors, " "), "secret-continuation") {
		t.Fatalf("continuation not safely surfaced: %v", report.Errors)
	}
}
