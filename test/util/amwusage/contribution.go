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
	"sort"
	"strings"
	"time"
)

type contributionRow struct {
	Workspace       int               `json:"workspace"`
	WorkspaceName   string            `json:"workspaceName"`
	Metric          string            `json:"metric"`
	Labels          map[string]string `json:"labels"`
	Collector       string            `json:"collector"`
	RunSeries       *float64          `json:"runSeries"`
	NewSeries       *float64          `json:"newSeries"`
	Samples         *float64          `json:"samples"`
	Partial         []string          `json:"partial"`
	BaselineSeries  *float64          `json:"baselineSeries"`
	BaselineSamples *float64          `json:"baselineSamples"`
	RunRate         *float64          `json:"runRate"`
	BaselineRate    *float64          `json:"baselineRate"`
	RateDelta       *float64          `json:"rateDelta"`
	Ownership       string            `json:"ownership"`
	CustomerID      string            `json:"customerID"`
	Customer        string            `json:"customer"`
	Cohort          string            `json:"cohort"`
}

type contributionRank struct {
	contributionRow
	Rank                      int
	Weight, Share, Cumulative *float64
	Suggestion                string
}

type contributionAnalysis struct {
	Rows                           []contributionRow   `json:"rows"`
	Warnings                       []string            `json:"warnings"`
	DefaultWeight                  string              `json:"defaultWeight"`
	Duration                       float64             `json:"duration"`
	Ranked                         []contributionRank  `json:"-"`
	Total                          *float64            `json:"-"`
	Measured                       int                 `json:"-"`
	WeightLabel                    string              `json:"-"`
	Growth                         []workspaceGrowth   `json:"-"`
	SamplesTotal, SamplesPerMinute *float64            `json:"-"`
	SampleRows                     int                 `json:"-"`
	BaselineStart, BaselineEnd     float64             `json:"-"`
	BaselineDuration               float64             `json:"baselineDuration"`
	BaselineValid                  bool                `json:"baselineValid"`
	ContextValid                   bool                `json:"contextValid"`
	NewLabel                       string              `json:"newLabel"`
	Scopes                         []contributionScope `json:"-"`
}

type contributionScope struct {
	Name                                                                           string
	Rows                                                                           int
	RunSeries, NewSeries, BaselineSeries, RunRate, BaselineRate, RateDelta         *float64
	RunKnown, NewKnown, BaselineKnown, RunRateKnown, BaselineRateKnown, DeltaKnown int
	Partial                                                                        bool
}

const (
	ownershipRun       = "Run-owned customer cluster"
	ownershipOther     = "Other HCP / ownership unknown"
	ownershipShared    = "Shared / unattributed"
	ownershipAmbiguous = "Ambiguous"
)

// Validate period metadata before using it as a measurement clock or ownership
// inventory. Invalid context must not silently turn into legacy 12-hour semantics.
func contributionContext(a *artifactData, c *contributionAnalysis) map[string]map[string]artifactCluster {
	owners := map[string]map[string]artifactCluster{}
	if a.Context == nil {
		return owners
	}
	ctx := a.Context
	seconds := func(raw string) (float64, error) {
		t, err := time.Parse(time.RFC3339Nano, raw)
		return float64(t.Unix()) + float64(t.Nanosecond())/1e9, err
	}
	start, startErr := seconds(ctx.Run.Start)
	end, endErr := seconds(ctx.Run.End)
	if ctx.SchemaVersion != 1 || startErr != nil || endErr != nil || math.Abs(start-a.Run.Start) > 0.001 || math.Abs(end-a.Run.End) > 0.001 || (a.Run.Prow != "" && ctx.Run.Prow != "" && a.Run.Prow != ctx.Run.Prow) {
		c.Warnings = append(c.Warnings, "Context run/schema does not match archive; baseline and ownership attribution withheld")
		return owners
	}
	c.ContextValid = true
	bs, bsErr := seconds(ctx.Baseline.Start)
	be, beErr := seconds(ctx.Baseline.End)
	if bsErr == nil && beErr == nil && bs >= 0 && be > bs && be <= start && ctx.Baseline.Reason != "" {
		c.BaselineStart, c.BaselineEnd, c.BaselineDuration, c.BaselineValid = bs, be, be-bs, true
	} else {
		c.Warnings = append(c.Warnings, "Invalid explicit baseline window/reason; baseline measures and new-vs-baseline comparison withheld")
	}
	for _, cluster := range ctx.Clusters {
		if cluster.ID == "" {
			c.Warnings = append(c.Warnings, "Ownership entry without cluster ID ignored")
			continue
		}
		for _, namespace := range cluster.Namespaces {
			if namespace == "" {
				continue
			}
			key := strings.ToLower(namespace)
			if owners[key] == nil {
				owners[key] = map[string]artifactCluster{}
			}
			owners[key][cluster.ID] = cluster
		}
	}
	return owners
}

