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
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
)

// RunSizingInput applies an offline report only to existing e2e_minimal requests
// in the limitClusterSizes=true template branch. The explicit namespace prefix
// is a user assertion of a minimal sample, not evidence of its size class.
func RunSizingInput(ctx context.Context, log logr.Logger, inputPath, templatePath, namespacePrefix string, opts Options) error {
	if !regexp.MustCompile(`^ocm-[a-z0-9][a-z0-9-]*-$`).MatchString(namespacePrefix) {
		return fmt.Errorf("namespace prefix must match ^ocm-[a-z0-9][a-z0-9-]*-$ (for example ocm-arohcpci01-); it is a literal prefix, not a regular expression")
	}
	if !finiteNonnegative(opts.ChangeThreshold) || opts.ChangeThreshold > 1 {
		return fmt.Errorf("change threshold must be finite and between 0 and 1")
	}
	if opts.Commit || opts.RenderCmd != "" || opts.GrafanaURL != "" ||
		(opts.SourcePrefix != "" && opts.SourcePrefix != "defaults") || opts.WritePath != "" || opts.WritePrefix != "" {
		return fmt.Errorf("sizing input mode cannot query Grafana, render, commit or edit normal config paths/prefixes")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	report, err := readInput(inputPath)
	if err != nil {
		return err
	}
	target, err := editor.NewSizing(templatePath)
	if err != nil {
		return err
	}
	fmt.Printf("\nOffline sizing report (mode=%s, scope=limitClusterSizes=true/e2e_minimal requests, namespace-prefix=%s, change-threshold=%g, report-threshold=%g)\n", mode(opts.DryRun), namespacePrefix, opts.ChangeThreshold, report.ChangeThreshold)
	fmt.Println("WARNING: The namespace prefix is a user assertion only: the report does not prove size class. Use only an e2e_minimal sample from the selected environment.")
	fmt.Println("WARNING: Pod limits were not checked: the report and sizing source contain requests only. No limits will be changed; verify pod limits before applying these requests.")
	for _, warning := range report.Warnings {
		fmt.Printf("WARNING: %s\n", warning)
	}
	entries := target.Entries()
	kinds := make([]string, len(entries))
	groups := make([]map[string][]inputRecommendation, len(entries))
	for i, entry := range entries {
		// Only these exact workload/container pairs have known controller kinds.
		// The editor's selected entries remain authoritative for what can be edited.
		if entry.Deployment != entry.Container {
			continue
		}
		switch entry.Deployment {
		case "kube-apiserver", "openshift-controller-manager", "cluster-policy-controller", "kube-controller-manager", "openshift-apiserver", "ovnkube-control-plane":
			kinds[i] = "Deployment"
		case "etcd":
			kinds[i] = "StatefulSet"
		default:
			continue
		}
		groups[i] = map[string][]inputRecommendation{}
	}
	unmapped := 0
	for _, row := range report.Recommendations {
		if !strings.HasPrefix(row.Namespace, namespacePrefix) || len(row.Namespace) == len(namespacePrefix) {
			continue
		}
		resolved := false
		switch row.Kind {
		case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob":
			resolved = strings.TrimSpace(row.Workload) != "" && !strings.EqualFold(row.Workload, "unknown")
		}
		matched := false
		for i, entry := range entries {
			if kinds[i] == "" || row.Container != entry.Container || (resolved && row.Workload != entry.Deployment) {
				continue
			}
			// An unresolved Pod/owner could belong to any entry with this container.
			// A resolved, different workload cannot, even if its container is reused.
			groups[i][row.Resource] = append(groups[i][row.Resource], row)
			matched = true
		}
		if !matched {
			unmapped++
			log.V(1).Info("skipped unknown sizing mapping", "cluster", row.Cluster, "namespace", row.Namespace, "kind", row.Kind, "workload", row.Workload, "container", row.Container, "resource", row.Resource, "warnings", row.Warnings)
			continue
		}
		for _, warning := range row.Warnings {
			log.V(1).Info("sizing evidence warning", "cluster", row.Cluster, "namespace", row.Namespace, "container", row.Container, "resource", row.Resource, "warning", warning)
		}
	}
	if unmapped > 0 {
		fmt.Printf("SKIP unknown sizing mappings: %d recommendation rows (details at verbosity 1)\n", unmapped)
	}
	var updates []editor.SizingUpdate
	for i, entry := range entries {
		for _, resource := range []string{"cpu", "memory"} {
			rows := groups[i][resource]
			key := entry.Deployment + "/" + entry.Container + "/" + resource
			var best *inputRecommendation
			peak := 0.0
			blocked := 0
			for j := range rows {
				row := &rows[j]
				if row.Kind != kinds[i] || row.Workload != entry.Deployment || !row.Eligible || row.InitContainer ||
					row.Replicas == 0 || row.MeasuredReplicas != row.Replicas || row.Peak == nil || row.Suggested == nil ||
					row.RequestMin == nil || row.RequestMax == nil {
					log.V(1).Info("blocked sizing evidence", "target", key, "cluster", row.Cluster, "namespace", row.Namespace, "kind", row.Kind, "workload", row.Workload)
					blocked++
					continue
				}
				if best == nil || *row.Suggested > *best.Suggested {
					best = row
				}
				peak = math.Max(peak, *row.Peak)
			}
			if blocked > 0 {
				fmt.Printf("SKIP blocked %s: %d of %d rows have ineligible, missing measurement, init-container or unknown/wrong owner evidence (details at verbosity 1)\n", key, blocked, len(rows))
				continue
			}
			if best == nil {
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
			observed := false
			for _, row := range rows {
				// Union, not the envelope: gaps between HCP request ranges are stale.
				if (value >= *row.RequestMin || inputEqual(value, *row.RequestMin)) &&
					(value <= *row.RequestMax || inputEqual(value, *row.RequestMax)) {
					observed = true
				}
			}
			if !observed {
				fmt.Printf("WARNING stale: SKIP %s: current %s is outside every observed request range\n", key, current)
				continue
			}
			// Producer flags describe observed requests, not this template scalar.
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
			fmt.Printf("CHANGE %s: %s -> %s (measured peak alert risk=%t)\n", key, current, best.SuggestedQuantity, alertRisk)
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
	log.Info("applied offline e2e_minimal request updates", "count", len(updates), "file", templatePath)
	return nil
}
