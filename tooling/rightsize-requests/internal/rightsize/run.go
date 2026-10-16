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

// Package rightsize queries production usage from Azure Managed Grafana and
// right-sizes the CPU/memory requests recorded in config/config.yaml.
package rightsize

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/grafana"
)

// Options controls a right-sizing run.
type Options struct {
	ConfigPath        string  // config to read CURRENT values from (source of truth)
	SourcePrefix      string  // dotted prefix in the source config, e.g. "defaults"
	WritePath         string  // config to WRITE new values to (defaults to ConfigPath)
	WritePrefix       string  // dotted prefix in the write config (defaults to SourcePrefix)
	Window            string  // PromQL lookback, e.g. "14d"
	Step              string  // subquery resolution, e.g. "5m"
	Margin            float64 // safety multiplier applied to observed usage, e.g. 1.25
	Percentile        float64 // per-pod aggregation OVER TIME: 0 or >=1 => max (peak); else quantile_over_time (e.g. 0.95)
	FleetPercentile   float64 // aggregation ACROSS PODS/clusters: 0 or >=1 => max; else percentile (e.g. 0.95)
	LimitMultiple     float64 // if a numeric memory limit exists, set it to this multiple of the new request (0 disables)
	DatasourcePattern string  // regexp; only datasources whose uid matches are queried
	GrafanaURL        string  // base Grafana URL, used to build Explore links in the commit message
	RenderCmd         string  // shell command to regenerate rendered configs, run in the write repo root before committing
	Commit            bool    // git-commit the edited file after writing
	DryRun            bool
	AllowDecrease     bool // also lower requests when observed usage is well below current
}

// overTime renders the per-pod over-time aggregation for a metric selector,
// honoring the configured percentile. inner must be a per-pod instant vector
// selector (already grouped to per-pod granularity).
func (o Options) overTime(inner string) string {
	if o.Percentile > 0 && o.Percentile < 1 {
		return fmt.Sprintf("quantile_over_time(%g, (%s)[%s:%s])", o.Percentile, inner, o.Window, o.Step)
	}
	return fmt.Sprintf("max_over_time((%s)[%s:%s])", inner, o.Window, o.Step)
}

// usage accumulates per-pod usage values (each already reduced over time) for a
// workload across every queried datasource (cluster). Keeping the full
// population — rather than only the running max — lets us take a fleet-wide
// percentile so a single anomalous pod or cluster cannot drive the fleet
// request.
type usage struct {
	cpuVals  []float64 // per-pod cores
	memVals  []float64 // per-pod bytes
	clusters map[string]struct{}
	// Per-datasource representative (max-pod) value, used to point an Explore
	// link at a cluster whose usage is REPRESENTATIVE of the chosen fleet
	// percentile - not the outlier cluster the percentile deliberately excludes.
	cpuByDS map[string]float64
	memByDS map[string]float64
}

// nearestDS returns the datasource uid whose value is closest to target.
func nearestDS(byDS map[string]float64, target float64) string {
	best := ""
	bestDiff := math.Inf(1)
	// Deterministic: iterate sorted uids.
	uids := make([]string, 0, len(byDS))
	for uid := range byDS {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		if d := math.Abs(byDS[uid] - target); d < bestDiff {
			bestDiff = d
			best = uid
		}
	}
	return best
}