func assignContributionOwner(r *contributionRow, owners map[string]map[string]artifactCluster) bool {
	r.Ownership = ownershipShared
	r.CustomerID, r.Customer = "", ""
	matches := map[string]artifactCluster{}
	for _, field := range []string{"namespace", "hostedcontrolplane"} {
		literal := strings.ToLower(r.Labels[field])
		if strings.HasPrefix(literal, "ocm-") {
			r.Ownership = ownershipOther
		}
		for id, cluster := range owners[literal] {
			matches[id] = cluster
		}
	}
	if len(matches) > 1 {
		r.Ownership, r.Customer = ownershipAmbiguous, ownershipAmbiguous
		return false
	}
	for id, cluster := range matches {
		r.Ownership, r.CustomerID = ownershipRun, id
		// Extend the displayed ID prefix until unique, but always group by full ID.
		n := min(8, len(id))
		for _, candidates := range owners {
			for other := range candidates {
				if other == id {
					continue
				}
				for n < len(id) && strings.HasPrefix(other, id[:n]) {
					n++
				}
			}
		}
		r.Customer = cluster.Name + " (" + id[:n] + ")"
	}
	if r.Customer == "" {
		r.Customer = r.Ownership
	}
	return true
}

func summarizeContributionScope(name string, rows []contributionRow) contributionScope {
	s := contributionScope{Name: name, Rows: len(rows)}
	for _, row := range rows {
		values := []*float64{row.RunSeries, row.NewSeries, row.BaselineSeries, row.RunRate, row.BaselineRate, row.RateDelta}
		totals := []**float64{&s.RunSeries, &s.NewSeries, &s.BaselineSeries, &s.RunRate, &s.BaselineRate, &s.RateDelta}
		known := []*int{&s.RunKnown, &s.NewKnown, &s.BaselineKnown, &s.RunRateKnown, &s.BaselineRateKnown, &s.DeltaKnown}
		for i, value := range values {
			if value == nil {
				continue
			}
			if *known[i] == 0 {
				*totals[i] = finiteSum(0)
			}
			(*known[i])++
			if *totals[i] != nil {
				*totals[i] = finiteSum(**totals[i] + *value)
			}
		}
		s.Partial = s.Partial || len(row.Partial) > 0
	}
	return s
}

type workspaceGrowth struct {
	Name                       string
	Baseline, End, Peak, Delta *float64
	BaselineTime, EndTime      float64
	Complete                   bool
}

// AMW can return different casing across queries. Join only the exact grouped
// dimensions (including prometheus), preserving the run-seen spelling for display.
func contributionLabels(labels map[string]string) (map[string]string, string, bool) {
	normal := map[string]string{}
	valid := true
	for key, value := range labels {
		key = strings.ToLower(key)
		if _, exists := normal[key]; exists {
			valid = false
		}
		normal[key] = value
	}
	keys := []string{"cluster", "job", "namespace", "hostedcontrolplane", "prometheus"}
	canonical := make([]string, len(keys))
	display := map[string]string{}
	for i, key := range keys {
		display[key] = normal[key]
		canonical[i] = strings.ToLower(normal[key])
	}
	for key := range normal {
		if _, ok := display[key]; !ok {
			valid = false
		}
	}
	b, _ := json.Marshal(canonical)
	return display, string(b), valid
}

