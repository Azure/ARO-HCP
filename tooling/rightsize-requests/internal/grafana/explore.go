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
)

// ExploreQuery is a single labeled PromQL expression for an Explore link.
type ExploreQuery struct {
	RefID string
	Expr  string
}

// ExploreURL builds a Grafana Explore deep link that opens the given PromQL
// queries against datasource dsUID over [from,to] (e.g. "now-14d", "now"). The
// link lets a reviewer see exactly the usage the tool measured.
func ExploreURL(base, dsUID string, queries []ExploreQuery, from, to string) string {
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(base, "http") {
		base = "https://" + base
	}

	type paneQuery struct {
		RefID      string `json:"refId"`
		Datasource dsRef  `json:"datasource"`
		Expr       string `json:"expr"`
		Range      bool   `json:"range"`
	}
	pqs := make([]paneQuery, 0, len(queries))
	for _, q := range queries {
		pqs = append(pqs, paneQuery{
			RefID:      q.RefID,
			Datasource: dsRef{Type: "prometheus", UID: dsUID},
			Expr:       q.Expr,
			Range:      true,
		})
	}

	pane := map[string]any{
		"rsz": map[string]any{
			"datasource": dsUID,
			"queries":    pqs,
			"range":      map[string]string{"from": from, "to": to},
		},
	}
	b, err := json.Marshal(pane)
	if err != nil {
		return ""
	}

	v := url.Values{}
	v.Set("schemaVersion", "1")
	v.Set("orgId", "1")
	v.Set("panes", string(b))
	return base + "/explore?" + v.Encode()
}
