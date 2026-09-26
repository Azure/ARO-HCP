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
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

var replicaPeakTestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func replicaPeakTestSeries(labels map[string]string, value any) PrometheusResult {
	return PrometheusResult{Metric: labels, Values: [][]any{{float64(replicaPeakTestTime.Unix()), value}}}
}

func replicaPeakTestSummary(labels map[string]string, maximum float64, requests bool) []PrometheusResult {
	stats := map[string]float64{"max": maximum, "count": 1, "first": float64(replicaPeakTestTime.Unix()), "last": float64(replicaPeakTestTime.Unix())}
	if requests {
		stats["min"] = maximum
	}
	var result []PrometheusResult
	for _, stat := range slices.Sorted(maps.Keys(stats)) {
		m := maps.Clone(labels)
		m["statistic"] = stat
		result = append(result, replicaPeakTestSeries(m, stats[stat]))
	}
	return result
}

func replicaPeakTestQuery(t *testing.T, data map[string][]PrometheusResult, failure string) utilizationQueryFunc {
	t.Helper()
	queries := replicaPeakQueries("cluster", replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime)
	var discovered atomic.Bool
	return func(ctx context.Context, ws, expression string, start, end time.Time) ([]PrometheusResult, error) {
		if !start.Equal(replicaPeakTestTime) || !end.Equal(start) || start.Location() != time.UTC {
			t.Errorf("expected a single UTC evaluation, got %s..%s", start, end)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Minute {
			t.Error("collector must bound its entire query budget")
		}
		if strings.Contains(expression, "underlay_clusters") {
			discovered.Store(true)
			if !strings.Contains(expression, `source="bicep"}[3600001ms]`) || !strings.Contains(expression, "max_over_time") {
				t.Errorf("inventory must cover the full window: %s", expression)
			}
			return []PrometheusResult{replicaPeakTestSeries(map[string]string{"cluster": "cluster"}, "1")}, nil
		}
		if !discovered.Load() {
			t.Error("container queries preceded discovery")
		}
		for _, q := range queries {
			if q.workspace == ws && q.expression == expression {
				if ws+"/"+q.name == failure {
					return data[ws+"/"+q.name], errors.New("query failed")
				}
				return data[ws+"/"+q.name], nil
			}
		}
		t.Errorf("unexpected query %s %s", ws, expression)
		return nil, nil
	}
}

func TestReplicaPeakIdentitiesAndSources(t *testing.T) {
	oldUID, newUID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	labels := func(uid string) map[string]string {
		return map[string]string{"cluster": "cluster", "namespace": "ns", "pod": "reused", "container": "app", "uid": uid, "resource": "cpu"}
	}
	usage := func(id string) map[string]string {
		m := labels("")
		delete(m, "uid")
		delete(m, "resource")
		m["id"], m["node"], m["instance"] = id, "node", "exporter"
		return m
	}
	data := map[string][]PrometheusResult{
		"svc/requests": replicaPeakTestSummary(labels(newUID), 0, true),
		"hcp/requests": replicaPeakTestSummary(labels(newUID), 2, true),
		"svc/cpu":      replicaPeakTestSummary(usage("/kubepods/pod"+oldUID+"/deleted"), 4, false),
		"svc/memory":   replicaPeakTestSummary(usage("/kubepods-burstable-pod"+strings.ReplaceAll(newUID, "-", "_")+".slice/cri-containerd-new.scope"), 100, false),
	}
	data["svc/cpu"] = append(data["svc/cpu"], replicaPeakTestSummary(usage("/other/cri-containerd-runtime.scope"), 3, false)...)
	data["svc/cpu"] = append(data["svc/cpu"], replicaPeakTestSummary(usage("/no-identity"), 5, false)...)
	info := labels(newUID)
	info["metric"], info["container_id"] = "kube_pod_container_info", "containerd://runtime"
	data["hcp/metadata"] = []PrometheusResult{replicaPeakTestSeries(info, "1")}
	for _, family := range []string{"kube_pod_owner", "kube_replicaset_owner", "kube_job_owner", "kube_replicationcontroller_owner"} {
		data["hcp/metadata"] = append(data["hcp/metadata"], replicaPeakTestSeries(map[string]string{"cluster": "cluster", "namespace": "ns", "metric": family, "owner_kind": "Deployment", "owner_name": "owner"}, "1"))
	}
	requestOnly := labels("request-only")
	requestOnly["container"] = "init"
	data["hcp/initRequests"] = replicaPeakTestSummary(requestOnly, 1, true)
	run := func() replicaPeakReport {
		return collectReplicaPeaks(context.Background(), replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, replicaPeakTestQuery(t, data, ""))
	}
	r := run()
	if len(r.Queries) != 10 || len(r.Containers) != 7 || len(r.Metadata) != 5 {
		t.Fatalf("unexpected artifact sizes: queries=%d containers=%d metadata=%d", len(r.Queries), len(r.Containers), len(r.Metadata))
	}
	for _, row := range r.Containers {
		want := newUID
		switch row.Labels["id"] {
		case "/kubepods/pod" + oldUID + "/deleted":
			want = oldUID
		case "/no-identity":
			want = ""
		}
		if row.QueryName == "initRequests" {
			want = "request-only"
		}
		if row.PodUID != want || row.Summary["count"] != 1 || row.Labels["statistic"] != "" {
			t.Errorf("identity/count changed: %+v", row)
		}
		if row.QueryName == "requests" {
			want := float64(0)
			if row.Workspace == workspaceHcp {
				want = 2
			}
			if row.Summary["max"] != want || row.Summary["min"] != want {
				t.Errorf("request sources were merged or zero dropped: %+v", row)
			}
		}
		if row.QueryName == "cpu" || row.QueryName == "memory" {
			if _, found := row.Summary["min"]; found {
				t.Errorf("fabricated usage statistic: %+v", row)
			}
		}
	}
	if !strings.Contains(strings.Join(r.Warnings, " "), "retained unmatched") {
		t.Fatal("missing identity must remain visible with a warning")
	}
	want, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for k, series := range data {
		series = append(slices.Clone(series), series...)
		slices.Reverse(series)
		data[k] = series
	}
	duplicate := run()
	// Query series counts intentionally describe the response, not deduped rows.
	for i := range duplicate.Queries {
		duplicate.Queries[i].Series = r.Queries[i].Series
	}
	got, _ := json.Marshal(duplicate)
	if string(got) != string(want) {
		t.Fatalf("duplicates/order changed compact evidence\nwant %s\ngot %s", want, got)
	}
}

func TestReplicaPeakSustainedCPU(t *testing.T) {
	uid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	for _, tc := range []struct {
		name, id, metadataUID, wantUID string
	}{
		{"cgroup", "/kubepods/pod" + uid + "/runtime", "", uid},
		{"systemd", "/kubepods-pod" + strings.ReplaceAll(uid, "-", "_") + ".slice/cri-containerd-runtime.scope", "", uid},
		{"runtime", "/cri-containerd-runtime.scope", uid, uid},
		{"unmatched", "/unknown", "", ""},
		{"conflicting", "/kubepods/pod" + uid + "/runtime", "other", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{"cluster": "cluster", "namespace": "ns", "pod": "pod", "container": "app", "node": "node", "instance": "exporter", "id": tc.id, "uid": "untrusted"}
			data := map[string][]PrometheusResult{
				"svc/cpu":          replicaPeakTestSummary(labels, 6, false),
				"svc/cpuSustained": replicaPeakTestSummary(labels, 1, false),
			}
			if tc.metadataUID != "" {
				info := maps.Clone(labels)
				info["metric"], info["container_id"], info["uid"] = "kube_pod_container_info", "containerd://runtime", tc.metadataUID
				data["hcp/metadata"] = []PrometheusResult{replicaPeakTestSeries(info, "1")}
			}
			r := collectReplicaPeaks(context.Background(), replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, replicaPeakTestQuery(t, data, ""))
			if r.CPUWindow != "2m" || r.SizingCPUWindow != "10m" || len(r.Containers) != 2 {
				t.Fatalf("CPU windows or independent records lost: %+v", r)
			}
			for _, row := range r.Containers {
				maximum := float64(6)
				if row.QueryName == "cpuSustained" {
					maximum = 1
				}
				if row.PodUID != tc.wantUID || !maps.Equal(row.Labels, labels) || len(row.Summary) != 4 || row.Summary["max"] != maximum {
					t.Errorf("CPU identity or summary changed: %+v", row)
				}
				if _, found := row.Summary["min"]; found {
					t.Errorf("usage must not have a minimum: %+v", row)
				}
			}
			if strings.Contains(strings.Join(r.Warnings, " "), "summary incomplete") {
				t.Fatalf("four usage statistics should be complete: %v", r.Warnings)
			}
			bad := maps.Clone(labels)
			bad["statistic"] = "min"
			data["svc/cpuSustained"] = append(data["svc/cpuSustained"], replicaPeakTestSeries(bad, 0))
			r = collectReplicaPeaks(context.Background(), replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, replicaPeakTestQuery(t, data, ""))
			for _, q := range r.Queries {
				if q.Name == "cpuSustained" && q.Status != "error" {
					t.Errorf("sustained CPU minimum must be rejected: %+v", q)
				}
			}
			for _, row := range r.Containers {
				if len(row.Summary) != 4 {
					t.Errorf("invalid minimum leaked into usage: %+v", row)
				}
			}
		})
	}
}

