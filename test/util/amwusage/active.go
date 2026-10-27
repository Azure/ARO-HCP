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
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

type activeRow struct {
	Workspace int               `json:"workspace"`
	Metric    string            `json:"metric"`
	Labels    map[string]string `json:"labels"`
	Collector string            `json:"collector"`
	Scope     string            `json:"scope"`
	ScopeKey  string            `json:"scopeKey"`
	Before    *bool             `json:"before"`
	End       *bool             `json:"end"`
}

type activeWorkspace struct {
	workspaceGrowth
	Limit *float64
	Chart renderChart
}

type activeInventory struct {
	Workspace                                        int
	WorkspaceName, Metric, Error                     string
	Before, End, Added, Removed, Net                 *float64
	Snapshots                                        []activeSnapshot
	BeforeQuery, EndQuery, BeforeRequest, EndRequest string
}

type activeSnapshot struct {
	Period                 string
	Series, WithoutReplica int
	Multiplicity           *float64
	Labels                 []activeLabel
	Agents                 []activeAgent
	Multiplicities         []activeMultiplicity
}

type activeLabel struct {
	Name                                    string
	Distinct, Missing, Projected, Reduction int
	Values                                  []activeLabelValue
}

type activeLabelValue struct {
	Value string
	Count int
}

type activeAgent struct {
	Cluster, Agent, Shard string
	Series                int
}

type activeMultiplicity struct {
	Copies, Identities int
}

type activeAnalysis struct {
	Rows                                               []activeRow       `json:"rows"`
	WorkspaceNames                                     []string          `json:"workspaceNames"`
	Workspaces                                         []activeWorkspace `json:"-"`
	Inventories                                        []activeInventory `json:"-"`
	Before, End, Delta                                 *float64          `json:"-"`
	InventoryBefore, InventoryEnd, Added, Removed, Net *float64          `json:"-"`
	Measured, Requested                                int               `json:"-"`
	BeforeMeasured, EndMeasured                        int               `json:"-"`
	Coverage                                           []activeCoverage  `json:"coverage"`
	Warnings                                           []string          `json:"warnings"`
}

type activeCoverage struct {
	Workspace   int    `json:"workspace"`
	Metric      string `json:"metric"`
	Before, End bool
}

var inventoryAgentPattern = regexp.MustCompile(`^prom-agent-prometheus(?:-shard-([0-9]+))?-([01])$`)

// Projections retain every other label, including cluster, target and metric
// identity. A shard-shaped name is only parsed for this observed naming scheme.
func analyzeActiveSnapshot(period string, sets map[string]map[string]string) activeSnapshot {
	snapshot := inventorySnapshot(sets)
	s := activeSnapshot{Period: period, Series: snapshot.Series, WithoutReplica: snapshot.WithoutReplica}
	if s.WithoutReplica > 0 {
		s.Multiplicity = finiteSum(float64(s.Series) / float64(s.WithoutReplica))
	}
	for label, analysis := range snapshot.Labels {
		entry := activeLabel{Name: label, Distinct: len(analysis.Values), Missing: analysis.Missing, Projected: s.Series - analysis.DropReduction, Reduction: analysis.DropReduction}
		counts := map[string]int{}
		for _, labels := range sets {
			if value, ok := labels[label]; ok {
				counts[value]++
			}
		}
		for _, value := range analysis.Values {
			entry.Values = append(entry.Values, activeLabelValue{value, counts[value]})
		}
		s.Labels = append(s.Labels, entry)
	}
	sort.Slice(s.Labels, func(i, j int) bool {
		if s.Labels[i].Reduction != s.Labels[j].Reduction {
			return s.Labels[i].Reduction > s.Labels[j].Reduction
		}
		return s.Labels[i].Name < s.Labels[j].Name
	})
	identities := map[string]int{}
	agents := map[[2]string]int{}
	for _, labels := range sets {
		identities[inventoryIdentity(labels, "prometheus_replica")]++
		agents[[2]string{labels["cluster"], labels["prometheus_replica"]}]++
	}
	hist := map[int]int{}
	for _, copies := range identities {
		hist[copies]++
	}
	for copies, count := range hist {
		s.Multiplicities = append(s.Multiplicities, activeMultiplicity{copies, count})
	}
	sort.Slice(s.Multiplicities, func(i, j int) bool { return s.Multiplicities[i].Copies < s.Multiplicities[j].Copies })
	for key, count := range agents {
		shard := "Unknown"
		if matches := inventoryAgentPattern.FindStringSubmatch(key[1]); matches != nil {
			shard = matches[1]
			if shard == "" {
				shard = "0"
			}
		}
		s.Agents = append(s.Agents, activeAgent{key[0], key[1], shard, count})
	}
	sort.Slice(s.Agents, func(i, j int) bool {
		a, b := s.Agents[i], s.Agents[j]
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		return a.Agent < b.Agent
	})
	return s
}

