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
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type seriesInventory struct {
	Name        string `json:"name"`
	Before      string `json:"before"`
	End         string `json:"end"`
	BeforeQuery string `json:"beforeQuery"`
	EndQuery    string `json:"endQuery"`
}

// AddInventories supplements a schema-1 artifact without repeating its requests.
// Output must be a new path. Source metadata and raw records are preserved; the
// supplied credential and transport are used only for the six-or-fewer new GETs.
// Workspace IDs and snapshot times come from input, not the options.
func AddInventories(ctx context.Context, input []byte, o CollectOptions) (CollectSummary, error) {
	var data collectionData
	if err := json.Unmarshal(input, &data); err != nil {
		return CollectSummary{}, err
	}
	if data.SchemaVersion != 1 || data.Records == nil || (len(o.InventoryMetrics) == 0 && len(o.InventorySelection) == 0) {
		return CollectSummary{}, errors.New("schema-1 records and explicit inventory metrics are required")
	}
	for _, record := range data.Records {
		if record == nil {
			return CollectSummary{}, errors.New("null source record")
		}
	}
	o.Workspaces = nil
	o.Start, o.End = time.Unix(data.Run.Start, 0), time.Unix(data.Run.End, 0)
	o.Context, o.Metrics, o.Selection = nil, nil, nil
	for _, workspace := range data.Workspaces {
		if workspace == nil {
			return CollectSummary{}, errors.New("null workspace")
		}
		parts := collectionResourceID.FindStringSubmatch(workspace.ID)
		if parts == nil || parts[3] != workspace.Name {
			return CollectSummary{}, errors.New("workspace name/ID mismatch")
		}
		u, err := url.Parse(workspace.Endpoint)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !collectionPrometheusHost.MatchString(u.Host) {
			return CollectSummary{}, errors.New("untrusted inventory endpoint")
		}
		o.Workspaces = append(o.Workspaces, workspace.ID)
	}
	if err := o.Validate(); err != nil {
		return CollectSummary{}, err
	}
	plan, err := planInventories(data.Workspaces, o.InventoryMetrics, o.InventorySelection)
	if err != nil {
		return CollectSummary{}, err
	}
	for i, entries := range plan {
		workspace := data.Workspaces[i]
		for _, entry := range entries {
			for _, existing := range workspace.Inventories {
				if strings.EqualFold(existing.Name, entry.Name) {
					return CollectSummary{}, errors.New("inventory already exists")
				}
			}
			if data.Records[entry.Before] != nil || data.Records[entry.End] != nil {
				return CollectSummary{}, errors.New("inventory record already exists")
			}
		}
	}
	return collect(ctx, o, input)
}

