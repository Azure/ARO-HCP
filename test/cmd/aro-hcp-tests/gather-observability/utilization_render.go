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
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Validate at the shared rendering boundary so live and offline reports have
// identical semantics. Missing measurements and empty snapshots are valid.
func renderUtilizationHTML(report utilizationReport) ([]byte, error) {
	if report.SchemaVersion != utilizationSchemaVersion {
		return nil, fmt.Errorf("unsupported utilization schemaVersion %d (expected %d)", report.SchemaVersion, utilizationSchemaVersion)
	}
	if report.GeneratedAt.IsZero() || report.Start.IsZero() || report.End.IsZero() || report.End.Before(report.Start) {
		return nil, fmt.Errorf("utilization generatedAt, start and end must be nonzero timestamps with start <= end")
	}
	resources := func(path string, values ...utilizationResources) error {
		for _, value := range values {
			for _, measurement := range []*float64{value.CPU, value.Memory} {
				if measurement != nil && (math.IsNaN(*measurement) || math.IsInf(*measurement, 0) || *measurement < 0) {
					return fmt.Errorf("%s: resource values must be finite and nonnegative", path)
				}
			}
		}
		return nil
	}
	clusters := map[string]bool{}
	for _, cluster := range report.Clusters {
		if strings.TrimSpace(cluster) == "" || clusters[cluster] {
			return nil, fmt.Errorf("clusters must contain unique nonempty names")
		}
		clusters[cluster] = true
	}
	first, last := report.Start.UTC().Truncate(time.Minute), report.End.UTC().Truncate(time.Minute)
	if first.Before(report.Start) {
		first = first.Add(time.Minute)
	}
	coverageKeys := map[[2]string]bool{}
	for i, coverage := range report.Coverage {
		key := [2]string{coverage.Scope, coverage.Resource}
		if (coverage.Scope != "overall" && !clusters[coverage.Scope]) || (coverage.Resource != "cpu" && coverage.Resource != "memory") || coverageKeys[key] {
			return nil, fmt.Errorf("coverage %d: scope/resource must be valid and unique", i)
		}
		coverageKeys[key] = true
		clusterCount := 1
		if coverage.Scope == "overall" {
			clusterCount = len(clusters)
		}
		if len(coverage.Intervals) == 0 || first.After(last) {
			return nil, fmt.Errorf("coverage %d: intervals must cover the evaluated minute grid and cannot be empty", i)
		}
		next := first
		for j, interval := range coverage.Intervals {
			path := fmt.Sprintf("coverage %d interval %d", i, j)
			for _, timestamp := range []time.Time{interval.Start, interval.End} {
				_, offset := timestamp.Zone()
				if timestamp.IsZero() || offset != 0 || !timestamp.Equal(timestamp.Truncate(time.Minute)) || timestamp.Before(first) || timestamp.After(last) {
					return nil, fmt.Errorf("%s: times must be UTC-minute samples within the report grid", path)
				}
			}
			if !interval.Start.Equal(next) || interval.End.Before(interval.Start) {
				return nil, fmt.Errorf("%s: intervals must be ordered, contiguous and cover the evaluated minute grid", path)
			}
			next = interval.End.Add(time.Minute)
			if interval.Nodes < 0 || interval.MissingClusters < 0 || interval.MissingClusters > clusterCount {
				return nil, fmt.Errorf("%s: node and missing cluster counts must be nonnegative and within scope", path)
			}
			for _, count := range []int{interval.MissingInventory, interval.MissingUsage, interval.MissingCapacity} {
				if count < 0 || count > interval.Nodes {
					return nil, fmt.Errorf("%s: missing node counts must be nonnegative and cannot exceed nodes", path)
				}
			}
			if interval.Eligible && (interval.Nodes == 0 || clusterCount == 0 || interval.MissingInventory != 0 || interval.MissingUsage != 0 || interval.MissingCapacity != 0 || interval.MissingClusters != 0) {
				return nil, fmt.Errorf("%s: eligible samples cannot have coverage gaps or zero nodes", path)
			}
		}
		if !next.Equal(last.Add(time.Minute)) {
			return nil, fmt.Errorf("coverage %d: intervals must cover the evaluated minute grid", i)
		}
	}
	for i, snapshot := range report.Snapshots {
		if snapshot.Time.IsZero() || snapshot.Time.Before(report.Start) || snapshot.Time.After(report.End) {
			return nil, fmt.Errorf("snapshot %d: time must be within the report start/end window", i)
		}
		nodes := map[[2]string]bool{}
		for j, node := range snapshot.Nodes {
			key := [2]string{node.Cluster, node.Name}
			if !clusters[node.Cluster] || strings.TrimSpace(node.Name) == "" {
				return nil, fmt.Errorf("snapshot %d node %d: named node must belong to a declared cluster", i, j)
			}
			if nodes[key] {
				return nil, fmt.Errorf("snapshot %d: duplicate node %s/%s", i, node.Cluster, node.Name)
			}
			nodes[key] = true
			if err := resources(fmt.Sprintf("snapshot %d node %d", i, j), node.Capacity, node.Allocatable, node.Usage); err != nil {
				return nil, err
			}
		}
		for j, workload := range snapshot.Workloads {
			path := fmt.Sprintf("snapshot %d workload %d", i, j)
			if !clusters[workload.Cluster] {
				return nil, fmt.Errorf("%s: workload must belong to a declared cluster", path)
			}
			if workload.Unscheduled && workload.Node != "" {
				return nil, fmt.Errorf("%s: unscheduled workload cannot have a node", path)
			}
			if workload.Pods < 0 || workload.PendingPods < 0 {
				return nil, fmt.Errorf("%s: pod counts must be nonnegative", path)
			}
			if workload.PendingPods > workload.Pods {
				return nil, fmt.Errorf("%s: pendingPods cannot exceed pods", path)
			}
			if err := resources(path, workload.Usage, workload.Requests, workload.Limits); err != nil {
				return nil, err
			}
			containers := map[string]bool{}
			for k, container := range workload.Containers {
				path := fmt.Sprintf("%s container %d", path, k)
				if strings.TrimSpace(container.Name) == "" || containers[container.Name] {
					return nil, fmt.Errorf("%s: container names must be nonempty and unique within a workload", path)
				}
				containers[container.Name] = true
				for _, count := range []*int{container.UnlimitedCPU, container.UnlimitedMemory} {
					if count != nil && (*count < 0 || *count > workload.Pods) {
						return nil, fmt.Errorf("%s: unlimited counts must be nonnegative and cannot exceed pods", path)
					}
				}
				if err := resources(path, container.Usage, container.Requests, container.Limits); err != nil {
					return nil, err
				}
			}
		}
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal utilization report: %w", err)
	}
	tmpl, err := template.New("utilization").Parse(string(mustReadArtifact("utilization.html.tmpl")))
	if err != nil {
		return nil, fmt.Errorf("parse utilization template: %w", err)
	}
	var buf bytes.Buffer
	// Only json.Marshal output is trusted here: its default HTML escaping keeps
	// embedded strings (including </script>) inside the single JSON data block.
	if err := tmpl.Execute(&buf, template.JS(data)); err != nil { //nolint:gosec // HTML-escaped JSON, not executable user input
		return nil, fmt.Errorf("render utilization template: %w", err)
	}
	return buf.Bytes(), nil
}

