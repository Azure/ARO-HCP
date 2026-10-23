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
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func inventoryTestBody(labels ...map[string]string) []byte {
	rows := []any{}
	for _, labels := range labels {
		rows = append(rows, map[string]any{"metric": labels, "value": []any{1790000000, "12"}})
	}
	body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	return body
}

func TestAnalyzeInventory(t *testing.T) {
	before := inventoryTestBody(
		map[string]string{"POD": "A", "prometheus_replica": "r0", "le": "1"},
		map[string]string{"pod": "a", "prometheus_replica": "r1", "le": "1"},
		map[string]string{"pod": "a", "prometheus_replica": "r0", "le": "2"},
	)
	end := inventoryTestBody(
		map[string]string{"pod": "a", "prometheus_replica": "R0", "le": "1"},
		map[string]string{"pod": "b", "prometheus_replica": "r0", "le": "1"},
	)
	result, err := AnalyzeInventory(before, end)
	if err != nil {
		t.Fatal(err)
	}
	if result.Common != 1 || result.Added != 1 || result.Removed != 2 || result.Before.Series != 3 || result.Before.WithoutReplica != 2 || result.End.WithoutReplica != 2 {
		t.Fatalf("wrong physical identity or set comparison: %+v", result)
	}
	if result.Before.Labels["prometheus_replica"].DropReduction != 1 || result.Before.Labels["le"].DropReduction != 1 || result.End.Labels["pod"].DropReduction != 1 {
		t.Fatalf("wrong independent drop-one projection: %+v", result)
	}
	if !reflect.DeepEqual(result.Before.Labels["pod"].Values, []string{"a"}) {
		t.Fatal("case-insensitive values not canonicalized")
	}
	if _, exists := result.Before.Labels["__name__"]; exists {
		t.Fatal("fabricated metric label")
	}
}

func TestInventoryInvalidAndEmpty(t *testing.T) {
	valid := string(inventoryTestBody(map[string]string{"pod": "a"}))
	for _, body := range []string{
		`null`, `{}`, strings.Replace(valid, `"vector"`, `"matrix"`, 1),
		strings.Replace(valid, `"12"`, `"NaN"`, 1), strings.Replace(valid, `"12"`, `"0"`, 1),
		strings.Replace(valid, `"12"`, `"1.5"`, 1), strings.Replace(valid, `"status":"success"`, `"status":"success","warnings":["partial"]`, 1),
		string(inventoryTestBody(map[string]string{"pod": "a"}, map[string]string{"POD": "A"})),
		string(inventoryTestBody(map[string]string{"pod": "a", "POD": "b"})),
	} {
		if _, err := AnalyzeInventory([]byte(body), []byte(valid)); err == nil {
			t.Fatalf("invalid inventory accepted: %s", body)
		}
	}
	result, err := AnalyzeInventory(inventoryTestBody(), inventoryTestBody())
	if err != nil || result.Before.Series != 0 || result.Added != 0 {
		t.Fatalf("successful empty vector rejected: %+v %v", result, err)
	}
	result, err = AnalyzeInventory(inventoryTestBody(map[string]string{}, map[string]string{"pod": ""}), inventoryTestBody())
	if err != nil || result.Before.Series != 2 || result.Before.Labels["pod"].Missing != 1 {
		t.Fatalf("missing actual labels were fabricated: %+v %v", result, err)
	}
}

func TestInventoryPlanCaps(t *testing.T) {
	workspaces := []*collectionWorkspace{{Name: "a", Names: []string{"up", "two"}}, {Name: "b", Names: []string{"up", "two"}}}
	for _, metrics := range [][]string{{"up", "two"}, {"up", "UP"}, {"missing"}, {`up{job="a"}`}} {
		if _, err := planInventories(workspaces, metrics, nil); err == nil {
			t.Fatalf("unsafe plan accepted: %v", metrics)
		}
	}
	plan, err := planInventories(workspaces, []string{"UP"}, nil)
	if err != nil || len(plan[0]) != 1 || len(plan[1]) != 1 || plan[0][0].BeforeQuery != "count_over_time(up[12h])" {
		t.Fatalf("bad bounded plan: %+v %v", plan, err)
	}
	plan, err = planInventories(workspaces, nil, map[string][]string{"a": {"up", "two"}, "b": {"up"}})
	if err != nil || len(plan[0]) != 2 || len(plan[1]) != 1 {
		t.Fatalf("workspace scoping failed: %+v %v", plan, err)
	}
}

