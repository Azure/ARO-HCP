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
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

// rightSizingSavings compares regular-container requests at one retained fleet
// pod-count peak, not physical compute savings, node capacity or historical replica
// totals or an actual CLI edit plan. Init containers are absent from utilization
// and therefore excluded. Only single-pod rows support change estimates: aggregate
// requests and limits cannot prove uniform per-instance values in multi-pod rows.
// HCP targets are hypothetical minimal-template edits: CI reports do not prove
// size class. Each component shares one target across all HCP namespaces/environments.
type rightSizingSavings struct {
	Time      time.Time                    `json:"time"`
	Resources []rightSizingSavingsResource `json:"resources"`
}

// Counts are concurrent container instances. Unknown requests are omitted from
// both totals, making them lower bounds; known but excluded demand stays unchanged.
// Matched/no-op requests are neither changed nor excluded. Units are cores/bytes.
type rightSizingSavingsResource struct {
	Cluster            string  `json:"cluster"`
	Resource           string  `json:"resource"`
	Before             float64 `json:"before"`
	After              float64 `json:"after"`
	Reductions         float64 `json:"reductions"`
	Increases          float64 `json:"increases"`
	ChangedContainers  int     `json:"changedContainers"`
	ExcludedContainers int     `json:"excludedContainers"`
	UnknownContainers  int     `json:"unknownContainers"`
}

func buildRightSizingSavings(report rightSizingReport, utilization utilizationReport) *rightSizingSavings {
	valid := func(v *float64) bool { return v != nil && *v >= 0 && !math.IsNaN(*v) && !math.IsInf(*v, 0) }
	equal := func(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Max(math.Abs(a), math.Abs(b))) }
	if report.Version != 3 || report.Headroom != 1 || !valid(&report.ChangeThreshold) || report.ChangeThreshold > 1 {
		return nil
	}
	var snapshot *utilizationSnapshot
	peakPods := 0
	for i := range utilization.Snapshots {
		s := &utilization.Snapshots[i]
		if s.Time.Before(report.Start) || s.Time.After(report.End) || s.Time.Before(utilization.Start) || s.Time.After(utilization.End) {
			continue
		}
		pods := 0
		for _, w := range s.Workloads {
			if slices.Contains(utilization.Clusters, w.Cluster) && w.Pods > 0 && w.Pods <= math.MaxInt-pods {
				pods += w.Pods
			}
		}
		if pods > 0 && (snapshot == nil || pods > peakPods || (pods == peakPods && s.Time.Before(snapshot.Time))) {
			snapshot, peakPods = s, pods
		}
	}
	if snapshot == nil {
		return nil
	}
	type identity struct{ cluster, namespace, kind, workload, container, resource string }
	type group struct {
		suggested, peak float64
		blocked         bool
	}
	groups := map[string]*group{}
	matched := map[identity][]rightSizingRecommendation{}
	for _, r := range report.Recommendations {
		if !r.InitContainer {
			id := identity{r.Cluster, r.Namespace, r.Kind, r.Workload, r.Container, r.Resource}
			matched[id] = append(matched[id], r)
		}
		for _, key := range rightSizingTargetKeys(r.Namespace, r.Kind, r.Workload, r.Container, r.Cluster, r.InitContainer) {
			key += "/" + r.Resource
			if groups[key] == nil {
				groups[key] = &group{}
			}
			g := groups[key]
			if r.InitContainer || !r.Eligible || !targets.EditableInCluster(r.Namespace, r.Kind, r.Workload, r.Container, r.Cluster, r.InitContainer) ||
				!valid(r.Suggested) || !valid(r.Peak) || !valid(r.RequestMin) || !valid(r.RequestMax) || *r.RequestMin > *r.RequestMax {
				g.blocked = true
				continue
			}
			g.suggested, g.peak = math.Max(g.suggested, *r.Suggested), math.Max(g.peak, *r.Peak)
		}
	}
	result := &rightSizingSavings{Time: snapshot.Time, Resources: []rightSizingSavingsResource{}}
	rows := map[[2]string]*rightSizingSavingsResource{}
	for _, w := range snapshot.Workloads {
		if !slices.Contains(utilization.Clusters, w.Cluster) || w.Pods <= 0 {
			continue
		}
		for _, resource := range []string{"cpu", "memory"} {
			key := [2]string{w.Cluster, resource}
			if rows[key] == nil {
				rows[key] = &rightSizingSavingsResource{Cluster: w.Cluster, Resource: resource}
			}
			s := rows[key]
			if len(w.Containers) == 0 {
				s.UnknownContainers += w.Pods // At least one unknown container per pod.
			}
			for _, c := range w.Containers {
				request, limit, unlimited := c.Requests.CPU, c.Limits.CPU, c.UnlimitedCPU
				if resource == "memory" {
					request, limit, unlimited = c.Requests.Memory, c.Limits.Memory, c.UnlimitedMemory
				}
				if !valid(request) || math.IsInf(s.Before+*request, 0) || math.IsInf(s.After+*request, 0) {
					s.UnknownContainers += w.Pods
					continue
				}
				s.Before += *request
				s.After += *request
				var g *group
				if keys := rightSizingTargetKeys(w.Namespace, w.Kind, w.Name, c.Name, w.Cluster, false); len(keys) == 1 {
					g = groups[keys[0]+"/"+resource]
				}
				local := matched[identity{w.Cluster, w.Namespace, w.Kind, w.Name, c.Name, resource}]
				if w.Pods != 1 || g == nil || g.blocked || len(local) == 0 ||
					w.Unscheduled || w.PendingPods != 0 || w.Node == "" || w.Node == "unknown" || !targets.EditableInCluster(w.Namespace, w.Kind, w.Name, c.Name, w.Cluster, false) {
					s.ExcludedContainers += w.Pods
					continue
				}
				current := *request
				if equal(current, g.suggested) {
					continue
				}
				observed := false
				for _, r := range local {
					observed = observed || ((current >= *r.RequestMin || equal(current, *r.RequestMin)) && (current <= *r.RequestMax || equal(current, *r.RequestMax)))
				}
				if !observed {
					s.ExcludedContainers += w.Pods
					continue
				}
				fraction := math.Abs(g.suggested-current) / current
				if current > 0 && g.peak <= 1.2*current && (fraction <= report.ChangeThreshold || equal(fraction, report.ChangeThreshold)) {
					continue
				}
				proposed := g.suggested
				if !valid(&proposed) || !valid(limit) || unlimited == nil || (*unlimited != 0 && *unlimited != 1) ||
					(*unlimited == 0 && proposed > *limit) || math.IsInf(s.After-*request+proposed, 0) {
					s.ExcludedContainers += w.Pods
					continue
				}
				s.After += proposed - *request
				s.Reductions += math.Max(0, *request-proposed)
				s.Increases += math.Max(0, proposed-*request)
				s.ChangedContainers += w.Pods
			}
		}
	}
	for _, row := range rows {
		result.Resources = append(result.Resources, *row)
	}
	slices.SortFunc(result.Resources, func(a, b rightSizingSavingsResource) int {
		return cmp.Or(cmp.Compare(a.Cluster, b.Cluster), cmp.Compare(a.Resource, b.Resource))
	})
	return result
}
