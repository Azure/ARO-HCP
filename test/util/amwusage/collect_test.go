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

package amwusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const collectionTestID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test/providers/Microsoft.Monitor/accounts/test"

const collectionTestContext = `{"schemaVersion":1,"run":{"start":"2026-09-21T04:25:45Z","end":"2026-09-21T06:43:40Z","prow":"https://prow.example/run"},"baseline":{"start":"2026-09-21T03:30:00Z","end":"2026-09-21T04:00:00Z","reason":"Verified in supplied evidence","sources":["https://example.com/baseline"]},"clusters":[{"id":"cluster-id","name":"cluster-name","resourceId":"/subscriptions/test/resourceGroups/test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-name","namespaces":["hcp-test"],"sourceURLs":["https://example.com/ownership"]}],"limitations":["Not universal inactivity"],"extraEvidence":{"keep":true}}`

type collectionTestCredential struct {
	scopes  []string
	err     error
	onToken func()
}

func (c *collectionTestCredential) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.scopes = append(c.scopes, o.Scopes...)
	if c.onToken != nil {
		c.onToken()
	}
	return azcore.AccessToken{Token: "SECRET-TOKEN", ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

type collectionTestTransport func(*http.Request) (*http.Response, error)

func (f collectionTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func collectionTestOptions(t *testing.T) CollectOptions {
	t.Helper()
	start := time.Date(2026, 9, 21, 4, 25, 45, 0, time.UTC)
	return CollectOptions{Workspaces: []string{collectionTestID}, Start: start, End: start.Add(8275 * time.Second), Output: filepath.Join(t.TempDir(), "data.json"), Credential: &collectionTestCredential{}}
}

func collectionTestResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"X-Ms-Request-Id": {"request-id"}, "Set-Cookie": {"sensitive-cookie"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func collectionTestBody(r *http.Request) string {
	switch {
	case r.URL.Path == collectionTestID:
		return `{"properties":{"metrics":{"prometheusQueryEndpoint":"https://test.westus3.prometheus.monitor.azure.com"}}}`
	case strings.HasSuffix(r.URL.Path, "/values"):
		return `{"status":"success","data":["up","kube_pod_info"]}`
	case strings.HasSuffix(r.URL.Path, "/metrics"):
		return `{"cost":158,"value":[]}`
	case strings.HasSuffix(r.URL.Path, "/query_range"):
		return `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	default:
		return `{"status":"success","data":{"resultType":"vector","result":[]}}`
	}
}

func collectionReadData(t *testing.T, path string) collectionData {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SECRET-TOKEN") || strings.Contains(string(raw), "sensitive-cookie") {
		t.Fatal("artifact leaked authentication material")
	}
	var data collectionData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCollectQueryShapeAndCheckpoint(t *testing.T) {
	t.Parallel()
	o := collectionTestOptions(t)
	o.Metrics = []string{"up"}
	o.Context = json.RawMessage(collectionTestContext)
	count := 0
	instantQueries := map[string]bool{
		"count by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up[8275000ms]))":                                                        false,
		"count by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up[8275000ms]) unless count_over_time(up[1800000ms] offset 9820000ms))": false,
		"sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up[8275000ms]))":                                                          false,
		"count by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up[1800000ms]))":                                                        false,
		"sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up[1800000ms]))":                                                          false,
	}
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		data := collectionReadData(t, o.Output)
		if len(data.Records) != count || data.Complete {
			t.Fatal("checkpoint missing preceding response or marked complete early")
		}
		count++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer SECRET-TOKEN" {
			t.Fatal("expected authenticated GET")
		}
		q := r.URL.Query()
		switch {
		case strings.HasSuffix(r.URL.Path, "/metrics"):
			if q.Get("interval") != "PT1M" || q.Get("aggregation") != "Maximum" || q.Get("timespan") != "2026-09-21T03:30:00Z/2026-09-21T06:54:00Z" {
				t.Fatalf("wrong platform query: %s", r.URL)
			}
			if strings.HasSuffix(q.Get("metricnames"), "Dropped") && (q.Get("top") != "100" || q.Has("$top") || q.Get("$filter") != "Reason eq '*'") {
				t.Fatal("drop query does not match working API")
			}
		case strings.HasSuffix(r.URL.Path, "/query_range"):
			if q.Get("query") != "sum by(cluster,job,namespace,hostedcontrolplane,prometheus)(count_over_time(up[5m]))/5" || q.Get("start") != "2026-09-21T04:30:45Z" || q.Get("end") != "2026-09-21T06:43:40Z" || q.Get("step") != "300" || q.Get("timeout") != "90s" {
				t.Fatalf("wrong range query: %s", r.URL)
			}
		case strings.HasSuffix(r.URL.Path, "/query"):
			seen, expected := instantQueries[q.Get("query")]
			at := "2026-09-21T06:43:40Z"
			if !strings.Contains(q.Get("query"), "8275000ms") {
				at = "2026-09-21T04:00:00Z"
			}
			if !expected || seen || q.Get("time") != at || q.Get("timeout") != "90s" {
				t.Fatalf("wrong instant query: %s", r.URL)
			}
			instantQueries[q.Get("query")] = true
		}
		return collectionTestResponse(collectionTestBody(r), 200), nil
	})
	summary, err := Collect(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 14 || summary.Platform != 6 || summary.PromQL != 6 || summary.Selected != 1 {
		t.Fatalf("wrong summary: %+v", summary)
	}
	data := collectionReadData(t, o.Output)
	if !data.Complete || data.SchemaVersion != 1 || data.Run.DurationMS != 8275000 || len(data.Manifests) != 14 || len(data.Workspaces[0].Metrics) != 1 || data.Run.Prow != "https://prow.example/run" {
		t.Fatalf("wrong artifact: %+v", data)
	}
	entry := data.Workspaces[0].Metrics[0]
	for id, query := range map[string]string{entry.NewSeries: entry.NewSeriesQuery, entry.Samples: entry.SamplesQuery, entry.BaselineSeries: entry.BaselineSeriesQuery, entry.BaselineSamples: entry.BaselineSamplesQuery} {
		if record := data.Records[id]; record == nil || !record.OK || !instantQueries[query] {
			t.Fatalf("successful empty result or query reference missing: %q %q", id, query)
		}
	}
	var original, preserved any
	if err := json.Unmarshal(o.Context, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data.Context, &preserved); err != nil || !reflect.DeepEqual(original, preserved) {
		t.Fatalf("context not fully preserved: %v", err)
	}
	raw, err := os.ReadFile(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	if err := Render(io.Discard, raw); err != nil {
		t.Fatalf("collected schema cannot be rendered: %v", err)
	}
	cred := o.Credential.(*collectionTestCredential)
	if len(cred.scopes) != 2 || cred.scopes[0] != "https://management.azure.com/.default" || cred.scopes[1] != "https://prometheus.monitor.azure.com/.default" {
		t.Fatalf("wrong token scopes/cache: %v", cred.scopes)
	}
	if _, err := Collect(context.Background(), o); err == nil {
		t.Fatal("existing evidence was overwritten")
	}
}

func TestCollectDiscoveryOnlyAndMissingSelection(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "discovery-only", true: "missing-selection"}[missing], func(t *testing.T) {
			t.Parallel()
			o := collectionTestOptions(t)
			if missing {
				o.Metrics = []string{"not_discovered"}
			}
			o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				return collectionTestResponse(collectionTestBody(r), 200), nil
			})
			summary, err := Collect(context.Background(), o)
			if (err != nil) != missing || summary.Requests != 8 || summary.PromQL != 0 {
				t.Fatalf("unexpected selection behavior: %+v, %v", summary, err)
			}
			data := collectionReadData(t, o.Output)
			if missing && (len(data.Errors) == 0 || data.Complete) {
				t.Fatal("missing selection not recorded")
			}
		})
	}
}

func TestCollectFailedQueryPreserved(t *testing.T) {
	t.Parallel()
	o := collectionTestOptions(t)
	o.Metrics = []string{"up"}
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/query_range") {
			return collectionTestResponse(`{"status":"error","error":"Query has exceeded the timeseries per metric limit"}`, 429), nil
		}
		return collectionTestResponse(collectionTestBody(r), 200), nil
	})
	summary, err := Collect(context.Background(), o)
	if err == nil || summary.Failed != 1 || summary.Requests != 11 {
		t.Fatalf("failed query was hidden or retried: %+v %v", summary, err)
	}
	data := collectionReadData(t, o.Output)
	record := data.Records["test-up-range"]
	if record.OK || record.Status != 429 || !strings.Contains(record.Body, "timeseries per metric limit") || record.ResponseBytes != len(record.Body) {
		t.Fatalf("failure envelope lost: %+v", record)
	}
}

func TestCollectFailedAdditionalQueriesPreserved(t *testing.T) {
	t.Parallel()
	o := collectionTestOptions(t)
	o.Metrics = []string{"up"}
	o.Context = json.RawMessage(collectionTestContext)
	const body = `{"status":"error","errorType":"bad_data","error":"unsupported query syntax"}`
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/query") {
			query := r.URL.Query().Get("query")
			if r.URL.Query().Get("time") == "2026-09-21T04:00:00Z" {
				return collectionTestResponse(body, 429), nil
			}
			if strings.Contains(query, " unless ") {
				return collectionTestResponse(body, 400), nil
			}
			if strings.HasPrefix(query, "sum by(") {
				return collectionTestResponse(body, 200), nil
			}
		}
		return collectionTestResponse(collectionTestBody(r), 200), nil
	})
	summary, err := Collect(context.Background(), o)
	if err == nil || summary.Failed != 4 || summary.Requests != 14 || summary.PromQL != 6 {
		t.Fatalf("additional query failures hidden or retried: %+v %v", summary, err)
	}
	data := collectionReadData(t, o.Output)
	entry := data.Workspaces[0].Metrics[0]
	for id, status := range map[string]int{entry.NewSeries: 400, entry.Samples: 200, entry.BaselineSeries: 429, entry.BaselineSamples: 429} {
		record := data.Records[id]
		if record == nil || record.OK || record.Error == "" || record.Status != status || record.Body != body {
			t.Fatalf("additional query failure envelope lost: %+v", record)
		}
	}
}

func TestCollectUntrustedEndpointAndRedirect(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "untrusted-endpoint", true: "redirect"}[redirect], func(t *testing.T) {
			t.Parallel()
			o := collectionTestOptions(t)
			o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "management.azure.com" {
					t.Fatal("token forwarded outside ARM")
				}
				if r.URL.Path == collectionTestID {
					if redirect {
						response := collectionTestResponse(`{}`, 302)
						response.Header.Set("Location", "https://evil.example/")
						return response, nil
					}
					return collectionTestResponse(`{"properties":{"metrics":{"prometheusQueryEndpoint":"https://evil.example/"}}}`, 200), nil
				}
				return collectionTestResponse(collectionTestBody(r), 200), nil
			})
			summary, err := Collect(context.Background(), o)
			if err == nil || summary.Failed != 1 || summary.Requests != 7 {
				t.Fatalf("unexpected endpoint rejection: %+v %v", summary, err)
			}
		})
	}
}

func TestCollectCancellationAndCredentialFailure(t *testing.T) {
	o := collectionTestOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancellation during token acquisition must retain that failed envelope.
	// No cloud transport is ever invoked.
	o.Credential = &collectionTestCredential{err: errors.New("SECRET-TOKEN"), onToken: cancel}
	summary, err := Collect(ctx, o)
	if err == nil || summary.Failed != 1 {
		t.Fatalf("credential failure not retained: %+v %v", summary, err)
	}
	data := collectionReadData(t, o.Output)
	if data.Complete || len(data.Records) != 1 || len(data.Errors) == 0 {
		t.Fatal("cancellation checkpoint missing")
	}
}

func TestCollectValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CollectOptions)
	}{
		{"wrong-resource", func(o *CollectOptions) {
			o.Workspaces = []string{strings.Replace(collectionTestID, "Microsoft.Monitor/accounts", "Microsoft.Compute/accounts", 1)}
		}},
		{"url-injection", func(o *CollectOptions) { o.Workspaces[0] += "?secret" }},
		{"duplicate-workspace", func(o *CollectOptions) { o.Workspaces = append(o.Workspaces, o.Workspaces[0]) }},
		{"no-window", func(o *CollectOptions) { o.Start = time.Time{} }},
		{"long-window", func(o *CollectOptions) { o.End = o.Start.Add(12*time.Hour + time.Second) }},
		{"short-window", func(o *CollectOptions) { o.End = o.Start.Add(time.Minute) }},
		{"fractional-window", func(o *CollectOptions) { o.End = o.End.Add(time.Millisecond) }},
		{"metric-expression", func(o *CollectOptions) { o.Metrics = []string{`up{job="x"}`} }},
		{"duplicate-metric", func(o *CollectOptions) { o.Metrics = []string{"up", "up"} }},
		{"too-many-metrics", func(o *CollectOptions) {
			for i := range 13 {
				o.Metrics = append(o.Metrics, fmt.Sprintf("metric_%d", i))
			}
		}},
		{"unknown-selection-key", func(o *CollectOptions) { o.Selection = map[string][]string{"typo": {"up"}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := collectionTestOptions(t)
			test.mutate(&o)
			if err := o.Validate(); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}

func TestCollectGlobalSelectionAndCaps(t *testing.T) {
	for _, mode := range []string{"matched-per-workspace", "per-workspace-cap", "total-cap"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			o := collectionTestOptions(t)
			o.Workspaces = []string{collectionTestID, collectionTestID + "2"}
			o.Metrics = []string{"up", "kube_pod_info"}
			if mode == "per-workspace-cap" {
				o.Metrics = []string{"a", "b", "c", "d", "e", "f"}
			}
			if mode == "total-cap" {
				o.Workspaces = append(o.Workspaces, collectionTestID+"3")
				o.Metrics = []string{"a", "b", "c", "d", "e"}
			}
			o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/accounts/") && !strings.HasSuffix(r.URL.Path, "/metrics") {
					name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					return collectionTestResponse(fmt.Sprintf(`{"properties":{"metrics":{"prometheusQueryEndpoint":"https://%s.westus3.prometheus.monitor.azure.com"}}}`, name), 200), nil
				}
				if strings.HasSuffix(r.URL.Path, "/values") {
					if mode != "matched-per-workspace" {
						return collectionTestResponse(`{"status":"success","data":["a","b","c","d","e","f"]}`, 200), nil
					}
					if strings.HasPrefix(r.URL.Host, "test2.") {
						return collectionTestResponse(`{"status":"success","data":["up"]}`, 200), nil
					}
				}
				return collectionTestResponse(collectionTestBody(r), 200), nil
			})
			summary, err := Collect(context.Background(), o)
			if mode == "matched-per-workspace" {
				if err != nil || summary.PromQL != 9 || summary.Selected != 3 {
					t.Fatalf("global selection not matched per workspace: %+v %v", summary, err)
				}
			} else if err == nil || summary.PromQL != 0 {
				t.Fatalf("over-cap selection sent PromQL: %+v %v", summary, err)
			}
		})
	}
}

func TestCollectWithoutBaseline(t *testing.T) {
	for _, withContext := range []bool{false, true} {
		t.Run(fmt.Sprint(withContext), func(t *testing.T) {
			t.Parallel()
			o := collectionTestOptions(t)
			o.Metrics = []string{"up"}
			if withContext {
				var c map[string]json.RawMessage
				if err := json.Unmarshal([]byte(collectionTestContext), &c); err != nil {
					t.Fatal(err)
				}
				delete(c, "baseline")
				var err error
				o.Context, err = json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
			}
			o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Query().Get("query"), "unless") || strings.Contains(r.URL.Query().Get("query"), "offset") {
					t.Fatal("implicit baseline queried")
				}
				if strings.HasSuffix(r.URL.Path, "/metrics") && r.URL.Query().Get("timespan") != "2026-09-21T04:15:00Z/2026-09-21T06:54:00Z" {
					t.Fatal("platform window expanded without baseline")
				}
				return collectionTestResponse(collectionTestBody(r), 200), nil
			})
			summary, err := Collect(context.Background(), o)
			if err != nil || summary.Requests != 11 || summary.PromQL != 3 {
				t.Fatalf("wrong no-baseline plan: %+v %v", summary, err)
			}
			m := collectionReadData(t, o.Output).Workspaces[0].Metrics[0]
			if m.NewSeries != "" || m.NewSeriesQuery != "" || m.BaselineSeries != "" || m.BaselineSeriesQuery != "" || m.BaselineSamples != "" || m.BaselineSamplesQuery != "" {
				t.Fatalf("implicit baseline references: %+v", m)
			}
		})
	}
}

func TestCollectContextValidation(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"schema", `"schemaVersion":1`, `"schemaVersion":2`},
		{"run-start", "04:25:45Z", "04:25:46Z"},
		{"run-end", "06:43:40Z", "06:43:41Z"},
		{"baseline-overlap", "04:00:00Z", "04:26:00Z"},
		{"baseline-too-short", "03:30:00Z", "03:59:00Z"},
		{"baseline-too-long", "2026-09-21T03:30:00Z", "2026-09-20T23:00:00Z"},
		{"baseline-fraction", "03:30:00Z", "03:30:00.001Z"},
		{"baseline-gap", `"start":"2026-09-21T03:30:00Z","end":"2026-09-21T04:00:00Z"`, `"start":"2026-09-20T03:30:00Z","end":"2026-09-20T04:00:00Z"`},
		{"baseline-old", `"start":"2026-09-21T03:30:00Z","end":"2026-09-21T04:00:00Z"`, `"start":"2026-08-01T03:30:00Z","end":"2026-08-01T04:00:00Z"`},
		{"baseline-reason", "Verified in supplied evidence", " "},
		{"baseline-source", `["https://example.com/baseline"]`, `[]`},
		{"cluster-source", `["https://example.com/ownership"]`, `[]`},
		{"cluster-id", `"id":"cluster-id"`, `"id":""`},
		{"cluster-resource", `"resourceId":"/subscriptions/test`, `"resourceId":"invalid`},
		{"namespace", `"hcp-test"`, `"invalid_namespace"`},
		{"duplicate-namespace", `["hcp-test"]`, `["hcp-test","hcp-test"]`},
		{"unsafe-source", "https://example.com/baseline", "https://user:password@example.com/baseline"},
		{"missing-prow", "https://prow.example/run", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := collectionTestOptions(t)
			o.Context = json.RawMessage(strings.Replace(collectionTestContext, test.old, test.replacement, 1))
			if err := o.Validate(); err == nil {
				t.Fatal("invalid context accepted")
			}
		})
	}
	for _, raw := range []string{"null", "{}", "[]", "invalid"} {
		o := collectionTestOptions(t)
		o.Context = json.RawMessage(raw)
		if err := o.Validate(); err == nil {
			t.Fatalf("invalid context accepted: %s", raw)
		}
	}
	for _, mode := range []string{"duplicate-id", "duplicate-resource", "shared-namespace", "prow-conflict"} {
		t.Run(mode, func(t *testing.T) {
			o := collectionTestOptions(t)
			var c collectionContext
			if err := json.Unmarshal([]byte(collectionTestContext), &c); err != nil {
				t.Fatal(err)
			}
			if mode == "prow-conflict" {
				o.ProwURL = "https://prow.example/other"
			} else {
				other := c.Clusters[0]
				if mode != "duplicate-id" {
					other.ID = "other-id"
				}
				if mode != "duplicate-resource" {
					other.ResourceID = strings.Replace(other.ResourceID, "/resourceGroups/test/", "/resourceGroups/other/", 1)
				}
				if mode != "shared-namespace" {
					other.Namespaces = []string{"other-namespace"}
				}
				c.Clusters = append(c.Clusters, other)
			}
			var err error
			o.Context, err = json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if err := o.Validate(); err == nil {
				t.Fatal("conflicting context accepted")
			}
		})
	}
}
