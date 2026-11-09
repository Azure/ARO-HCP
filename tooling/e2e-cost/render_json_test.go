// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRenderJSONMatchesReportFigures(t *testing.T) {
	snapshot := renderFixture()
	view, err := newCostReport(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, renderFixture()); err != nil {
		t.Fatal(err)
	}
	var data reportData
	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Positive != view.Positive || data.Negative != view.Negative || data.Net != view.Net || data.ResourceCount != view.ResourceCount ||
		data.InfraRate != view.InfraRate || data.Incomplete != view.Incomplete || data.Job != snapshot.Job {
		t.Fatalf("JSON figures differ from the HTML report's: %+v", data)
	}
	for name, pair := range map[string][2]string{
		"inventory": {string(data.Inventory), string(view.InventoryData)},
		"option":    {string(data.Option), string(view.Option)},
		"typeData":  {string(data.TypeData), string(view.TypeData)},
	} {
		var a, b any
		if json.Unmarshal([]byte(pair[0]), &a) != nil || json.Unmarshal([]byte(pair[1]), &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("%s differs from the HTML report's embedded data", name)
		}
	}
	if strings.Contains(out.String(), "echarts") || data.SubscriptionMode {
		t.Fatal("JSON must carry data only, without the chart runtime")
	}
}

func TestRenderJSONRejectsInvalidSnapshots(t *testing.T) {
	snapshot := renderFixture()
	snapshot.Currency = "EUR"
	var out bytes.Buffer
	if err := RenderJSON(&out, snapshot); err == nil || out.Len() != 0 {
		t.Fatal("invalid snapshots must fail before writing anything")
	}
	if err := RenderJSON(&out, nil); err == nil {
		t.Fatal("nil snapshot must be rejected")
	}
}