func TestReplicaPeakCPUWindowJSON(t *testing.T) {
	for _, window := range []string{"", "10m"} {
		r := replicaPeakReport{CPUWindow: "2m", SizingCPUWindow: window}
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"sizingCPUWindow"`) != (window != "") {
			t.Fatalf("legacy reports must omit sizingCPUWindow: %s", data)
		}
		var decoded replicaPeakReport
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.CPUWindow != "2m" || decoded.SizingCPUWindow != window {
			t.Fatalf("CPU windows failed round trip: %+v", decoded)
		}
	}
}

func TestReplicaPeakPartialAndMalformed(t *testing.T) {
	m := map[string]string{"cluster": "cluster", "namespace": "ns", "pod": "pod", "container": "app", "uid": "uid", "resource": "cpu"}
	data := map[string][]PrometheusResult{"svc/requests": replicaPeakTestSummary(m, 2, true), "svc/cpu": replicaPeakTestSummary(m, 9, false)}
	for _, value := range []any{"NaN", "+Inf", "-1", "nonsense"} {
		bad := maps.Clone(m)
		bad["statistic"] = "max"
		data["svc/requests"] = append(data["svc/requests"], replicaPeakTestSeries(bad, value))
	}
	data["svc/requests"] = append(data["svc/requests"], PrometheusResult{Metric: m})
	for _, sample := range [][]any{{"NaN", "2"}, {float64(replicaPeakTestTime.Unix()) + 0.5, "2"}, {float64(replicaPeakTestTime.Unix() - 60), "2"}, {"bad"}} {
		bad := maps.Clone(m)
		bad["statistic"] = "max"
		data["svc/requests"] = append(data["svc/requests"], PrometheusResult{Metric: bad, Values: [][]any{sample}})
	}
	r := collectReplicaPeaks(context.Background(), replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, replicaPeakTestQuery(t, data, "svc/cpu"))
	if len(r.Containers) != 1 || r.Containers[0].Summary["max"] != 2 {
		t.Fatalf("failed query must not leak partial results; valid observations must survive: %+v", r.Containers)
	}
	statuses := map[string]string{}
	for _, q := range r.Queries {
		statuses[q.Workspace+"/"+q.Name] = q.Status
	}
	if statuses["svc/inventory"] != "success" || statuses["svc/cpu"] != "error" || statuses["svc/requests"] != "error" || statuses["hcp/requests"] != "empty" {
		t.Fatalf("query status distinctions lost: %v", statuses)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatalf("invalid observations poisoned JSON: %v", err)
	}
}

func TestReplicaPeakWindowsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end time.Time
	}{
		{"zero", time.Time{}, replicaPeakTestTime},
		{"reversed", replicaPeakTestTime, replicaPeakTestTime.Add(-time.Minute)},
		{"no grid", replicaPeakTestTime.Add(-30 * time.Second), replicaPeakTestTime.Add(-time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := collectReplicaPeaks(context.Background(), tc.start, tc.end, replicaPeakTestTime, func(context.Context, string, string, time.Time, time.Time) ([]PrometheusResult, error) {
				t.Error("invalid window issued a query")
				return nil, nil
			})
			if len(r.Warnings) == 0 {
				t.Fatal("invalid window needs a warning")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := collectReplicaPeaks(ctx, replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, func(context.Context, string, string, time.Time, time.Time) ([]PrometheusResult, error) {
		t.Error("canceled collector issued a query")
		return nil, nil
	})
	if len(r.Queries) != 1 || r.Queries[0].Status != "error" || !strings.Contains(strings.Join(r.Warnings, " "), "context canceled") {
		t.Fatalf("cancellation lost: %+v", r)
	}
	var calls atomic.Int32
	query := replicaPeakTestQuery(t, nil, "")
	r = collectReplicaPeaks(context.Background(), replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime.Add(time.Hour), replicaPeakTestTime, func(ctx context.Context, ws, expression string, start, end time.Time) ([]PrometheusResult, error) {
		calls.Add(1)
		return query(ctx, ws, expression, start, end)
	})
	if !r.End.Equal(replicaPeakTestTime) || calls.Load() != 10 || !strings.Contains(strings.Join(r.Warnings, " "), "no container observations") {
		t.Fatalf("capping/empty cluster failed: %+v", r)
	}
}

func TestReplicaPeakConflictingEvidence(t *testing.T) {
	uid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	for _, cgroup := range []bool{false, true} {
		t.Run(fmt.Sprint(cgroup), func(t *testing.T) {
			m := map[string]string{"cluster": "cluster", "namespace": "ns", "pod": "reused", "container": "app", "id": "/runtime"}
			if cgroup {
				m["id"] = "/pod" + uid + "/runtime"
			}
			data := map[string][]PrometheusResult{"svc/cpu": replicaPeakTestSummary(m, 2, false)}
			data["svc/cpu"] = append(data["svc/cpu"], replicaPeakTestSummary(m, 3, false)...)
			for _, candidate := range []string{uid, "other", uid} {
				info := maps.Clone(m)
				info["metric"], info["uid"], info["container_id"] = "kube_pod_container_info", candidate, "containerd://runtime"
				data["svc/metadata"] = append(data["svc/metadata"], replicaPeakTestSeries(info, "1"))
			}
			var previous string
			for pass := 0; pass < 2; pass++ {
				r := collectReplicaPeaks(context.Background(), replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, replicaPeakTestQuery(t, data, ""))
				if len(r.Containers) != 1 || r.Containers[0].PodUID != "" {
					t.Fatalf("conflicting UID must not be resolved: %+v", r.Containers)
				}
				if _, exists := r.Containers[0].Summary["max"]; exists {
					t.Fatal("conflicting statistic must be withheld, not summed or selected by order")
				}
				b, _ := json.Marshal(r)
				if pass > 0 && previous != string(b) {
					t.Fatal("conflict resolution depends on response order")
				}
				previous = string(b)
				for _, series := range data {
					slices.Reverse(series)
				}
			}
		})
	}
}

func TestReplicaPeakCancellationDuringBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	r := collectReplicaPeaks(ctx, replicaPeakTestTime.Add(-time.Hour), replicaPeakTestTime, replicaPeakTestTime, func(ctx context.Context, ws, expression string, start, end time.Time) ([]PrometheusResult, error) {
		if strings.Contains(expression, "underlay_clusters") {
			return []PrometheusResult{replicaPeakTestSeries(map[string]string{"cluster": "cluster"}, "1")}, nil
		}
		calls.Add(1)
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if calls.Load() > 2 || len(r.Queries) != 10 {
		t.Fatalf("canceled batch should retain all statuses without launching more work: calls=%d queries=%d", calls.Load(), len(r.Queries))
	}
	for _, q := range r.Queries[1:] {
		if q.Status != "error" || !strings.Contains(q.Error, "canceled") {
			t.Errorf("cancellation missing from query status: %+v", q)
		}
	}
}

func TestReplicaPeakDiscoveryEmptyAndUnaligned(t *testing.T) {
	start, end := replicaPeakTestTime.Add(-time.Hour+10*time.Second), replicaPeakTestTime.Add(20*time.Second)
	var calls int
	r := collectReplicaPeaks(context.Background(), start, end, end, func(ctx context.Context, ws, expression string, from, to time.Time) ([]PrometheusResult, error) {
		calls++
		if !from.Equal(replicaPeakTestTime) || !to.Equal(from) || !strings.Contains(expression, "[3610001ms]") || !strings.Contains(expression, fmt.Sprintf("@ %d.000", end.Unix())) {
			t.Errorf("discovery must retain full window edges at one evaluation: %s %s..%s", expression, from, to)
		}
		return nil, nil
	})
	if calls != 1 || len(r.Clusters) != 0 || r.Queries[0].Status != "empty" || len(r.Warnings) == 0 {
		t.Fatalf("empty discovery must stop expensive queries: %+v calls=%d", r, calls)
	}
}

func TestReplicaPeakPrometheusWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" || r.URL.Query().Get("start") != r.URL.Query().Get("end") || r.URL.Query().Get("step") != "60s" || r.Header.Get("Authorization") == "" {
			t.Errorf("unexpected authenticated query_range request: %v", r)
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","warnings":["truncated"],"data":{"resultType":"matrix","result":[{"metric":{"cluster":"cluster"},"values":[[%d,"1"]]}]}}`, replicaPeakTestTime.Unix())
	}))
	defer server.Close()
	o := Options{completedOptions: &completedOptions{TimeWindow: timing.TimeWindow{Start: replicaPeakTestTime.Add(-time.Hour), End: replicaPeakTestTime}, cred: utilizationTestCredential{}}}
	r := o.collectReplicaPeaks(context.Background(), map[string]*workspaceData{workspaceSvc: {PromEndpoint: server.URL}})
	if len(r.Clusters) != 0 || r.Queries[0].Status != "error" || !strings.Contains(r.Queries[0].Error, "truncated") {
		t.Fatalf("truncated response presented as complete: %+v", r)
	}
	r = o.collectReplicaPeaks(context.Background(), nil)
	if len(r.Queries) != 1 || r.Queries[0].Status != "error" || !strings.Contains(r.Queries[0].Error, "endpoint unavailable") {
		t.Fatalf("missing workspace must leave an error artifact: %+v", r)
	}
}