type contributionVector struct {
	groups   map[string]renderGroup
	complete bool
	warnings []string
}

func readContribution(ref string, records map[string]artifactRecord, end float64) contributionVector {
	v := contributionVector{groups: map[string]renderGroup{}}
	if ref == "" {
		v.warnings = append(v.warnings, "Not collected (older cache or unplanned query)")
		return v
	}
	var response promResponse
	if state := decodeResponse(records[ref], &response); state != "" {
		v.warnings = append(v.warnings, state)
		return v
	}
	if response.Status != "success" || response.Data.Result == nil || (response.Data.ResultType != "" && response.Data.ResultType != "vector") {
		v.warnings = append(v.warnings, "Unsuccessful or malformed instant response")
		return v
	}
	v.complete = len(response.Warnings) == 0
	v.warnings = append(v.warnings, response.Warnings...)
	invalid := false
	total := 0.0
	for _, item := range response.Data.Result {
		labels, key, validLabels := contributionLabels(item.Metric)
		var count *float64
		if len(item.Value) == 2 {
			timestamp := finiteValue(item.Value[0])
			if timestamp != nil && math.Abs(*timestamp-end) < 0.001 {
				count = finiteValue(item.Value[1])
			}
		}
		_, duplicate := v.groups[key]
		if count == nil || (count != nil && (*count < 0 || math.Trunc(*count) != *count)) || !validLabels || duplicate {
			invalid = true
			v.warnings = append(v.warnings, "Invalid count, timestamp, grouped labels or duplicate canonical group: "+key)
		} else {
			total += *count
		}
		v.groups[key] = renderGroup{Labels: labels, Count: count}
	}
	if finiteSum(total) == nil {
		invalid = true
		v.warnings = append(v.warnings, "Metric total overflow")
	}
	if invalid {
		// A corrupt group poisons this metric's measure, not just its own row.
		v.complete = false
		for key, group := range v.groups {
			group.Count = nil
			v.groups[key] = group
		}
	}
	return v
}

