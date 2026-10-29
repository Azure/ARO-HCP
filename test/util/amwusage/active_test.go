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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func activeRecord(t *testing.T, at float64, labels ...map[string]string) artifactRecord {
	t.Helper()
	rows := []any{}
	for _, l := range labels {
		rows = append(rows, map[string]any{"metric": l, "value": []any{at, "7"}})
	}
	b, err := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}})
	if err != nil {
		t.Fatal(err)
	}
	return testRecord(string(b))
}

func TestActiveFullIdentityPeriods(t *testing.T) {
	a := baselineFixture()
	a.Workspaces[0].Inventories = []seriesInventory{{Name: "full_metric", Before: "ib", End: "ie"}}
	common := map[string]string{"namespace": "ocm-a", "hostedcontrolplane": "ocm-a-cp", "pod": "common"}
	dropped := map[string]string{"pod": "removed"}
	added := map[string]string{"namespace": "ocm-a", "hostedcontrolplane": "ocm-a-cp", "pod": "new"}
	a.Records["ib"] = activeRecord(t, a.Run.Start, common, dropped)
	a.Records["ie"] = activeRecord(t, a.Run.End, common, added)
	x := analyzeActive(a, analyzeArtifact(a))
	if x.Measured != 1 || len(x.Rows) != 3 {
		t.Fatalf("unexpected inventory: %+v", x)
	}
	requireNumber(t, x.InventoryBefore, 2)
	requireNumber(t, x.InventoryEnd, 2)
	requireNumber(t, x.Added, 1)
	requireNumber(t, x.Removed, 1)
	requireNumber(t, x.Net, 0)
	if *x.InventoryBefore+*x.Added-*x.Removed != *x.InventoryEnd {
		t.Fatal("set identity arithmetic broken")
	}
	owned := 0
	for _, r := range x.Rows {
		if r.ScopeKey == "customer:sameprefix-one" {
			owned++
		}
	}
	if owned != 2 {
		t.Fatal("namespace and HCP aliases double-counted or lost ownership")
	}
	// Quiet-baseline and narrower run vectors cannot define rolling 12h sets.
	a.Records["bs"] = activeRecord(t, 1200, map[string]string{"pod": "unrelated"})
	a.Records["i"] = activeRecord(t, a.Run.End, map[string]string{"pod": "unrelated"})
	x = analyzeActive(a, analyzeArtifact(a))
	requireNumber(t, x.Added, 1)
	requireNumber(t, x.InventoryEnd, 2)
	if len(x.Inventories[0].Snapshots) != 4 {
		t.Fatal("before/added/removed/end offline analyses missing")
	}
}

func TestActiveUnknownInventoriesAndOldArchives(t *testing.T) {
	a := testArtifact()
	x := analyzeActive(a, analyzeArtifact(a))
	if len(x.Rows) != 0 || x.InventoryBefore != nil || x.InventoryEnd != nil {
		t.Fatal("old aggregate archive fabricated full inventories")
	}
	a.Workspaces[0].Inventories = []seriesInventory{{Name: "m", Before: "b", End: "e"}}
	a.Records["b"] = activeRecord(t, a.Run.Start)
	for _, r := range []artifactRecord{{}, activeRecord(t, a.Run.End-1, map[string]string{"a": "b"}), testRecord(`{"status":"success","warnings":["partial"],"data":{"resultType":"vector","result":[]}}`)} {
		a.Records["e"] = r
		x = analyzeActive(a, analyzeArtifact(a))
		if x.Measured != 0 || len(x.Rows) != 0 || x.Net != nil || x.Inventories[0].Error == "" {
			t.Fatal("incomplete inventory treated as empty")
		}
	}
	a.Records["e"] = activeRecord(t, a.Run.End)
	x = analyzeActive(a, analyzeArtifact(a))
	requireNumber(t, x.InventoryBefore, 0)
	requireNumber(t, x.InventoryEnd, 0)
}

