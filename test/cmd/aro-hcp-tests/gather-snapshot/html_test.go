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

package gathersnapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

const hostileSnapshotText = "</script><script>window.injected=true</script><img src=x onerror=window.injected=true> & \" ' \u2028\u2029\n"

func overviewFixture() ([]*snapshot.Manifest, []*snapshot.VerificationReport) {
	manifests := []*snapshot.Manifest{
		{TestName: hostileSnapshotText, ResourceGroup: "rg-one", TimeWindow: snapshot.TimeWindow{Start: time.Unix(0, 0).UTC(), End: time.Unix(60, 0).UTC()}},
		{TestName: "Second", ResourceGroup: "rg-two"},
		{TestName: "Empty", ResourceGroup: "rg-three"},
	}
	reports := []*snapshot.VerificationReport{{Cases: []snapshot.VerificationCase{
		{Suite: "z-suite", Category: "z-category", Query: hostileSnapshotText, Status: snapshot.VerificationFail, RenderedKQL: hostileSnapshotText},
		{Suite: "a-suite", Category: "other", Query: "no-kql", Status: snapshot.VerificationSkipped},
		{Suite: "z-suite", Category: "a-category", Query: "unknown-status", Status: "unknown", RenderedKQL: "unknown KQL"},
		{Suite: "z-suite", Category: "z-category", Query: "pass", Status: snapshot.VerificationPass, RenderedKQL: hostileSnapshotText},
		{Suite: "z-suite", Category: "z-category", Query: "skip", Status: snapshot.VerificationSkipped},
	}}, {Cases: []snapshot.VerificationCase{
		{Suite: "pass-only", Category: "pass-only", Query: "pass-only", Status: snapshot.VerificationPass, RenderedKQL: hostileSnapshotText},
	}}, {}}
	return manifests, reports
}

func TestBuildHTMLData(t *testing.T) {
	manifests, reports := overviewFixture()
	data := buildHTMLData(manifests, reports)
	if data.TotalPassCount != 2 || data.TotalFailCount != 1 || data.TotalSkipCount != 2 {
		t.Fatalf("incorrect totals: %+v", data)
	}
	section := data.Sections[0]
	if section.Statuses != "pass fail skip" || section.PassCount != 1 || section.FailCount != 1 || section.SkipCount != 2 || section.StartTime != "1970-01-01T00:00:00Z" || section.EndTime != "1970-01-01T00:01:00Z" {
		t.Fatalf("incorrect section metadata: %+v", section)
	}
	if section.Nodes[0].Name != "z-suite" || section.Nodes[1].Name != "a-suite" || section.Nodes[0].Children[0].Name != "z-category" || section.Nodes[0].Children[1].Name != "a-category" {
		t.Fatalf("suite/category first-seen ordering changed: %+v", section.Nodes)
	}
	category := section.Nodes[0].Children[0]
	if section.Nodes[0].FailCount != 1 || category.FailCount != 1 || category.Statuses != "pass fail skip" {
		t.Fatalf("missing collapsed failure/status indicators: %+v", category)
	}
	if got := []string{category.Queries[0].Key, category.Queries[1].Key, category.Queries[2].Key}; !reflect.DeepEqual(got, []string{hostileSnapshotText, "pass", "skip"}) {
		t.Fatalf("query order changed: %v", got)
	}
	if !reflect.DeepEqual(data.KQL, []string{"", hostileSnapshotText, "unknown KQL"}) {
		t.Fatalf("KQL was not losslessly deduplicated: %q", data.KQL)
	}
	for _, section := range data.Sections {
		for _, node := range section.Nodes {
			for _, category := range node.Children {
				for _, query := range category.Queries {
					if data.KQL[query.KQLIndex] != query.KQL {
						t.Fatalf("KQL index lost query text: %+v", query)
					}
				}
			}
		}
	}
	if got := buildHTMLData(manifests, reports[:1]); len(got.Sections) != 1 || got.TotalPassCount != 1 {
		t.Fatalf("unpaired manifests must still be ignored: %+v", got)
	}
	if got := buildHTMLData(manifests[:1], reports); len(got.Sections) != 1 {
		t.Fatalf("unpaired reports must still be ignored: %+v", got)
	}
}

func TestWriteHTMLOverview(t *testing.T) {
	manifests, reports := overviewFixture()
	for _, tc := range []struct {
		name      string
		manifests []*snapshot.Manifest
		reports   []*snapshot.VerificationReport
	}{{"populated", manifests, reports}, {name: "empty"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "nested")
			if err := WriteHTMLOverview(dir, tc.manifests, tc.reports); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "snapshot-summary.html"))
			if err != nil {
				t.Fatal(err)
			}
			page := string(raw)
			if strings.Contains(page, "<img") || strings.Contains(page, "<script>window.injected") || strings.Contains(page, "<details") || strings.Contains(page, "innerHTML") || strings.Contains(page, "ZgotmplZ") {
				t.Fatal("HTML contains unsafe content or eagerly rendered descendants")
			}
			data := buildHTMLData(tc.manifests, tc.reports)
			for id, value := range map[string]any{"snapshot-data": data.Sections, "snapshot-kql": data.KQL} {
				_, rest, found := strings.Cut(page, `<script id="`+id+`" type="application/json">`)
				if !found {
					t.Fatalf("missing JSON payload %s", id)
				}
				payload, _, found := strings.Cut(rest, "</script>")
				if !found || strings.Contains(payload, "\n") {
					t.Fatalf("JSON payload %s is missing or not compact", id)
				}
				var got, want any
				if err := json.Unmarshal([]byte(payload), &got); err != nil {
					t.Fatalf("invalid safe JSON %s: %v", id, err)
				}
				expected, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(expected, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("payload %s did not round-trip", id)
				}
			}
		})
	}
}

// Optional local replay without a live Kusto connection. Outputs are retained
// only when explicitly requested, allowing the real artifact to be measured.
func TestReplayHTMLOverview(t *testing.T) {
	input := os.Getenv("SNAPSHOT_REPLAY_INPUT")
	if input == "" {
		t.Skip("set SNAPSHOT_REPLAY_INPUT to replay snapshot-data.json")
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var data snapshotData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("SNAPSHOT_REPLAY_OUTPUT")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := WriteHTMLOverview(dir, data.Manifests, data.Reports); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "snapshot-summary.html"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("replayed %d manifests: %s (%d bytes)", len(data.Manifests), dir, info.Size())
}