func analyzeContributions(a *artifactData, summary renderAnalysis) contributionAnalysis {
	c := contributionAnalysis{Rows: []contributionRow{}, DefaultWeight: "runSeries", Duration: a.Run.End - a.Run.Start, NewLabel: "Absent in preceding12h"}
	owners := contributionContext(a, &c)
	if a.Context != nil {
		c.NewLabel = "New vs baseline (unavailable)"
		if c.BaselineValid {
			c.NewLabel = "New vs explicit quiet baseline"
		}
	}
	for wi, w := range a.Workspaces {
		c.Growth = append(c.Growth, analyzeWorkspaceGrowth(w.Name, summary.Workspaces[wi].Platform["ActiveTimeSeries"], a.Run))
		for _, metric := range w.Metrics {
			vectors := []contributionVector{
				readContribution(metric.Instant, a.Records, a.Run.End),
				readContribution(metric.NewSeries, a.Records, a.Run.End),
				readContribution(metric.Samples, a.Records, a.Run.End),
			}
			if a.Context != nil && !c.BaselineValid {
				vectors[1] = contributionVector{}
			}
			if c.BaselineValid {
				vectors = append(vectors, readContribution(metric.BaselineSeries, a.Records, c.BaselineEnd), readContribution(metric.BaselineSamples, a.Records, c.BaselineEnd))
			}
			groups := map[string]map[string]string{}
			for vi, v := range vectors {
				for _, warning := range v.warnings {
					c.Warnings = append(c.Warnings, fmt.Sprintf("%s / %s / %s: %s", w.Name, metric.Name, []string{"Series seen", c.NewLabel, "Stored samples", "Baseline series", "Baseline samples"}[vi], warning))
				}
				for key, group := range v.groups {
					if _, ok := groups[key]; !ok {
						groups[key] = group.Labels
					}
				}
			}
			// Keep failed/empty metrics visible rather than silently dropping selection coverage.
			if len(groups) == 0 {
				groups[""] = map[string]string{}
			}
			keys := make([]string, 0, len(groups))
			for key := range groups {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				r := contributionRow{Workspace: wi, WorkspaceName: w.Name, Metric: metric.Name, Labels: groups[key], Collector: "Unattributed"}
				if r.Labels["prometheus"] != "" {
					r.Collector = "OSS Prometheus"
				}
				values := []**float64{&r.RunSeries, &r.NewSeries, &r.Samples, &r.BaselineSeries, &r.BaselineSamples}
				for vi, v := range vectors {
					if group, ok := v.groups[key]; ok {
						*values[vi] = group.Count
						if group.Count != nil && !v.complete {
							r.Partial = append(r.Partial, []string{"runSeries", "newSeries", "samples", "baselineSeries", "baselineSamples"}[vi])
						}
					} else if v.complete {
						*values[vi] = finiteSum(0)
					}
				}
				if !assignContributionOwner(&r, owners) {
					c.Warnings = append(c.Warnings, fmt.Sprintf("%s / %s: Ambiguous ownership for namespace %q / HCP %q; no customer assigned", w.Name, metric.Name, r.Labels["namespace"], r.Labels["hostedcontrolplane"]))
				}
				r.Cohort = "Period presence unknown"
				if r.RunSeries != nil && r.BaselineSeries != nil {
					switch {
					case *r.RunSeries > 0 && *r.BaselineSeries > 0:
						r.Cohort = "Group present in both periods"
					case *r.RunSeries > 0:
						r.Cohort = "Group present only in run"
					case *r.BaselineSeries > 0:
						r.Cohort = "Group present only in baseline"
					default:
						r.Cohort = "No series in either period"
					}
				}
				if r.Samples != nil {
					r.RunRate = finiteSum(*r.Samples / (c.Duration / 60))
				}
				if r.BaselineSamples != nil && c.BaselineDuration > 0 {
					r.BaselineRate = finiteSum(*r.BaselineSamples / (c.BaselineDuration / 60))
				}
				partialSamples := false
				for _, p := range r.Partial {
					if p == "samples" {
						r.Partial = append(r.Partial, "runRate")
						partialSamples = true
					}
					if p == "baselineSamples" {
						r.Partial = append(r.Partial, "baselineRate")
						partialSamples = true
					}
					if p == "runSeries" || p == "baselineSeries" {
						r.Cohort = "Period presence unknown"
					}
				}
				if r.RunRate != nil && r.BaselineRate != nil && !partialSamples {
					r.RateDelta = finiteSum(*r.RunRate - *r.BaselineRate)
				}
				if _, ok := vectors[1].groups[key]; ok {
					seen, exists := vectors[0].groups[key]
					if (!exists && vectors[0].complete) || (seen.Count != nil && r.NewSeries != nil && *r.NewSeries > *seen.Count) {
						c.Warnings = append(c.Warnings, fmt.Sprintf("%s / %s: new-series group absent from run-seen groups or count exceeds series seen: %s (not clamped)", w.Name, metric.Name, key))
					}
				}
				if r.NewSeries != nil {
					c.DefaultWeight = "newSeries"
				}
				c.Rows = append(c.Rows, r)
			}
		}
	}
	c.WeightLabel = "Series seen"
	if c.DefaultWeight == "newSeries" {
		c.WeightLabel = c.NewLabel
	}
	c.Scopes = append(c.Scopes, summarizeContributionScope("All regional", c.Rows))
	for _, category := range []string{ownershipRun, ownershipOther, ownershipShared, ownershipAmbiguous} {
		var rows []contributionRow
		for _, row := range c.Rows {
			if row.Ownership == category {
				rows = append(rows, row)
			}
		}
		c.Scopes = append(c.Scopes, summarizeContributionScope(category, rows))
	}
	total := 0.0
	samples := 0.0
	for _, r := range c.Rows {
		if r.Samples != nil {
			samples += *r.Samples
			c.SampleRows++
		}
		value := r.RunSeries
		if c.DefaultWeight == "newSeries" {
			value = r.NewSeries
		}
		if value != nil {
			total += *value
			c.Measured++
		}
		c.Ranked = append(c.Ranked, contributionRank{contributionRow: r, Weight: value, Suggestion: contributionSuggestion(r)})
	}
	if c.Measured > 0 {
		c.Total = finiteSum(total)
	}
	if c.SampleRows > 0 {
		c.SamplesTotal = finiteSum(samples)
		c.SamplesPerMinute = finiteSum(samples / (c.Duration / 60))
	}
	sort.SliceStable(c.Ranked, func(i, j int) bool {
		if c.Ranked[i].Weight == nil {
			return false
		}
		return c.Ranked[j].Weight == nil || *c.Ranked[i].Weight > *c.Ranked[j].Weight
	})
	cumulative := 0.0
	for i := range c.Ranked {
		r := &c.Ranked[i]
		r.Rank = i + 1
		if r.Samples != nil && c.SamplesTotal != nil && *c.SamplesTotal > 0 && *r.Samples / *c.SamplesTotal >= 0.2 {
			if r.Suggestion != "" {
				r.Suggestion += "; "
			}
			r.Suggestion += "Review scrape interval/metric value"
		}
		if r.Weight != nil && c.Total != nil && *c.Total > 0 {
			r.Share = finiteSum(*r.Weight / *c.Total)
			cumulative += *r.Share
			r.Cumulative = finiteSum(cumulative)
		}
	}
	return c
}

