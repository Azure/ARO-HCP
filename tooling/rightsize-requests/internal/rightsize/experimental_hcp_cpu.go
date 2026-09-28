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

package rightsize

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

// RunExperimentalHCPCPUInputs is deliberately separate from the strict runners:
// incomplete CI evidence can authorize reductions, never increases or memory edits.
func RunExperimentalHCPCPUInputs(ctx context.Context, log logr.Logger, inputs []string, templatePath, configPath, prefix string, opts Options) error {
	if !opts.AllowDecrease || (templatePath == "") == (configPath == "") {
		return fmt.Errorf("experimental HCP CPU reductions require --allow-decrease and exactly one HCP mode")
	}
	if prefix != "ocm-arohcpci00-" && prefix != "ocm-arohcpci01-" {
		return fmt.Errorf("experimental HCP CPU reductions require CI namespace prefix ocm-arohcpci00- or ocm-arohcpci01-")
	}
	if !finiteNonnegative(opts.ChangeThreshold) || opts.ChangeThreshold > 1 {
		return fmt.Errorf("change threshold must be finite and between 0 and 1")
	}
	if opts.Commit || opts.RenderCmd != "" || opts.GrafanaURL != "" ||
		(opts.SourcePrefix != "" && opts.SourcePrefix != "defaults") || opts.WritePath != "" || opts.WritePrefix != "" {
		return fmt.Errorf("experimental HCP CPU mode cannot query Grafana, render, commit or edit other config paths/prefixes")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Validate every field of every report, including memory, before filtering.
	dataset, err := readDataset(inputs)
	if err != nil {
		return err
	}
	var sizing *editor.SizingEditor
	var additional *editor.AdditionalSizingEditor
	entries := map[[2]string]editor.AdditionalSizingEntry{}
	catalog := targets.MinimalTargets()
	path, scope := templatePath, "limitClusterSizes=true/e2e_minimal"
	if configPath != "" {
		path, scope = configPath, "clouds.dev.defaults.hypershift.additionalMinimalResourceRequests"
		catalog = targets.AdditionalMinimalTargets()
		additional, err = editor.NewAdditionalSizing(path)
		if err != nil {
			return err
		}
		for _, entry := range additional.Entries() {
			entries[[2]string{entry.Deployment, entry.Container}] = entry
		}
		// Check conflicting inherited identities even if no reduction is proposed.
		if _, err := additional.StageExperimentalCPU(nil); err != nil {
			return err
		}
	} else {
		sizing, err = editor.NewSizing(path)
		if err != nil {
			return err
		}
		for _, entry := range sizing.Entries() {
			entries[[2]string{entry.Deployment, entry.Container}] = editor.AdditionalSizingEntry{SizingEntry: entry}
		}
	}
	known := map[[2]string]string{}
	for _, target := range catalog {
		known[[2]string{target.Workload, target.Container}] = target.Kind
	}
	for key := range entries {
		if known[key] == "" {
			return fmt.Errorf("unsupported experimental HCP target %s/%s (original seven are never additional)", key[0], key[1])
		}
	}
	fmt.Printf("\nEXPERIMENTAL HCP CPU reductions (mode=%s, scope=%s, namespace-prefix=%s, reports=%d, CPU=%s, change-threshold=%g, latest-window-end=%s)\n", mode(opts.DryRun), scope, prefix, len(dataset.Reports), dataset.CPUWindow, opts.ChangeThreshold, dataset.End.Format(time.RFC3339))
	for _, source := range dataset.Reports {
		fmt.Printf("SOURCE %s sha256=%s window=%s..%s\n", source.Path, source.SHA256, source.Report.Start.Format(time.RFC3339), source.Report.End.Format(time.RFC3339))
	}
	fmt.Println("WARNING: EXPERIMENTAL: ignoring eligibility, coverage, unmeasured replicas and report absence. Missing peaks remain unknown, never zero. Unresolved identities are skipped, never inferred.")
	fmt.Println("WARNING: CI prefix is a user assertion only; size class is not proven. Use only e2e_minimal samples. Pod limits were not checked. Memory and limits are never changed.")
	fmt.Println("WARNING: Gross CPU reductions only, not net savings or a safety guarantee. Increases are skipped; HCP resource floors are not applied. Incomplete evidence can miss higher peaks.")
	for _, warning := range dataset.Warnings {
		fmt.Printf("WARNING: %s\n", warning)
	}
	groups := map[[2]string][]inputRecommendation{}
	unknown, ignored := 0, 0
	management := regexp.MustCompile(`(?:^|-)mgmt-[0-9]+$`)
	for _, row := range dataset.Recommendations {
		for _, warning := range row.Warnings {
			fmt.Printf("WARNING: source=%s %s/%s %s/%s/%s %s: %s\n", row.source, row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container, row.Resource, warning)
		}
		if row.Resource != "cpu" || !strings.HasPrefix(row.Namespace, prefix) || row.Namespace == prefix {
			ignored++
			continue
		}
		key := [2]string{row.Workload, row.Container}
		matched := known[key] == row.Kind && known[key] != "" && !row.InitContainer && management.MatchString(row.Cluster)
		if additional != nil {
			matched = targets.RequiresConfiguredBaseline(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer)
		}
		if !matched {
			unknown++
			fmt.Printf("WARNING: SKIP unmapped CPU identity source=%s %s/%s %s/%s/%s init=%t; no inference\n", row.source, row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container, row.InitContainer)
			continue
		}
		groups[key] = append(groups[key], row)
	}
	sort.Slice(catalog, func(i, j int) bool {
		return catalog[i].Workload+"/"+catalog[i].Container < catalog[j].Workload+"/"+catalog[j].Container
	})
	var updates []editor.SizingUpdate
	var cpuUpdates []editor.AdditionalSizingEntry
	missingReports, missingPeaks, missingRequests, unmeasured, ineligible, seeds := 0, 0, 0, 0, 0, 0
	for _, target := range catalog {
		key := [2]string{target.Workload, target.Container}
		label := key[0] + "/" + key[1] + "/cpu"
		if err := targets.ValidateResourceRequestOverride(target.Workload, target.Container); err != nil {
			fmt.Printf("SKIP %s: %v\n", label, err)
			continue
		}
		rows := groups[key]
		present := map[string]bool{}
		var best *inputRecommendation
		var baseline *float64
		ambiguous := false
		for i := range rows {
			row := &rows[i]
			present[row.source] = true
			if !row.Eligible {
				ineligible++
			}
			unmeasured += row.Replicas - row.MeasuredReplicas
			if row.Peak == nil {
				missingPeaks++
				fmt.Printf("WARNING: %s unknown peak in %s %s/%s; skipped measurement\n", label, row.source, row.Cluster, row.Namespace)
			} else if best == nil || *row.Peak > *best.Peak {
				best = row
			}
			if row.RequestMin == nil || row.RequestMax == nil {
				missingRequests++
				fmt.Printf("WARNING: %s unknown request bounds in %s %s/%s; known peak retained\n", label, row.source, row.Cluster, row.Namespace)
			}
			for _, bound := range []*float64{row.RequestMin, row.RequestMax} {
				if bound == nil {
					continue
				}
				if *bound <= 0 || (baseline != nil && !inputEqual(*baseline, *bound)) {
					ambiguous = true
				}
				baseline = bound
			}
		}
		for _, source := range dataset.Reports {
			if !present[source.Path] {
				missingReports++
				fmt.Printf("WARNING: %s missing report evidence: %s (waived)\n", label, source.Path)
			}
		}
		if best == nil {
			fmt.Printf("SKIP %s: no known peak; unknown is not zero\n", label)
			continue
		}
		// Suggestions may be absent on ineligible rows. Derive from the raw max,
		// using exactly the validated v3 ceiling policy, with no extra headroom.
		value := math.Max(1, math.Ceil(*best.Peak/.01)) * .01
		if !finiteNonnegative(value * 1000) {
			return fmt.Errorf("%s: rounded peak overflow", label)
		}
		quantity := fmt.Sprintf("%.0fm", value*1000)
		fmt.Printf("EVIDENCE %s governing-max=%g target=%s source=%s %s/%s\n", label, *best.Peak, quantity, best.source, best.Cluster, best.Namespace)
		entry, exists := entries[key]
		seed := additional != nil && (!exists || entry.CPU == "")
		current, err := ParseCPUCores(entry.CPU)
		if seed {
			if baseline == nil || ambiguous {
				fmt.Printf("SKIP %s: seed baseline ambiguous, nonpositive or absent; all known request bounds must agree\n", label)
				continue
			}
			current = *baseline
			if !exists {
				entry = editor.AdditionalSizingEntry{ID: key[0] + "-" + key[1], SizingEntry: editor.SizingEntry{Deployment: key[0], Container: key[1]}}
			}
		} else if err != nil || !finiteNonnegative(current) || current <= 0 {
			fmt.Printf("SKIP %s: positive numeric current CPU request required\n", label)
			continue
		}
		if inputEqual(current, value) {
			fmt.Printf("NOOP %s: current already target %s\n", label, quantity)
			continue
		}
		if value > current {
			fmt.Printf("SKIP increase %s: %gm -> %s\n", label, current*1000, quantity)
			continue
		}
		observed := seed
		for _, row := range rows {
			if row.RequestMin != nil && row.RequestMax != nil &&
				(current >= *row.RequestMin || inputEqual(current, *row.RequestMin)) && (current <= *row.RequestMax || inputEqual(current, *row.RequestMax)) {
				observed = true
			}
		}
		if !observed {
			fmt.Printf("WARNING stale: SKIP %s: current is outside every known observed request range\n", label)
			continue
		}
		fraction := (current - value) / current
		if opts.ChangeThreshold > 0 && (fraction <= opts.ChangeThreshold || inputEqual(fraction, opts.ChangeThreshold)) {
			fmt.Printf("SKIP deadband %s: %gm -> %s\n", label, current*1000, quantity)
			continue
		}
		fmt.Printf("CHANGE %s: %gm -> %s (experimental, seed=%t, id=%s)\n", label, current*1000, quantity, seed, entry.ID)
		if seed {
			seeds++
		}
		updates = append(updates, editor.SizingUpdate{Deployment: key[0], Container: key[1], Resource: "cpu", NewValue: quantity})
		entry.CPU, entry.Memory = quantity, ""
		cpuUpdates = append(cpuUpdates, entry)
	}
	fmt.Printf("SUMMARY experimental CPU: changes=%d seeds=%d ignored-rows=%d unknown-identities=%d missing-report-targets=%d missing-peak-rows=%d missing-request-rows=%d unmeasured-replicas=%d ineligible-rows=%d (incompleteness waived; no net savings or safety claim)\n", len(updates), seeds, ignored, unknown, missingReports, missingPeaks, missingRequests, unmeasured, ineligible)
	var staged *editor.ExperimentalCPUEdit
	if additional != nil {
		// Validate the exact same staging path for dry runs, including collisions.
		staged, err = additional.StageExperimentalCPU(cpuUpdates)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.DryRun || len(updates) == 0 {
		return nil
	}
	if staged != nil {
		err = staged.Apply()
	} else {
		err = sizing.Apply(updates)
	}
	if err != nil {
		return err
	}
	log.Info("applied experimental HCP CPU reductions", "count", len(updates), "seeds", seeds, "file", path)
	return nil
}
