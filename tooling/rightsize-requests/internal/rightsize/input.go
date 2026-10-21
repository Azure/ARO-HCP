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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
)

type inputReport struct {
	Version         int                   `json:"version"`
	Start           time.Time             `json:"start"`
	End             time.Time             `json:"end"`
	Headroom        float64               `json:"headroom"`
	ChangeThreshold float64               `json:"changeThreshold"`
	CPUWindow       string                `json:"cpuWindow"`
	Warnings        []string              `json:"warnings"`
	Recommendations []inputRecommendation `json:"recommendations"`
	Savings         *inputSavings         `json:"savings,omitempty"`
}

// Savings is informational only; edit decisions use recommendation evidence.
type inputSavings struct {
	Time      time.Time              `json:"time"`
	Resources []inputResourceSavings `json:"resources"`
}

type inputResourceSavings struct {
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

type inputRecommendation struct {
	source            string
	Cluster           string   `json:"cluster"`
	Namespace         string   `json:"namespace"`
	Kind              string   `json:"kind"`
	Workload          string   `json:"workload"`
	Container         string   `json:"container"`
	Resource          string   `json:"resource"`
	InitContainer     bool     `json:"initContainer"`
	Replicas          int      `json:"replicas"`
	MeasuredReplicas  int      `json:"measuredReplicas"`
	Samples           *int     `json:"samples,omitempty"`
	Peak              *float64 `json:"peak"`
	BurstPeak         *float64 `json:"burstPeak"`
	RequestMin        *float64 `json:"requestMin"`
	RequestMax        *float64 `json:"requestMax"`
	Suggested         *float64 `json:"suggested"`
	SuggestedQuantity string   `json:"suggestedQuantity"`
	Delta             *float64 `json:"delta"`
	Direction         string   `json:"direction"`
	Eligible          bool     `json:"eligible"`
	Actionable        bool     `json:"actionable"`
	AlertRisk         bool     `json:"alertRisk"`
	Warnings          []string `json:"warnings"`
}

// checkInputSchema complements encoding/json: exact keys (required unless
// omitempty), no duplicate keys or null scalars. Nullable values remain unknown.
func checkInputSchema(d *json.Decoder, typ reflect.Type) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if typ.Kind() == reflect.Pointer {
		if token == nil {
			return nil
		}
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[time.Time]() {
		if _, ok := token.(string); !ok {
			return fmt.Errorf("timestamp must be a string")
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return fmt.Errorf("expected object")
		}
		type fieldInfo struct {
			typ      reflect.Type
			required bool
		}
		fields := map[string]fieldInfo{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, options, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.PkgPath != "" || name == "-" {
				continue
			}
			fields[name] = fieldInfo{f.Type, !strings.Contains(","+options+",", ",omitempty,")}
		}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			field, exists := fields[name]
			if !ok || !exists {
				return fmt.Errorf("unknown or duplicate field %q", key)
			}
			if err := checkInputSchema(d, field.typ); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			delete(fields, name)
		}
		for _, field := range fields {
			if field.required {
				return fmt.Errorf("missing required fields in %s", typ.Name())
			}
		}
		_, err = d.Token()
		return err
	case reflect.Slice:
		if token != json.Delim('[') {
			return fmt.Errorf("expected array")
		}
		for d.More() {
			if err := checkInputSchema(d, typ.Elem()); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	default:
		if token == nil {
			return fmt.Errorf("null scalar is not allowed")
		}
		if _, ok := token.(json.Delim); ok {
			return fmt.Errorf("expected scalar")
		}
		return nil // The typed decoder checks scalar types and integer bounds.
	}
}

func readInput(path string) (inputReport, error) {
	var report inputReport
	data, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := checkInputSchema(d, reflect.TypeFor[inputReport]()); err != nil {
		return report, fmt.Errorf("input schema: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return report, fmt.Errorf("input must contain exactly one JSON object (trailing data)")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&report); err != nil {
		return report, fmt.Errorf("decode input: %w", err)
	}
	if report.Version != 3 {
		return report, fmt.Errorf("input requires right-sizing report version=3; version 1 nearest-rounded and version 2 120%% reports are incompatible: rerender replica peaks via render-right-sizing")
	}
	if report.Headroom != 1 || (report.CPUWindow != "10m" && report.CPUWindow != "2m") {
		return report, fmt.Errorf("input requires headroom=1 and cpuWindow=10m or 2m")
	}
	if !finiteNonnegative(report.ChangeThreshold) || report.ChangeThreshold > 1 {
		return report, fmt.Errorf("input changeThreshold must be finite and between 0 and 1")
	}
	if report.Start.IsZero() || report.End.IsZero() || report.Start.After(report.End) {
		return report, fmt.Errorf("input requires a valid start/end time range")
	}
	if savings := report.Savings; savings != nil {
		if savings.Time.IsZero() || savings.Time.Before(report.Start) || savings.Time.After(report.End) {
			return report, fmt.Errorf("savings.time must be within the report start/end time range")
		}
		for i, r := range savings.Resources {
			if strings.TrimSpace(r.Cluster) == "" || (r.Resource != "cpu" && r.Resource != "memory") {
				return report, fmt.Errorf("savings.resources[%d]: cluster is required and resource must be cpu or memory", i)
			}
			for _, value := range []float64{r.Before, r.After, r.Reductions, r.Increases} {
				if !finiteNonnegative(value) {
					return report, fmt.Errorf("savings.resources[%d]: totals must be finite and nonnegative", i)
				}
			}
			if r.ChangedContainers < 0 || r.ExcludedContainers < 0 || r.UnknownContainers < 0 {
				return report, fmt.Errorf("savings.resources[%d]: container counts must be nonnegative", i)
			}
		}
	}
	for i, r := range report.Recommendations {
		invalid := func(reason string) (inputReport, error) {
			return report, fmt.Errorf("recommendations[%d]: %s", i, reason)
		}
		if strings.TrimSpace(r.Cluster) == "" || strings.TrimSpace(r.Namespace) == "" || strings.TrimSpace(r.Container) == "" {
			return invalid("cluster, namespace and container are required")
		}
		if r.Resource != "cpu" && r.Resource != "memory" {
			return invalid("resource must be cpu or memory")
		}
		if r.Replicas < 0 || r.MeasuredReplicas < 0 || r.MeasuredReplicas > r.Replicas {
			return invalid("invalid replica counts")
		}
		if r.Samples != nil && *r.Samples < 0 {
			return invalid("samples must be nonnegative")
		}
		for _, value := range []*float64{r.Peak, r.BurstPeak, r.RequestMin, r.RequestMax, r.Suggested} {
			if value != nil && !finiteNonnegative(*value) {
				return invalid("measurements and suggestions must be finite and nonnegative")
			}
		}
		// Delta is signed; it is informational rather than an edit decision.
		if r.Delta != nil && (math.IsNaN(*r.Delta) || math.IsInf(*r.Delta, 0)) {
			return invalid("delta must be finite")
		}
		if r.RequestMin != nil && r.RequestMax != nil && *r.RequestMin > *r.RequestMax {
			return invalid("requestMin exceeds requestMax")
		}
		if r.Suggested == nil {
			if r.SuggestedQuantity != "" {
				return invalid("suggestedQuantity requires suggested")
			}
			continue
		}
		suffix, scale := "m", 1000.0
		if r.Resource == "memory" {
			suffix, scale = "Mi", 1.0/(1<<20)
		}
		digits, found := strings.CutSuffix(r.SuggestedQuantity, suffix)
		if !found || len(digits) < 2 || digits[0] < '1' || digits[0] > '9' || digits[len(digits)-1] != '0' {
			return invalid("suggestedQuantity must be a canonical positive whole multiple of 10 with suffix " + suffix)
		}
		for _, digit := range digits {
			if digit < '0' || digit > '9' {
				return invalid("suggestedQuantity must contain only decimal digits before suffix " + suffix)
			}
		}
		if r.SuggestedQuantity != fmt.Sprintf("%.0f%s", *r.Suggested*scale, suffix) {
			return invalid("suggestedQuantity must match the canonical formatted suggestion")
		}
		quantity, err := inputParser(r.Resource)(r.SuggestedQuantity)
		if err != nil || !finiteNonnegative(quantity) || !inputEqual(quantity, *r.Suggested) {
			return invalid("suggested and suggestedQuantity disagree")
		}
		if r.Peak != nil {
			// Report values are already sized. Validate once, but never reapply
			// rounding or Grafana's different memory policy when writing.
			unit := 0.01
			if r.Resource == "memory" {
				unit = 10 * (1 << 20)
			}
			want := math.Max(1, math.Ceil(*r.Peak/unit)) * unit
			if !finiteNonnegative(want) || !inputEqual(want, *r.Suggested) {
				return invalid("suggested must equal peak rounded up to 10m/10Mi (minimum 10m/10Mi)")
			}
		}
	}
	return report, nil
}

func finiteNonnegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func inputEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func inputParser(resource string) parseFn {
	if resource == "cpu" {
		return ParseCPUCores
	}
	return ParseMemoryBytes
}

// RunInput consumes a validated offline report without credentials, queries,
// rendering or commits. Only dev request leaves can be passed to the editor.
func RunInput(ctx context.Context, log logr.Logger, path string, opts Options) error {
	return RunInputs(ctx, log, []string{path}, opts)
}

// RunInputs evaluates the complete dataset before making any configuration edits.
func RunInputs(ctx context.Context, log logr.Logger, paths []string, opts Options) error {
	if !finiteNonnegative(opts.ChangeThreshold) || opts.ChangeThreshold > 1 {
		return fmt.Errorf("change threshold must be finite and between 0 and 1")
	}
	if opts.Commit || opts.RenderCmd != "" || opts.GrafanaURL != "" {
		return fmt.Errorf("input mode cannot query Grafana, render or commit")
	}
	if (opts.SourcePrefix != "" && opts.SourcePrefix != "defaults") ||
		(opts.WritePrefix != "" && opts.WritePrefix != "clouds.dev.defaults") {
		return fmt.Errorf("input mode requires source-prefix=defaults and write-prefix=clouds.dev.defaults")
	}
	r, err := readDataset(paths)
	if err != nil {
		return err
	}
	source, err := editor.New(opts.ConfigPath)
	if err != nil {
		return err
	}
	if opts.WritePath == "" {
		opts.WritePath = opts.ConfigPath
	}
	target := source
	if opts.WritePath != opts.ConfigPath {
		target, err = editor.New(opts.WritePath)
		if err != nil {
			return err
		}
	}
	fmt.Printf("\nOffline right-sizing dataset (mode=%s, scope=clouds.dev.defaults requests, change-threshold=%g, reports=%d, CPU=%s)\n", mode(opts.DryRun), opts.ChangeThreshold, len(r.Reports), r.CPUWindow)
	for _, report := range r.Reports {
		fmt.Printf("SOURCE %s sha256=%s window=%s..%s\n", report.Path, report.SHA256, report.Report.Start.Format(time.RFC3339), report.Report.End.Format(time.RFC3339))
	}
	for _, warning := range r.Warnings {
		fmt.Printf("WARNING: %s\n", warning)
	}
	groups := map[string][]inputRecommendation{}
	targets := map[string]Target{}
	unmapped := 0
	for _, row := range r.Recommendations {
		mapping, ok := Resolve(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer)
		mappings := []Target{mapping}
		if !ok {
			workload := row.Workload
			if row.Kind == "Pod" {
				workload = "" // A bare Pod name is not an identified controller workload.
			}
			mappings = CandidateTargets(row.Namespace, workload, row.Container, row.Cluster, row.InitContainer)
		}
		if len(mappings) == 0 {
			unmapped++
			log.V(1).Info("skipped unknown mapping", "cluster", row.Cluster, "namespace", row.Namespace, "container", row.Container, "resource", row.Resource, "warnings", row.Warnings)
			continue
		}
		for _, warning := range row.Warnings {
			fmt.Printf("WARNING: %s/%s/%s %s: %s\n", row.Cluster, row.Namespace, row.Container, row.Resource, warning)
		}
		for _, mapping := range mappings {
			key := mapping.requestPath("clouds.dev.defaults", row.Resource)
			groups[key] = append(groups[key], row)
			targets[key] = mapping
		}
	}
	if unmapped > 0 {
		fmt.Printf("SKIP unknown mappings: %d recommendation rows (details at verbosity 1)\n", unmapped)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var updates []editor.Update
	for _, key := range keys {
		rows := groups[key]
		var best *inputRecommendation
		peak := 0.0
		blocked := false
		for i := range rows {
			row := &rows[i]
			mapping, resolved := Resolve(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer)
			if !row.Eligible || !resolved || mapping != targets[key] || row.Replicas == 0 ||
				row.MeasuredReplicas != row.Replicas || row.Peak == nil || row.Suggested == nil ||
				row.RequestMin == nil || row.RequestMax == nil {
				fmt.Printf("SKIP blocked %s: %s/%s/%s has ineligible, missing measurement or unresolved identity evidence (source %s)\n", key, row.Cluster, row.Kind, row.Workload, row.source)
				blocked = true
				continue
			}
			if best == nil || *row.Suggested > *best.Suggested {
				best = row
			}
			peak = math.Max(peak, *row.Peak)
		}
		if blocked || best == nil {
			continue
		}
		// Read the effective dev override before global defaults, including a
		// base-file dev override when writing a separate sparse overlay.
		current, _, err := target.Get(key)
		if errors.Is(err, editor.ErrPathNotFound) {
			current, _, err = source.Get(key)
		}
		if errors.Is(err, editor.ErrPathNotFound) {
			current, _, err = source.Get(targets[key].requestPath("defaults", best.Resource))
		}
		if err != nil && !errors.Is(err, editor.ErrPathNotFound) {
			fmt.Printf("WARNING request: SKIP %s: malformed effective current request: %v\n", key, err)
			continue
		}
		value, parseErr := inputParser(best.Resource)(current)
		if err != nil || parseErr != nil || !finiteNonnegative(value) {
			fmt.Printf("SKIP %s: effective current request is missing or nonnumeric\n", key)
			continue
		}
		if inputEqual(value, *best.Suggested) {
			fmt.Printf("NOOP %s: already %s\n", key, current)
			continue
		}
		if *best.Suggested < value {
			present := map[string]bool{}
			for _, row := range rows {
				present[row.source] = true
			}
			for _, report := range r.Reports {
				if !present[report.Path] {
					fmt.Printf("SKIP incomplete dataset %s: no %s evidence in %s; reductions require every report\n", key, best.Resource, report.Path)
					blocked = true
				}
			}
			if blocked {
				continue
			}
		}
		observed := false
		for _, row := range rows {
			// Union, not the envelope: gaps between cluster ranges are stale.
			if (value >= *row.RequestMin || inputEqual(value, *row.RequestMin)) &&
				(value <= *row.RequestMax || inputEqual(value, *row.RequestMax)) {
				observed = true
			}
		}
		if !observed {
			fmt.Printf("WARNING stale: SKIP %s: current %s is outside every observed request range\n", key, current)
			continue
		}
		// Producer actionability/risk flags use observed requests, not the
		// effective current config. Recompute after aggregating all clusters.
		alertRisk := peak > 1.2*value
		if !alertRisk && value > 0 && opts.ChangeThreshold > 0 {
			fraction := math.Abs(*best.Suggested-value) / value
			if fraction <= opts.ChangeThreshold || inputEqual(fraction, opts.ChangeThreshold) {
				fmt.Printf("SKIP deadband %s: %s -> %s changes by %g%% (threshold %g%%)\n", key, current, best.SuggestedQuantity, fraction*100, opts.ChangeThreshold*100)
				continue
			}
		}
		if *best.Suggested < value && !opts.AllowDecrease {
			fmt.Printf("SKIP %s: decrease %s -> %s requires --allow-decrease\n", key, current, best.SuggestedQuantity)
			continue
		}
		limitPath := targets[key].limitPath("clouds.dev.defaults", best.Resource)
		limit, _, limitErr := target.Get(limitPath)
		if errors.Is(limitErr, editor.ErrPathNotFound) {
			limit, _, limitErr = source.Get(limitPath)
		}
		if errors.Is(limitErr, editor.ErrPathNotFound) {
			limit, _, limitErr = source.Get(targets[key].limitPath("defaults", best.Resource))
		}
		if limitErr != nil && !errors.Is(limitErr, editor.ErrPathNotFound) {
			fmt.Printf("WARNING limit: SKIP %s: malformed effective limit: %v\n", key, limitErr)
			continue
		}
		limit = strings.TrimSpace(limit)
		if limitErr == nil && !IsSentinel(limit) {
			limitValue, err := inputParser(best.Resource)(limit)
			if err != nil || !finiteNonnegative(limitValue) {
				fmt.Printf("WARNING limit: SKIP %s: malformed effective limit %q\n", key, limit)
				continue
			}
			if limitValue > 0 && *best.Suggested > limitValue && !inputEqual(*best.Suggested, limitValue) {
				fmt.Printf("WARNING limit: SKIP %s: proposed request %s exceeds effective limit %s\n", key, best.SuggestedQuantity, limit)
				continue
			}
		}
		fmt.Printf("CHANGE %s: %s -> %s (measured peak alert risk=%t; governing source %s)\n", key, current, best.SuggestedQuantity, alertRisk, best.source)
		updates = append(updates, editor.Update{Path: key, NewValue: best.SuggestedQuantity})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.DryRun || len(updates) == 0 {
		return nil
	}
	if err := target.Upsert(updates); err != nil {
		return err
	}
	log.Info("applied offline dev request updates", "count", len(updates), "file", opts.WritePath)
	return nil
}