func planInventories(workspaces []*collectionWorkspace, metrics []string, selection map[string][]string) ([][]seriesInventory, error) {
	plan := make([][]seriesInventory, len(workspaces))
	pairs := 0
	seen := map[string]bool{}
	for _, metric := range metrics {
		if !collectionMetricName.MatchString(metric) || seen[strings.ToLower(metric)] {
			return nil, errors.New("inventory requires unique exact metric names (case-insensitive)")
		}
		seen[strings.ToLower(metric)] = true
		matched := false
		for i, workspace := range workspaces {
			for _, name := range workspace.Names {
				if !strings.EqualFold(name, metric) {
					continue
				}
				matched = true
				query := "count_over_time(" + name + "[12h])"
				prefix := workspace.Name + "-" + name + "-inventory-"
				plan[i] = append(plan[i], seriesInventory{Name: name, Before: prefix + "before", End: prefix + "end", BeforeQuery: query, EndQuery: query})
				pairs++
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("inventory metric %q not discovered in any workspace", metric)
		}
	}
	for i, workspace := range workspaces {
		for _, key := range []string{workspace.Name, workspace.ID} {
			for _, metric := range selection[key] {
				one, err := planInventories([]*collectionWorkspace{workspace}, []string{metric}, nil)
				if err != nil {
					return nil, err
				}
				duplicate := false
				for _, entry := range plan[i] {
					if strings.EqualFold(entry.Name, metric) {
						duplicate = true
					}
				}
				if !duplicate {
					plan[i] = append(plan[i], one[0]...)
					pairs++
				}
			}
		}
	}
	if pairs > 3 {
		return nil, errors.New("inventory selection exceeds three workspace/metric pairs (six requests)")
	}
	return plan, nil
}

func collectInventories(workspaces []*collectionWorkspace, metrics []string, selection map[string][]string, before, end time.Time, request func(string, string, string, func([]byte) error) (bool, error)) error {
	plan, err := planInventories(workspaces, metrics, selection)
	if err != nil {
		return err
	}
	for i, entries := range plan {
		workspace := workspaces[i]
		for _, entry := range entries {
			workspace.Inventories = append(workspace.Inventories, entry)
			for _, snapshot := range []struct {
				id string
				at time.Time
			}{{entry.Before, before}, {entry.End, end}} {
				values := url.Values{"query": {entry.BeforeQuery}, "time": {snapshot.at.UTC().Format(time.RFC3339)}, "timeout": {"90s"}}
				_, err := request(snapshot.id, "inventory", workspace.Endpoint+"/api/v1/query?"+values.Encode(), func(body []byte) error {
					if _, err := inventoryLabelsets(body); err != nil {
						return err
					}
					var response promResponse
					if err := json.Unmarshal(body, &response); err != nil {
						return err
					}
					for _, row := range response.Data.Result {
						var at float64
						if err := json.Unmarshal(row.Value[0], &at); err != nil {
							return err
						}
						if at != float64(snapshot.at.Unix()) {
							return errors.New("inventory evaluation timestamp differs from requested snapshot")
						}
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// InventoryAnalysis describes retained sampled series, not received-series quota
// accounting. Added/removed compare two rolling (t-12h,t] sets, not cluster births.
type InventoryAnalysis struct {
	Before  InventorySnapshot `json:"before"`
	End     InventorySnapshot `json:"end"`
	Common  int               `json:"common"`
	Added   int               `json:"added"`
	Removed int               `json:"removed"`
}

type InventorySnapshot struct {
	Series         int                       `json:"series"`
	WithoutReplica int                       `json:"withoutReplica"`
	Labels         map[string]InventoryLabel `json:"labels"`
}

type InventoryLabel struct {
	Values  []string `json:"values"`
	Missing int      `json:"missing"`
	// Each potential is evaluated independently; potentials must not be summed.
	DropReduction int `json:"dropReduction"`
}

// AnalyzeInventory compares full physical identities with AMW case-insensitivity.
// Metric identity belongs to the enclosing inventory: count_over_time drops
// __name__, and no synthetic metric aliases are introduced into labelsets.
func AnalyzeInventory(beforeBody, endBody []byte) (InventoryAnalysis, error) {
	var result InventoryAnalysis
	before, err := inventoryLabelsets(beforeBody)
	if err != nil {
		return result, err
	}
	end, err := inventoryLabelsets(endBody)
	if err != nil {
		return result, err
	}
	result.Before, result.End = inventorySnapshot(before), inventorySnapshot(end)
	for identity := range before {
		if _, ok := end[identity]; ok {
			result.Common++
		}
	}
	result.Added, result.Removed = len(end)-result.Common, len(before)-result.Common
	return result, nil
}

func inventoryLabelsets(body []byte) (map[string]map[string]string, error) {
	var response promResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Status != "success" || response.Data.ResultType != "vector" || response.Data.Result == nil || len(response.Warnings) > 0 {
		return nil, errors.New("inventory requires a successful vector without warnings")
	}
	sets := map[string]map[string]string{}
	for _, row := range response.Data.Result {
		if row.Metric == nil || len(row.Value) != 2 {
			return nil, errors.New("invalid inventory sample")
		}
		var count string
		var timestamp float64
		if string(row.Value[0]) == "null" || json.Unmarshal(row.Value[0], &timestamp) != nil || json.Unmarshal(row.Value[1], &count) != nil {
			return nil, errors.New("invalid inventory sample value")
		}
		n, err := strconv.ParseFloat(count, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 1 || n != math.Trunc(n) {
			return nil, errors.New("inventory sample count must be a positive integer")
		}
		labels := map[string]string{}
		for label, value := range row.Metric {
			key := strings.ToLower(label)
			if _, exists := labels[key]; exists {
				return nil, errors.New("case-colliding label names")
			}
			labels[key] = strings.ToLower(value)
		}
		identity := inventoryIdentity(labels, "")
		if _, exists := sets[identity]; exists {
			return nil, errors.New("duplicate AMW physical series in inventory")
		}
		sets[identity] = labels
	}
	return sets, nil
}

func inventoryIdentity(labels map[string]string, drop string) string {
	kept := make(map[string]string, len(labels))
	for label, value := range labels {
		if label != drop {
			kept[label] = value
		}
	}
	identity, _ := json.Marshal(kept) // String maps always marshal and keys are sorted.
	return string(identity)
}

func inventorySnapshot(sets map[string]map[string]string) InventorySnapshot {
	result := InventorySnapshot{Series: len(sets), Labels: map[string]InventoryLabel{}}
	values := map[string]map[string]bool{}
	replicas := map[string]bool{}
	for _, labels := range sets {
		replicas[inventoryIdentity(labels, "prometheus_replica")] = true
		for label, value := range labels {
			if values[label] == nil {
				values[label] = map[string]bool{}
			}
			values[label][value] = true
		}
	}
	result.WithoutReplica = len(replicas)
	for label, distinct := range values {
		entry := InventoryLabel{}
		projected := map[string]bool{}
		for value := range distinct {
			entry.Values = append(entry.Values, value)
		}
		sort.Strings(entry.Values)
		for _, labels := range sets {
			if _, ok := labels[label]; !ok {
				entry.Missing++
			}
			projected[inventoryIdentity(labels, label)] = true
		}
		entry.DropReduction = len(sets) - len(projected)
		result.Labels[label] = entry
	}
	return result
}