func newRenderUtilizationCommand() *cobra.Command {
	var input, output string
	cmd := &cobra.Command{
		Use:   "render-utilization --input utilization.json --output dir",
		Short: "Render a saved utilization report without Azure configuration or credentials.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input) == "" || strings.TrimSpace(output) == "" {
				return fmt.Errorf("--input and --output are required")
			}
			file, err := os.Open(input)
			if err != nil {
				return fmt.Errorf("open utilization input: %w", err)
			}
			defer file.Close()
			decoder := json.NewDecoder(file)
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return fmt.Errorf("decode utilization input: %w", err)
			}
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				return fmt.Errorf("utilization input must contain exactly one JSON report (trailing data)")
			}
			decoder = json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			var report utilizationReport
			if err := decoder.Decode(&report); err != nil {
				return fmt.Errorf("decode utilization input: %w", err)
			}
			// Go scalar counters otherwise silently accept missing/null as zero.
			// RawMessage distinguishes a missing nullable count from explicit null.
			var presence struct {
				Snapshots []struct {
					Workloads []struct {
						Pods        *int
						PendingPods *int
						Containers  []struct {
							UnlimitedCPU    json.RawMessage
							UnlimitedMemory json.RawMessage
						}
					}
				}
			}
			if err := json.Unmarshal(raw, &presence); err != nil {
				return fmt.Errorf("decode utilization counters: %w", err)
			}
			for i, snapshot := range presence.Snapshots {
				for j, workload := range snapshot.Workloads {
					if workload.Pods == nil || workload.PendingPods == nil {
						return fmt.Errorf("snapshot %d workload %d: pods and pendingPods are required and cannot be null", i, j)
					}
					for k, container := range workload.Containers {
						if len(container.UnlimitedCPU) == 0 || len(container.UnlimitedMemory) == 0 {
							return fmt.Errorf("snapshot %d workload %d container %d: unlimitedCPU and unlimitedMemory are required (null means unknown)", i, j, k)
						}
					}
				}
			}
			html, err := renderUtilizationHTML(report)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(output, 0755); err != nil {
				return fmt.Errorf("create utilization output directory: %w", err)
			}
			if err := os.WriteFile(filepath.Join(output, "utilization-summary.html"), html, 0644); err != nil {
				return fmt.Errorf("write utilization HTML: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Saved utilization JSON report.")
	cmd.Flags().StringVar(&output, "output", "", "Directory for utilization-summary.html.")
	return cmd
}
