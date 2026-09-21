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
	"math"
	"strings"
	"time"
)

// validateUtilizationHistory can also be called by the parent utilization
// renderer. History is additive in schema v1; absent history remains valid.
func validateUtilizationHistory(report utilizationReport) error {
	if report.SchemaVersion != utilizationSchemaVersion {
		return fmt.Errorf("unsupported utilization schemaVersion %d (expected %d)", report.SchemaVersion, utilizationSchemaVersion)
	}
	if report.GeneratedAt.IsZero() || report.Start.IsZero() || report.End.IsZero() || report.End.Before(report.Start) {
		return fmt.Errorf("utilization generatedAt, start and end must be nonzero timestamps with start <= end")
	}
	clusters := map[string]bool{}
	for _, cluster := range report.Clusters {
		if strings.TrimSpace(cluster) == "" || clusters[cluster] {
			return fmt.Errorf("clusters must contain unique nonempty names")
		}
		clusters[cluster] = true
	}
	if len(report.History) == 0 {
		return nil
	}
	step, err := time.ParseDuration(report.Step)
	if err != nil || step != time.Minute {
		return fmt.Errorf("history step must be one minute")
	}
	first, last := report.Start.UTC().Truncate(time.Minute), report.End.UTC().Truncate(time.Minute)
	if first.Before(report.Start) {
		first = first.Add(time.Minute)
	}
	next := first
	for i, sample := range report.History {
		_, offset := sample.Time.Zone()
		if sample.Time.IsZero() || offset != 0 || !sample.Time.Equal(sample.Time.Truncate(time.Minute)) || sample.Time.Before(first) || sample.Time.After(last) {
			return fmt.Errorf("history %d: time must be a UTC-minute sample within the report grid", i)
		}
		if !sample.Time.Equal(next) {
			return fmt.Errorf("history %d: samples must be ordered, contiguous and cover the evaluated minute grid", i)
		}
		next = next.Add(time.Minute)
		expected := map[string]bool{}
		for _, cluster := range sample.Expected {
			if !clusters[cluster] || expected[cluster] {
				return fmt.Errorf("history %d: expected clusters must be declared and unique", i)
			}
			expected[cluster] = true
		}
		nodes := map[[2]string]bool{}
		for j, node := range sample.Nodes {
			path := fmt.Sprintf("history %d node %d", i, j)
			key := [2]string{node.Cluster, node.Name}
			if !clusters[node.Cluster] || strings.TrimSpace(node.Name) == "" || nodes[key] {
				return fmt.Errorf("%s: nodes must have unique nonempty identities in declared clusters", path)
			}
			nodes[key] = true
			for _, value := range []utilizationHistoryResources{node.Capacity, node.Allocatable, node.Usage, node.Requests} {
				for _, measurement := range []*float64{value.CPU, value.Memory, value.SwiftNIC} {
					if measurement != nil && (math.IsNaN(*measurement) || math.IsInf(*measurement, 0) || *measurement < 0) {
						return fmt.Errorf("%s: resource values must be finite and nonnegative", path)
					}
				}
			}
			if node.Usage.SwiftNIC != nil {
				return fmt.Errorf("%s: SWIFT-NIC has no usage measurement", path)
			}
		}
	}
	if !next.Equal(last.Add(time.Minute)) {
		return fmt.Errorf("history samples must cover the evaluated minute grid")
	}
	return nil
}

func renderResourceHistoryHTML(report utilizationReport) ([]byte, error) {
	if err := validateUtilizationHistory(report); err != nil {
		return nil, err
	}
	// Peak workload detail and peak-ranking coverage belong to the other tab.
	report.Snapshots = nil
	report.Coverage = nil
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal resource history: %w", err)
	}
	tmpl, err := template.New("resourcehistory").Parse(string(mustReadArtifact("resourcehistory.html.tmpl")))
	if err != nil {
		return nil, fmt.Errorf("parse resource history template: %w", err)
	}
	var buf bytes.Buffer
	// json.Marshal's default HTML escaping prevents embedded strings from
	// closing the data script. All dynamic UI text uses textContent.
	if err := tmpl.Execute(&buf, template.JS(data)); err != nil { //nolint:gosec // HTML-escaped JSON, not executable user input
		return nil, fmt.Errorf("render resource history template: %w", err)
	}
	return buf.Bytes(), nil
}