func contributionSuggestion(r contributionRow) string {
	var suggestions []string
	if strings.HasSuffix(strings.ToLower(r.Metric), "_bucket") {
		suggestions = append(suggestions, "Inspect histogram dimensions")
	}
	if r.NewSeries != nil && r.RunSeries != nil && *r.RunSeries > 0 && *r.NewSeries / *r.RunSeries >= 0.5 {
		suggestions = append(suggestions, "Inspect target churn / lifecycle (not causality)")
	}
	return strings.Join(suggestions, "; ")
}

func analyzeWorkspaceGrowth(name string, p platformAnalysis, run *artifactRun) workspaceGrowth {
	g := workspaceGrowth{Name: name}
	if p.State != "" || len(p.Series) != 1 || p.Series[0].Interval != "PT1M" {
		return g
	}
	s := p.Series[0]
	start, end := -1, -1
	for i, point := range s.Values {
		if point.Time <= run.Start && run.Start-point.Time < 60 {
			start = i
		}
		if point.Time <= run.End && run.End-point.Time < 60 {
			end = i
		}
	}
	if start >= 0 {
		g.BaselineTime, g.Baseline = s.Values[start].Time, s.Values[start].Value
	}
	if end >= 0 {
		g.EndTime, g.End = s.Values[end].Time, s.Values[end].Value
	}
	if g.Baseline != nil && *g.Baseline < 0 {
		g.Baseline = nil
	}
	if g.End != nil && *g.End < 0 {
		g.End = nil
	}
	if g.Baseline != nil && g.End != nil {
		g.Delta = finiteSum(*g.End - *g.Baseline)
	}
	if start < 0 || end < start {
		return g
	}
	g.Complete = true
	for _, point := range s.Values[start : end+1] {
		if point.Value == nil || *point.Value < 0 {
			g.Complete = false
			continue
		}
		if g.Peak == nil || *point.Value > *g.Peak {
			g.Peak = point.Value
		}
	}
	if !g.Complete {
		g.Peak = nil
	}
	return g
}
