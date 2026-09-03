// Copyright 2025 Microsoft Corporation
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

package grafana

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestExploreURL(t *testing.T) {
	u := ExploreURL("https://g.example.com/", "services-uksouth", []ExploreQuery{
		{RefID: "A", Expr: `container_memory_working_set_bytes{namespace="arobit"}`},
		{RefID: "B", Expr: `rate(container_cpu_usage_seconds_total{namespace="arobit"}[5m])`},
	}, "now-14d", "now")

	if !strings.HasPrefix(u, "https://g.example.com/explore?") {
		t.Fatalf("unexpected prefix: %s", u)
	}

	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	panes := parsed.Query().Get("panes")
	if panes == "" {
		t.Fatal("missing panes param")
	}

	var decoded map[string]struct {
		Datasource string `json:"datasource"`
		Queries    []struct {
			RefID      string `json:"refId"`
			Expr       string `json:"expr"`
			Datasource dsRef  `json:"datasource"`
		} `json:"queries"`
		Range map[string]string `json:"range"`
	}
	if err := json.Unmarshal([]byte(panes), &decoded); err != nil {
		t.Fatalf("panes not valid JSON: %v", err)
	}
	pane, ok := decoded["rsz"]
	if !ok {
		t.Fatal("missing rsz pane")
	}
	if pane.Datasource != "services-uksouth" {
		t.Errorf("datasource = %q", pane.Datasource)
	}
	if len(pane.Queries) != 2 || pane.Queries[0].RefID != "A" || pane.Queries[1].RefID != "B" {
		t.Errorf("queries = %+v", pane.Queries)
	}
	if pane.Queries[0].Datasource.UID != "services-uksouth" || pane.Queries[0].Datasource.Type != "prometheus" {
		t.Errorf("query datasource = %+v", pane.Queries[0].Datasource)
	}
	if pane.Range["from"] != "now-14d" || pane.Range["to"] != "now" {
		t.Errorf("range = %+v", pane.Range)
	}
}