func TestAddInventoriesPreservesSource(t *testing.T) {
	t.Parallel()
	o := collectionTestOptions(t)
	o.InventoryMetrics = []string{"up"}
	source := map[string]any{
		"schemaVersion": 1, "run": map[string]any{"start": o.Start.Unix(), "end": o.End.Unix(), "unknown": "preserved"},
		"workspaces": []any{map[string]any{"name": "test", "id": collectionTestID, "endpoint": "https://test.westus3.prometheus.monitor.azure.com", "names": []string{"up"}, "unknown": "preserved"}},
		"records":    map[string]any{"old": map[string]any{"ok": true, "unknown": "preserved"}},
		"manifests":  []any{map[string]any{"unknown": "preserved"}}, "unknown": "preserved", "complete": true,
	}
	raw, _ := json.Marshal(source)
	calls := 0
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		wantTime := o.Start
		if calls == 1 {
			wantTime = o.End
		}
		if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != "count_over_time(up[12h])" || r.URL.Query().Get("time") != wantTime.Format("2006-01-02T15:04:05Z07:00") {
			t.Fatalf("unexpected inventory query: %s", r.URL)
		}
		calls++
		body := strings.ReplaceAll(string(inventoryTestBody(map[string]string{"pod": "p", "prometheus_replica": "r"})), "1790000000", fmt.Sprint(wantTime.Unix()))
		return collectionTestResponse(body, 200), nil
	})
	summary, err := AddInventories(context.Background(), raw, o)
	if err != nil || calls != 2 || summary.Requests != 2 || summary.PromQL != 2 {
		t.Fatalf("bounded augmentation failed: %+v %v", summary, err)
	}
	data := collectionReadData(t, o.Output)
	if !data.Complete || len(data.Workspaces[0].Inventories) != 1 || len(data.Records) != 3 {
		t.Fatalf("missing inventory evidence: %+v", data)
	}
	newRaw, err := os.ReadFile(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	var augmented map[string]any
	if err := json.Unmarshal(newRaw, &augmented); err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"run", "unknown"} {
		if !reflect.DeepEqual(original[key], augmented[key]) {
			t.Fatalf("source %s changed", key)
		}
	}
	if augmented["run"].(map[string]any)["unknown"] != "preserved" || augmented["records"].(map[string]any)["old"].(map[string]any)["unknown"] != "preserved" || augmented["manifests"].([]any)[0].(map[string]any)["unknown"] != "preserved" || augmented["workspaces"].([]any)[0].(map[string]any)["unknown"] != "preserved" {
		t.Fatal("unknown source provenance lost")
	}
	if _, err := AddInventories(context.Background(), raw, o); err == nil {
		t.Fatal("existing output overwritten")
	}
	if calls != 2 {
		t.Fatal("extra requests on output collision")
	}
}

func TestCollectInventoriesOneShot(t *testing.T) {
	t.Parallel()
	o := collectionTestOptions(t)
	o.InventoryMetrics = []string{"up"}
	o.Transport = collectionTestTransport(func(r *http.Request) (*http.Response, error) {
		return collectionTestResponse(collectionTestBody(r), 200), nil
	})
	summary, err := Collect(context.Background(), o)
	if err != nil || summary.Requests != 10 || summary.PromQL != 2 {
		t.Fatalf("wrong one-shot inventory plan: %+v %v", summary, err)
	}
	if len(collectionReadData(t, o.Output).Workspaces[0].Inventories) != 1 {
		t.Fatal("inventory references missing")
	}
}