func readActiveInventory(ref string, records map[string]artifactRecord, at float64) (map[string]map[string]string, error) {
	record, exists := records[ref]
	if ref == "" || !exists || !record.OK {
		return nil, fmt.Errorf("inventory query failed or missing: %s", ref)
	}
	sets, err := inventoryLabelsets([]byte(record.Body))
	if err != nil {
		return nil, err
	}
	var response promResponse
	if err := json.Unmarshal([]byte(record.Body), &response); err != nil {
		return nil, err
	}
	for _, row := range response.Data.Result {
		timestamp := finiteValue(row.Value[0])
		if timestamp == nil || math.Abs(*timestamp-at) > 0.001 {
			return nil, fmt.Errorf("inventory timestamp differs from boundary: %s", ref)
		}
	}
	return sets, nil
}

func analyzeActive(a *artifactData, summary renderAnalysis) activeAnalysis {
	x := activeAnalysis{Rows: []activeRow{}, WorkspaceNames: []string{}, Coverage: []activeCoverage{}}
	context := contributionAnalysis{}
	owners := contributionContext(a, &context)
	x.Warnings = append(x.Warnings, context.Warnings...)
	before, end, delta := 0.0, 0.0, 0.0
	beforeOK, endOK, deltaOK := len(a.Workspaces) > 0, len(a.Workspaces) > 0, len(a.Workspaces) > 0
	ib, ie, added, removed := 0.0, 0.0, 0.0, 0.0
	for wi, w := range summary.Workspaces {
		x.WorkspaceNames = append(x.WorkspaceNames, w.Name)
		growth := analyzeWorkspaceGrowth(w.Name, w.Platform["ActiveTimeSeries"], a.Run)
		limit := analyzeWorkspaceGrowth(w.Name, w.Platform["ActiveTimeSeriesLimit"], a.Run)
		var lines []chartInput
		for i, s := range w.Platform["ActiveTimeSeries"].Series {
			lines = append(lines, chartInput{fmt.Sprintf("Active series (minute maximum), physical series %d", i+1), "#006f79", false, s.Values})
		}
		x.Workspaces = append(x.Workspaces, activeWorkspace{growth, limit.End, makeChart(w.Name+" / ActiveTimeSeries", lines, float64(a.Run.PlatformStart), float64(a.Run.PlatformEnd-60), a.Run)})
		if growth.Baseline == nil {
			beforeOK = false
		} else {
			before += *growth.Baseline
		}
		if growth.End == nil {
			endOK = false
		} else {
			end += *growth.End
		}
		if growth.Delta == nil {
			deltaOK = false
		} else {
			delta += *growth.Delta
		}
		seenMetrics := map[string]bool{}
		for _, inv := range w.Inventories {
			x.Requested++
			item := activeInventory{Workspace: wi, WorkspaceName: w.Name, Metric: inv.Name, BeforeQuery: inv.BeforeQuery, EndQuery: inv.EndQuery, BeforeRequest: inv.Before, EndRequest: inv.End}
			bs, berr := readActiveInventory(inv.Before, a.Records, a.Run.Start)
			es, eerr := readActiveInventory(inv.End, a.Records, a.Run.End)
			if seenMetrics[strings.ToLower(inv.Name)] {
				berr = fmt.Errorf("duplicate inventory metric")
				eerr = berr
			}
			seenMetrics[strings.ToLower(inv.Name)] = true
			if berr != nil || eerr != nil {
				if berr != nil {
					item.Error += fmt.Sprintf("Before unavailable: %v. ", berr)
					bs = nil
				}
				if eerr != nil {
					item.Error += fmt.Sprintf("End unavailable: %v. ", eerr)
					es = nil
				}
				item.Error += "Comparison withheld; independently valid snapshots retained."
			}
			x.Coverage = append(x.Coverage, activeCoverage{wi, inv.Name, berr == nil, eerr == nil})
			if berr == nil {
				x.BeforeMeasured++
				item.Before = finiteSum(float64(len(bs)))
				ib += *item.Before
			}
			if eerr == nil {
				x.EndMeasured++
				item.End = finiteSum(float64(len(es)))
				ie += *item.End
			}
			comparable := berr == nil && eerr == nil
			union := map[string]map[string]string{}
			var adds, drops map[string]map[string]string
			if comparable {
				x.Measured++
				adds, drops = map[string]map[string]string{}, map[string]map[string]string{}
			}
			for id, labels := range bs {
				union[id] = labels
				if _, ok := es[id]; !ok && comparable {
					drops[id] = labels
				}
			}
			for id, labels := range es {
				union[id] = labels
				if _, ok := bs[id]; !ok && comparable {
					adds[id] = labels
				}
			}
			if comparable {
				item.Added, item.Removed, item.Net = finiteSum(float64(len(adds))), finiteSum(float64(len(drops))), finiteSum(float64(len(es)-len(bs)))
				added += *item.Added
				removed += *item.Removed
			}
			for _, p := range []struct {
				name string
				sets map[string]map[string]string
			}{{"Before the run", bs}, {"Added during the run", adds}, {"Removed during the run", drops}, {"At the end", es}} {
				if p.sets != nil {
					item.Snapshots = append(item.Snapshots, analyzeActiveSnapshot(p.name, p.sets))
				}
			}
			ids := make([]string, 0, len(union))
			for id := range union {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				labels := union[id]
				owner := contributionRow{Labels: labels}
				if !assignContributionOwner(&owner, owners) {
					x.Warnings = append(x.Warnings, fmt.Sprintf("%s / %s: ambiguous namespace/HCP ownership", w.Name, inv.Name))
				}
				scope, scopeKey := owner.Customer, "customer:"+owner.CustomerID
				if owner.CustomerID == "" {
					scope = owner.Ownership + " / namespace: " + labels["namespace"] + " / HCP: " + labels["hostedcontrolplane"]
					scopeKey = scope
				}
				collector := "Unattributed"
				if labels["prometheus"] != "" {
					collector = "OSS Prometheus"
				}
				_, b := bs[id]
				_, e := es[id]
				var beforeMembership, endMembership *bool
				if berr == nil {
					beforeMembership = &b
				}
				if eerr == nil {
					endMembership = &e
				}
				x.Rows = append(x.Rows, activeRow{wi, inv.Name, labels, collector, scope, scopeKey, beforeMembership, endMembership})
			}
			x.Inventories = append(x.Inventories, item)
		}
	}
	if beforeOK {
		x.Before = finiteSum(before)
	}
	if endOK {
		x.End = finiteSum(end)
	}
	if deltaOK {
		x.Delta = finiteSum(delta)
	}
	if x.BeforeMeasured > 0 {
		x.InventoryBefore = finiteSum(ib)
	}
	if x.EndMeasured > 0 {
		x.InventoryEnd = finiteSum(ie)
	}
	if x.Measured > 0 {
		x.Added = finiteSum(added)
		x.Removed = finiteSum(removed)
		x.Net = finiteSum(added - removed)
	}
	return x
}