func TestActiveProjectionCorrelationAndShards(t *testing.T) {
	sets := map[string]map[string]string{}
	for _, cluster := range []string{"c1", "c2"} {
		for shard := 0; shard < 2; shard++ {
			for replica := 0; replica < 2; replica++ {
				for bucket := 0; bucket < 3; bucket++ {
					labels := map[string]string{"cluster": cluster, "namespace": fmt.Sprintf("ns-%d", shard), "instance": fmt.Sprintf("instance-%d", shard), "pod": "same-pod-name", "le": fmt.Sprint(bucket), "prometheus_replica": fmt.Sprintf("prom-agent-prometheus-shard-%d-%d", shard, replica)}
					sets[inventoryIdentity(labels, "")] = labels
				}
			}
		}
	}
	s := analyzeActiveSnapshot("end", sets)
	if s.Series != 24 || s.WithoutReplica != 12 || len(s.Agents) != 8 {
		t.Fatalf("cluster/shard identities merged: %+v", s)
	}
	requireNumber(t, s.Multiplicity, 2)
	for _, l := range s.Labels {
		if l.Name == "namespace" && (l.Distinct != 2 || l.Reduction != 0) {
			t.Fatal("correlated namespace incorrectly treated as removable identity")
		}
		if l.Name == "le" && l.Reduction != 16 {
			t.Fatal("histogram physical buckets not preserved")
		}
	}
	unknown := map[string]string{"cluster": "c1", "prometheus_replica": "custom-agent", "instance": "other"}
	sets[inventoryIdentity(unknown, "")] = unknown
	s = analyzeActiveSnapshot("end", sets)
	if s.Series != 25 || s.WithoutReplica != 13 || len(s.Multiplicities) != 2 {
		t.Fatal("global two-replica assumption")
	}
	found := false
	for _, a := range s.Agents {
		if a.Agent == "custom-agent" {
			found = true
			if a.Shard != "Unknown" {
				t.Fatal("unknown agent inferred as shard")
			}
		}
	}
	if !found {
		t.Fatal("unknown agent dropped")
	}
}

func TestActivePlatformTotalsWithholdGaps(t *testing.T) {
	a := testArtifact()
	a.Run.Start = 60
	a.Run.End = 120
	testCompletePlatform(a)
	a.Workspaces = append(a.Workspaces, a.Workspaces[0])
	a.Workspaces[1].Name = "second"
	x := analyzeActive(a, analyzeArtifact(a))
	requireNumber(t, x.Before, 0)
	requireNumber(t, x.End, 0)
	requireNumber(t, x.Delta, 0)
	a.Workspaces[1].Platform = map[string]string{}
	x = analyzeActive(a, analyzeArtifact(a))
	if x.Before != nil || x.End != nil || x.Delta != nil {
		t.Fatal("missing workspace silently omitted from headline totals")
	}
	if x.Workspaces[0].Baseline == nil {
		t.Fatal("independent workspace evidence lost")
	}
}