// fleetValue reduces a per-pod population to a single value using the configured
// fleet percentile (linear interpolation). A percentile of 0 or >=1 means take
// the raw max (the busiest pod).
func fleetValue(vals []float64, pct float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	if pct <= 0 || pct >= 1 {
		return sorted[len(sorted)-1]
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := pct * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (rank-float64(lo))*(sorted[hi]-sorted[lo])
}

// change is a proposed edit to a single request scalar.
type change struct {
	service  string
	resource string // "cpu" or "memory"
	path     string
	oldValue string
	newValue string
	reason   string
	clusters int    // number of clusters that contributed to the observed usage
	explore  string // Grafana Explore link showing the underlying usage
}

// Grafana is the subset of the grafana client used here (for testability).
type Grafana interface {
	ListDatasources(ctx context.Context) ([]grafana.Datasource, error)
	InstantQuery(ctx context.Context, dsUID, query string) ([]grafana.Sample, error)
}

// Run executes the full workflow: discover datasources, query peak usage, map
// results back to config.yaml, and (unless DryRun) apply the edits in place.
func Run(ctx context.Context, log logr.Logger, gc Grafana, opts Options) error {
	if opts.Margin <= 0 {
		opts.Margin = 1.25
	}
	if opts.Window == "" {
		opts.Window = "14d"
	}
	if opts.Step == "" {
		opts.Step = "5m"
	}
	if opts.SourcePrefix == "" {
		opts.SourcePrefix = "defaults"
	}
	if opts.WritePath == "" {
		opts.WritePath = opts.ConfigPath
	}
	if opts.WritePrefix == "" {
		opts.WritePrefix = opts.SourcePrefix
	}

	datasources, err := gc.ListDatasources(ctx)
	if err != nil {
		return fmt.Errorf("listing datasources: %w", err)
	}

	var dsFilter *regexp.Regexp
	if opts.DatasourcePattern != "" {
		dsFilter, err = regexp.Compile(opts.DatasourcePattern)
		if err != nil {
			return fmt.Errorf("invalid --datasource-pattern: %w", err)
		}
	}

	var selected []grafana.Datasource
	for _, ds := range datasources {
		if ds.Type != "prometheus" {
			continue
		}
		if dsFilter != nil && !dsFilter.MatchString(ds.UID) {
			continue
		}
		selected = append(selected, ds)
	}
	if len(selected) == 0 {
		return fmt.Errorf("no matching prometheus datasources found (of %d total)", len(datasources))
	}
	log.Info("selected datasources", "count", len(selected))

	// Azure Managed Prometheus matches =~ UNANCHORED, so we must anchor the
	// namespace regex explicitly; otherwise "aro-hcp" also matches
	// "aro-hcp-admin-api" and hosted-control-plane namespaces like
	// "ocm-...-aro-hcp-lab-bra".
	nsRegex := "^(" + strings.Join(Namespaces(), "|") + ")$"
	// Two-stage aggregation. Stage 1 (in PromQL, per cluster): reduce each pod's
	// time series to a single value using the over-time percentile. We keep pod
	// granularity (no max-by collapse) so stage 2 sees the full population.
	// Stage 2 (in Go, across all pods and clusters): take the fleet percentile,
	// so an anomalous pod or cluster cannot dictate the fleet-wide request.
	memInner := fmt.Sprintf(
		`max by (namespace, container, pod) (container_memory_working_set_bytes{container!="", namespace=~"%s"})`,
		nsRegex,
	)
	cpuInner := fmt.Sprintf(
		`max by (namespace, container, pod) (rate(container_cpu_usage_seconds_total{container!="", namespace=~"%s"}[5m]))`,
		nsRegex,
	)
	memQuery := opts.overTime(memInner)
	cpuQuery := opts.overTime(cpuInner)

	usages := map[[2]string]*usage{}
	getUsage := func(ns, c string) *usage {
		k := [2]string{ns, c}
		u := usages[k]
		if u == nil {
			u = &usage{
				clusters: map[string]struct{}{},
				cpuByDS:  map[string]float64{},
				memByDS:  map[string]float64{},
			}
			usages[k] = u
		}
		return u
	}

	// Query datasources concurrently; each region's Managed Prometheus subquery
	// over a long window is slow, so run them in parallel with a bounded pool.
	type dsResult struct {
		name     string
		mem, cpu []grafana.Sample
		memErr   error
		cpuErr   error
	}
	results := make([]dsResult, len(selected))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, ds := range selected {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, ds grafana.Datasource) {
			defer wg.Done()
			defer func() { <-sem }()
			r := dsResult{name: ds.Name}
			r.mem, r.memErr = gc.InstantQuery(ctx, ds.UID, memQuery)
			if r.memErr == nil {
				r.cpu, r.cpuErr = gc.InstantQuery(ctx, ds.UID, cpuQuery)
			}
			results[i] = r
		}(i, ds)
	}
	wg.Wait()

	dsWithData := map[string]struct{}{}
	dsFailed := 0
	var staleDS []string // datasources whose AMW hostname no longer resolves
	for i, r := range results {
		uid := selected[i].UID
		if r.cpuErr != nil {
			log.V(1).Info("cpu query failed; memory-only for this datasource", "datasource", r.name, "err", r.cpuErr.Error())
		}
		if r.memErr != nil {
			msg := r.memErr.Error()
			if strings.Contains(msg, "no such host") {
				staleDS = append(staleDS, r.name)
				log.V(1).Info("datasource points at a deleted AMW (stale; run grafanactl clean)", "datasource", r.name)
			} else {
				log.V(1).Info("query failed; skipping datasource", "datasource", r.name, "err", msg)
			}
			dsFailed++
			continue
		}
		for _, s := range r.mem {
			ns, c := s.Labels["namespace"], s.Labels["container"]
			if ns == "" || c == "" {
				continue
			}
			u := getUsage(ns, c)
			u.memVals = append(u.memVals, s.Value)
			u.clusters[uid] = struct{}{}
			if s.Value > u.memByDS[uid] {
				u.memByDS[uid] = s.Value
			}
			dsWithData[uid] = struct{}{}
		}
		for _, s := range r.cpu {
			ns, c := s.Labels["namespace"], s.Labels["container"]
			if ns == "" || c == "" {
				continue
			}
			u := getUsage(ns, c)
			u.cpuVals = append(u.cpuVals, s.Value)
			u.clusters[uid] = struct{}{}
			if s.Value > u.cpuByDS[uid] {
				u.cpuByDS[uid] = s.Value
			}
			dsWithData[uid] = struct{}{}
		}
	}
	log.Info("query complete", "datasourcesSelected", len(selected),
		"datasourcesWithData", len(dsWithData), "datasourcesUnreachable", dsFailed,
		"staleDatasources", len(staleDS))
	if len(staleDS) > 0 {
		sort.Strings(staleDS)
		log.Info("stale datasources point at deleted AMWs and were skipped; consider `grafanactl clean`",
			"datasources", strings.Join(staleDS, ","))
	}

	// sourceEd reads current values; targetEd receives edits. They may be the
	// same file (simple case) or different (e.g. read base config.yaml, write a
	// sparse msft overlay).
	sourceEd, err := editor.New(opts.ConfigPath)
	if err != nil {
		return err
	}
	targetEd := sourceEd
	if opts.WritePath != opts.ConfigPath {
		targetEd, err = editor.New(opts.WritePath)
		if err != nil {
			return err
		}
	}

	// effectiveCurrent returns the current value for a resource: the override in
	// the target (write) config if present, else the source config value.
	effectiveCurrent := func(sourcePath, writePath string) (string, bool) {
		if v, _, err := targetEd.Get(writePath); err == nil {
			return v, true
		}
		if v, _, err := sourceEd.Get(sourcePath); err == nil {
			return v, true
		}
		return "", false
	}

	var changes []change
	var unmapped []string

	// Deterministic ordering.
	keys := make([][2]string, 0, len(usages))
	for k := range usages {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	for _, k := range keys {
		ns, container := k[0], k[1]
		u := usages[k]
		cpuCores := fleetValue(u.cpuVals, opts.FleetPercentile)
		memBytes := fleetValue(u.memVals, opts.FleetPercentile)

		target, ok := Lookup(ns, container)
		if !ok {
			unmapped = append(unmapped, fmt.Sprintf("%s/%s (cpu=%s mem=%s, %d pods/%d clusters)",
				ns, container, FormatCPU(cpuCores), FormatMemory(memBytes), len(u.cpuVals), len(u.clusters)))
			continue
		}

		// Explore link pinned to a REPRESENTATIVE cluster: the one whose usage is
		// closest to the chosen fleet percentile (not the outlier cluster the
		// percentile deliberately excludes). Prefer the memory-driven cluster,
		// falling back to CPU for cpu-only services.
		exploreURL := ""
		if opts.GrafanaURL != "" {
			exDS := ""
			if len(u.memVals) > 0 {
				exDS = nearestDS(u.memByDS, memBytes)
			}
			if exDS == "" {
				exDS = nearestDS(u.cpuByDS, cpuCores)
			}
			if exDS != "" {
				exploreURL = grafana.ExploreURL(opts.GrafanaURL, exDS, []grafana.ExploreQuery{
					{RefID: "A", Expr: fmt.Sprintf(`max by (namespace, container, pod) (container_memory_working_set_bytes{namespace=%q, container=%q})`, ns, container)},
					{RefID: "B", Expr: fmt.Sprintf(`max by (namespace, container, pod) (rate(container_cpu_usage_seconds_total{namespace=%q, container=%q}[5m]))`, ns, container)},
				}, "now-"+opts.Window, "now")
			}
		}

		if len(u.cpuVals) > 0 {
			cur, found := effectiveCurrent(target.requestPath(opts.SourcePrefix, "cpu"), target.requestPath(opts.WritePrefix, "cpu"))
			if found {
				if c, ok := decide(target.Service, "cpu", target.requestPath(opts.WritePrefix, "cpu"),
					cur, FormatCPU(cpuCores*opts.Margin), cpuCores*opts.Margin,
					ParseCPUCores, opts.AllowDecrease); ok {
					c.clusters = len(u.clusters)
					c.explore = exploreURL
					changes = append(changes, c)
				}
			}
		}
		if len(u.memVals) > 0 {
			memNumeric := memBytes * opts.Margin
			newReq := FormatMemory(memNumeric)
			cur, found := effectiveCurrent(target.requestPath(opts.SourcePrefix, "memory"), target.requestPath(opts.WritePrefix, "memory"))
			if found {
				if c, ok := decide(target.Service, "memory", target.requestPath(opts.WritePrefix, "memory"),
					cur, newReq, memNumeric, ParseMemoryBytes, opts.AllowDecrease); ok {
					c.clusters = len(u.clusters)
					c.explore = exploreURL
					changes = append(changes, c)

					// limits = LimitMultiple x request, but only for containers
					// that already set a NUMERIC memory limit (leave "unlimited"
					// / absent limits alone).
					if opts.LimitMultiple > 0 {
						limCur, limFound := effectiveCurrent(target.limitPath(opts.SourcePrefix, "memory"), target.limitPath(opts.WritePrefix, "memory"))
						if limFound && !IsSentinel(limCur) {
							if _, perr := ParseMemoryBytes(limCur); perr == nil {
								newLim := FormatMemory(memNumeric * opts.LimitMultiple)
								if newLim != limCur {
									changes = append(changes, change{
										service: target.Service, resource: "mem-limit",
										path:     target.limitPath(opts.WritePrefix, "memory"),
										oldValue: limCur, newValue: newLim,
										reason:   fmt.Sprintf("%gx request", opts.LimitMultiple),
										clusters: len(u.clusters),
										explore:  exploreURL,
									})
								}
							}
						}
					}
				}
			}
		}
	}

	report(log, changes, unmapped, opts)

	if opts.DryRun || len(changes) == 0 {
		return nil
	}

	updates := make([]editor.Update, 0, len(changes))
	for _, c := range changes {
		updates = append(updates, editor.Update{Path: c.path, NewValue: c.newValue})
	}
	if err := targetEd.Upsert(updates); err != nil {
		return fmt.Errorf("applying updates to %s: %w", opts.WritePath, err)
	}
	log.Info("applied updates", "count", len(changes), "file", opts.WritePath)

	// Regenerate rendered configuration (if configured) so the committed state is
	// consistent; its output is folded into the commit.
	rendered := false
	if opts.RenderCmd != "" {
		if err := runRender(opts.WritePath, opts.RenderCmd, log); err != nil {
			return fmt.Errorf("render: %w", err)
		}
		rendered = true
	} else {
		log.Info("remember to regenerate rendered configs (e.g. make -C config materialize)")
	}

	if opts.Commit {
		msg := buildCommitMessage(changes, opts)
		if err := gitCommit(opts.WritePath, msg, rendered); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}
		log.Info("committed changes", "file", opts.WritePath)
	}
	return nil
}

