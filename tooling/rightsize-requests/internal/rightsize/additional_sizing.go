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
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

// RunAdditionalSizingInputs sizes existing additional minimal-HCP config knobs.
// The audited catalog is resolved relative to the explicit config file, never
// the working directory. No report can introduce an unconfigured target.
func RunAdditionalSizingInputs(ctx context.Context, log logr.Logger, inputPaths []string, configPath, namespacePrefix string, opts Options) error {
	if !regexp.MustCompile(`^ocm-[a-z0-9][a-z0-9-]*-$`).MatchString(namespacePrefix) {
		return fmt.Errorf("namespace prefix must match ^ocm-[a-z0-9][a-z0-9-]*-$; it is a literal prefix")
	}
	if !finiteNonnegative(opts.ChangeThreshold) || opts.ChangeThreshold > 1 {
		return fmt.Errorf("change threshold must be finite and between 0 and 1")
	}
	if opts.Commit || opts.RenderCmd != "" || opts.GrafanaURL != "" ||
		(opts.SourcePrefix != "" && opts.SourcePrefix != "defaults") || opts.WritePath != "" || opts.WritePrefix != "" {
		return fmt.Errorf("additional sizing input mode cannot query Grafana, render, commit or edit other config paths/prefixes")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	report, err := readDataset(inputPaths)
	if err != nil {
		return err
	}
	target, err := editor.NewAdditionalSizing(configPath)
	if err != nil {
		return err
	}
	catalogPath := filepath.Join(filepath.Dir(filepath.Dir(configPath)), "hypershiftoperator", "deploy", "regular-resource-targets.yaml")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return fmt.Errorf("read regular HCP targets: %w", err)
	}
	var catalog map[string][]string
	d := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := d.Decode(&catalog); err != nil {
		return fmt.Errorf("decode regular HCP targets: %w", err)
	}
	var extra yaml.Node
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one regular HCP targets document: %v", err)
	}
	known := map[[2]string]bool{}
	for workload, containers := range catalog {
		for _, container := range containers {
			key := [2]string{workload, container}
			if workload == "" || container == "" || known[key] {
				return fmt.Errorf("invalid or duplicate regular HCP target %v", key)
			}
			known[key] = true
		}
	}
	for _, minimal := range targets.MinimalTargets() {
		delete(known, [2]string{minimal.Workload, minimal.Container})
	}
	entries := target.Entries()
	for _, entry := range entries {
		if !known[[2]string{entry.Deployment, entry.Container}] {
			return fmt.Errorf("%s: unsupported additional regular HCP target %s/%s (original seven are excluded)", entry.ID, entry.Deployment, entry.Container)
		}
	}
	fmt.Printf("\nOffline additional HCP sizing dataset (mode=%s, scope=clouds.dev.defaults.hypershift.additionalMinimalResourceRequests, namespace-prefix=%s, change-threshold=%g, reports=%d, CPU=%s)\n", mode(opts.DryRun), namespacePrefix, opts.ChangeThreshold, len(report.Reports), report.CPUWindow)
	for _, source := range report.Reports {
		fmt.Printf("SOURCE %s sha256=%s window=%s..%s\n", source.Path, source.SHA256, source.Report.Start.Format(time.RFC3339), source.Report.End.Format(time.RFC3339))
	}
	fmt.Println("WARNING: The namespace prefix is a user assertion only: the report does not prove size class. Use only an e2e_minimal sample from the selected environment.")
	fmt.Println("WARNING: Pod limits were not checked. No limits will be changed; verify pod limits before applying these requests.")
	for _, warning := range report.Warnings {
		fmt.Printf("WARNING: %s\n", warning)
	}
	if len(entries) == 0 {
		fmt.Println("INFO: no configured additional HCP targets; supply baseline identities and quantities in config before sizing. No entries will be inserted.")
	}
	groups := make([]map[string][]inputRecommendation, len(entries))
	for i := range entries {
		groups[i] = map[string][]inputRecommendation{}
	}
	unmapped := 0
	for _, row := range report.Recommendations {
		if !strings.HasPrefix(row.Namespace, namespacePrefix) || row.Namespace == namespacePrefix {
			continue
		}
		resolved := false
		switch row.Kind {
		case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
			resolved = strings.TrimSpace(row.Workload) != "" && !strings.EqualFold(row.Workload, "unknown")
		}
		matched := false
		for i, entry := range entries {
			// Unknown owners block every candidate with the same container. A
			// known different workload is not evidence for this target.
			if row.Container == entry.Container && (!resolved || row.Workload == entry.Deployment) {
				groups[i][row.Resource] = append(groups[i][row.Resource], row)
				matched = true
			}
		}
		if !matched {
			unmapped++
		}
		for _, warning := range row.Warnings {
			log.V(1).Info("additional sizing evidence warning", "source", row.source, "namespace", row.Namespace, "container", row.Container, "warning", warning)
		}
	}
	if unmapped > 0 {
		fmt.Printf("SKIP unknown additional sizing mappings: %d recommendation rows\n", unmapped)
	}
	var updates []editor.SizingUpdate
	for i, entry := range entries {
		kind := "Deployment"
		if entry.Deployment == "etcd" {
			kind = "StatefulSet"
		}
		for _, resource := range []string{"cpu", "memory"} {
			key := entry.ID + "/" + resource
			rows := groups[i][resource]
			var best *inputRecommendation
			peak, blocked := 0.0, false
			for j := range rows {
				row := &rows[j]
				if row.Kind != kind || row.Workload != entry.Deployment || !row.Eligible || row.InitContainer ||
					row.Replicas == 0 || row.MeasuredReplicas != row.Replicas || row.Peak == nil || row.Suggested == nil || row.RequestMin == nil || row.RequestMax == nil {
					fmt.Printf("SKIP blocked %s: ineligible, missing measurement, init-container or unknown/wrong owner evidence in %s/%s (source %s)\n", key, row.Cluster, row.Namespace, row.source)
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
			current := entry.CPU
			if resource == "memory" {
				current = entry.Memory
			}
			value, err := inputParser(resource)(current)
			if err != nil || !finiteNonnegative(value) {
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
				for _, source := range report.Reports {
					if !present[source.Path] {
						fmt.Printf("SKIP incomplete dataset %s: reductions require evidence in every report; missing %s\n", key, source.Path)
						blocked = true
					}
				}
				if blocked {
					continue
				}
			}
			observed := false
			for _, row := range rows {
				if (value >= *row.RequestMin || inputEqual(value, *row.RequestMin)) && (value <= *row.RequestMax || inputEqual(value, *row.RequestMax)) {
					observed = true
				}
			}
			if !observed {
				fmt.Printf("WARNING stale: SKIP %s: current %s is outside every observed request range\n", key, current)
				continue
			}
			alertRisk := peak > 1.2*value
			if !alertRisk && value > 0 && opts.ChangeThreshold > 0 {
				fraction := math.Abs(*best.Suggested-value) / value
				if fraction <= opts.ChangeThreshold || inputEqual(fraction, opts.ChangeThreshold) {
					fmt.Printf("SKIP deadband %s: %s -> %s\n", key, current, best.SuggestedQuantity)
					continue
				}
			}
			if *best.Suggested < value && !opts.AllowDecrease {
				fmt.Printf("SKIP %s: decrease %s -> %s requires --allow-decrease\n", key, current, best.SuggestedQuantity)
				continue
			}
			fmt.Printf("CHANGE %s: %s -> %s (measured peak alert risk=%t; governing source %s)\n", key, current, best.SuggestedQuantity, alertRisk, best.source)
			updates = append(updates, editor.SizingUpdate{Deployment: entry.Deployment, Container: entry.Container, Resource: resource, NewValue: best.SuggestedQuantity})
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.DryRun || len(updates) == 0 {
		return nil
	}
	if err := target.Apply(updates); err != nil {
		return err
	}
	log.Info("applied offline additional minimal HCP request updates", "count", len(updates), "file", configPath)
	return nil
}