func TestActiveRenderingOrderAndEscaping(t *testing.T) {
	a := testArtifact()
	testCompletePlatform(a)
	a.Workspaces[0].Inventories = []seriesInventory{{Name: "test_bucket", Before: "b", End: "e"}}
	a.Records["b"] = activeRecord(t, a.Run.Start)
	a.Records["e"] = activeRecord(t, a.Run.End, map[string]string{"pod": "</script><img src=x onerror=alert(1)>"})
	var report bytes.Buffer
	if err := Render(&report, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
	s := report.String()
	if strings.Contains(s, "<img") {
		t.Fatal("inventory labels not escaped")
	}
	for _, part := range []string{"Where did the active series come from?", "Before the run", "Growth during the run", "Which labels multiply these identities?", "Samples and quiet-window comparison"} {
		if !strings.Contains(s, part) {
			t.Fatalf("missing %q", part)
		}
	}
	if strings.Index(s, `id="active-overview"`) > strings.Index(s, `id="active-breakdown"`) || strings.Index(s, `id="active-breakdown"`) > strings.Index(s, `id="sample-comparison"`) {
		t.Fatal("active-series investigation no longer leads")
	}
}

func TestActiveArchivedV3(t *testing.T) {
	path := os.Getenv("AMW_USAGE_INVENTORY")
	if path == "" {
		t.Skip("AMW_USAGE_INVENTORY not supplied")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := decodeArtifact(b)
	if err != nil {
		t.Fatal(err)
	}
	x := analyzeActive(a, analyzeArtifact(a))
	requireNumber(t, x.Before, 4072380)
	requireNumber(t, x.End, 13405954)
	requireNumber(t, x.Delta, 9333574)
	requireNumber(t, x.InventoryBefore, 350)
	requireNumber(t, x.InventoryEnd, 13408)
	requireNumber(t, x.Added, 13058)
	requireNumber(t, x.Removed, 0)
	if x.Measured != 3 || len(x.Rows) != 13408 {
		t.Fatalf("inventory selection changed: %d / %d", x.Measured, len(x.Rows))
	}
	for _, w := range x.Workspaces {
		if renderUTC(w.BaselineTime) != "2026-09-21 04:25:00 UTC" || renderUTC(w.EndTime) != "2026-09-21 06:43:00 UTC" {
			t.Fatalf("wrong platform boundary minutes: %+v", w.workspaceGrowth)
		}
		if w.Name == "hcps-westus3" {
			requireNumber(t, w.Delta, 7217737)
		}
		if w.Name == "services-westus3" {
			requireNumber(t, w.Delta, 2115837)
		}
	}
	for _, inv := range x.Inventories {
		if inv.Metric == "api_inbound_request_duration_bucket" {
			requireNumber(t, inv.End, 1140)
			requireNumber(t, inv.Before, 350)
		}
		end := inv.Snapshots[3]
		requireNumber(t, end.Multiplicity, 2)
	}
}

func TestActiveIndependentSnapshots(t *testing.T) {
	for _, failed := range []string{"before", "end"} {
		t.Run(failed, func(t *testing.T) {
			a := testArtifact()
			a.Workspaces[0].Inventories = []seriesInventory{{Name: "m", Before: "b", End: "e"}}
			labels := map[string]string{"cluster": "c", "job": "j", "prometheus_replica": "prom-agent-prometheus-0"}
			a.Records["b"] = activeRecord(t, a.Run.Start, labels)
			a.Records["e"] = activeRecord(t, a.Run.End, labels)
			if failed == "before" {
				delete(a.Records, "b")
			} else {
				delete(a.Records, "e")
			}
			x := analyzeActive(a, analyzeArtifact(a))
			if x.Measured != 0 || len(x.Rows) != 1 || len(x.Inventories[0].Snapshots) != 1 || x.Added != nil || x.Removed != nil || x.Net != nil {
				t.Fatalf("independent snapshot lost or comparison fabricated: %+v", x)
			}
			r, inv := x.Rows[0], x.Inventories[0]
			if inv.Added != nil || inv.Removed != nil || inv.Net != nil {
				t.Fatal("incomplete metric comparison not withheld")
			}
			if failed == "before" {
				requireNumber(t, inv.End, 1)
				requireNumber(t, x.InventoryEnd, 1)
				if inv.Before != nil || x.InventoryBefore != nil || r.Before != nil || r.End == nil || !*r.End || x.BeforeMeasured != 0 || x.EndMeasured != 1 {
					t.Fatal("unknown before became absent or end lost")
				}
			} else {
				requireNumber(t, inv.Before, 1)
				requireNumber(t, x.InventoryBefore, 1)
				if inv.End != nil || x.InventoryEnd != nil || r.End != nil || r.Before == nil || !*r.Before || x.BeforeMeasured != 1 || x.EndMeasured != 0 {
					t.Fatal("unknown end became absent or before lost")
				}
			}
			encoded, err := json.Marshal(x)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"`+failed+`":null`) {
				t.Fatal("browser membership lacks explicit unknown")
			}
		})
	}
}

func TestActiveEmptyMultiplicityUnknown(t *testing.T) {
	s := analyzeActiveSnapshot("Before the run", map[string]map[string]string{})
	if s.Multiplicity != nil || s.Series != 0 || s.WithoutReplica != 0 {
		t.Fatal("empty mean multiplicity must be undefined")
	}
	a := testArtifact()
	a.Workspaces[0].Inventories = []seriesInventory{{Name: "m", Before: "b", End: "e"}}
	a.Records["b"] = activeRecord(t, a.Run.Start)
	a.Records["e"] = activeRecord(t, a.Run.End)
	var b bytes.Buffer
	if err := Render(&b, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Mean physical multiplicity: Unknown (empty inventory)") || strings.Contains(b.String(), "Mean physical multiplicity: 0.") {
		t.Fatal("offline empty multiplicity not shown as unknown")
	}
}

func TestActiveInventoryRequestLedger(t *testing.T) {
	a := testArtifact()
	r := artifactRecord{OK: true}
	r.Request.Kind = "inventory"
	a.Manifests = append(a.Manifests, r)
	r.OK = false
	a.Manifests = append(a.Manifests, r)
	s := analyzeArtifact(a)
	if s.PromQL != 2 || s.PromQLErrors != 1 {
		t.Fatal("inventory PromQL calls missing from request ledger")
	}
}