// runRender executes cmdStr (regenerating rendered configuration) from the git
// repository root that contains writePath.
func runRender(writePath, cmdStr string, log logr.Logger) error {
	root, err := repoRoot(writePath)
	if err != nil {
		root = filepath.Dir(writePath)
	}
	log.Info("rendering service configuration", "cmd", cmdStr, "dir", root)
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%q failed: %v: %s", cmdStr, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func repoRoot(file string) (string, error) {
	out, err := exec.Command("git", "-C", filepath.Dir(file), "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// buildCommitMessage renders a commit message in the team's format: a title, a
// short rationale, and a per-service summary with a Grafana Explore link.
func buildCommitMessage(changes []change, opts Options) string {
	// Group changes by service, preserving first-seen order.
	var order []string
	byService := map[string][]change{}
	for _, c := range changes {
		if _, ok := byService[c.service]; !ok {
			order = append(order, c.service)
		}
		byService[c.service] = append(byService[c.service], c)
	}

	var b strings.Builder
	b.WriteString("hcp: right-size service CPU/memory requests from prod usage\n\n")
	fmt.Fprintf(&b,
		"Set fleet-wide CPU/memory requests for ARO-HCP services based on observed\n"+
			"production usage, computed by tooling/rightsize-requests against the prod\n"+
			"Grafana (Azure Monitor Workspace) datasources. Values are the fleet %s of\n"+
			"each pod's %s-over-%s usage x%.2f margin, aggregated across all reachable\n"+
			"prod service clusters (a fleet percentile so a single anomalous pod or\n"+
			"cluster does not drive the request).\n\n",
		statistic(opts.FleetPercentile), statistic(opts.Percentile), opts.Window, opts.Margin)

	for _, svc := range order {
		cs := byService[svc]
		parts := make([]string, 0, len(cs))
		explore := ""
		for _, c := range cs {
			label := c.resource
			switch c.resource {
			case "mem-limit":
				parts = append(parts, fmt.Sprintf("limit %s -> %s (%s)", c.oldValue, c.newValue, c.reason))
				continue
			case "memory":
				label = "memory"
			case "cpu":
				label = "cpu"
			}
			parts = append(parts, fmt.Sprintf("%s %s -> %s", label, c.oldValue, c.newValue))
			if c.explore != "" {
				explore = c.explore
			}
		}
		fmt.Fprintf(&b, "- %s: %s\n", svc, strings.Join(parts, ", "))
		if explore != "" {
			fmt.Fprintf(&b, "  explore: %s\n", explore)
		}
	}
	return b.String()
}

// gitCommit stages and commits changes in the repository containing file, using
// message on stdin. When stageAll is set (e.g. after a render step), all changes
// in the repo are staged so regenerated files are included; otherwise only the
// edited file is staged.
func gitCommit(file, message string, stageAll bool) error {
	dir := filepath.Dir(file)
	root := dir
	if r, err := repoRoot(file); err == nil {
		root = r
	}
	if stageAll {
		if out, err := exec.Command("git", "-C", root, "add", "-A").CombinedOutput(); err != nil {
			return fmt.Errorf("git add -A: %v: %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		if out, err := exec.Command("git", "-C", dir, "add", file).CombinedOutput(); err != nil {
			return fmt.Errorf("git add: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	cmd := exec.Command("git", "-C", root, "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type parseFn func(string) (float64, error)

// decide compares a proposed value against the current value and returns a
// change if warranted (an increase, or a large decrease when allowed).
func decide(service, res, path, cur, newValue string, newNumeric float64, parse parseFn, allowDecrease bool) (change, bool) {
	if IsSentinel(cur) {
		return change{}, false // never touch NONE/unlimited
	}
	curNumeric, err := parse(cur)
	if err != nil {
		return change{}, false
	}
	if newValue == cur {
		return change{}, false
	}
	switch {
	case newNumeric > curNumeric:
		return change{service: service, resource: res, path: path, oldValue: cur, newValue: newValue, reason: "usage exceeds request"}, true
	case allowDecrease && newNumeric < curNumeric*0.5:
		return change{service: service, resource: res, path: path, oldValue: cur, newValue: newValue, reason: "request oversized"}, true
	default:
		return change{}, false
	}
}

func report(log logr.Logger, changes []change, unmapped []string, opts Options) {
	fmt.Printf("\nRight-sizing report (window=%s, per-pod=%s over time, fleet=%s across pods, margin=%.2fx, mode=%s)\n",
		opts.Window, statistic(opts.Percentile), statistic(opts.FleetPercentile), opts.Margin, mode(opts.DryRun))
	fmt.Println(strings.Repeat("-", 96))
	if len(changes) == 0 {
		fmt.Println("No request changes needed.")
	} else {
		fmt.Printf("%-24s %-8s %-12s %-12s %-8s %s\n", "SERVICE", "RESOURCE", "CURRENT", "PROPOSED", "CLUSTERS", "REASON")
		for _, c := range changes {
			fmt.Printf("%-24s %-8s %-12s %-12s %-8d %s\n", c.service, c.resource, c.oldValue, c.newValue, c.clusters, c.reason)
		}
	}
	if len(unmapped) > 0 {
		fmt.Println(strings.Repeat("-", 96))
		fmt.Println("Observed workloads with NO config mapping (extend internal/rightsize/mapping.go):")
		for _, u := range unmapped {
			fmt.Printf("  - %s\n", u)
		}
	}
	fmt.Println(strings.Repeat("-", 96))
}

func statistic(p float64) string {
	if p > 0 && p < 1 {
		return fmt.Sprintf("p%g", p*100)
	}
	return "max"
}

func mode(dryRun bool) string {
	if dryRun {
		return "dry-run"
	}
	return "write"
}
