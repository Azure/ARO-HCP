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
	data, err := marshalResourceHistoryHTML(report)
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

// These intern tables are private to the HTML, not the utilization.json replay
// contract. Each minute retains its own metadata (including membership and
// advertisement evidence) and exact nullable measurements.
type historyHTMLMetadata struct {
	Cluster         string `json:"cluster"`
	Name            string `json:"name"`
	Pool            string `json:"pool"`
	SKU             string `json:"sku"`
	Inventory       bool   `json:"inventory"`
	SwiftAdvertised *bool  `json:"swiftAdvertised"`
}

type historyHTMLSample struct {
	Time     time.Time `json:"time"`
	Expected []string  `json:"expected"`
	// Pairs index metadata and quantities, respectively. Quantities retain the
	// capacity, allocatable, usage, requests order used by the renderer.
	Nodes    [][2]int `json:"nodes"`
	Warnings []int    `json:"warnings,omitempty"`
}

func marshalResourceHistoryHTML(report utilizationReport) ([]byte, error) {
	metadata := historyInternTable[historyHTMLMetadata]{}
	quantities := historyInternTable[[4]utilizationHistoryResources]{}
	warnings := historyInternTable[string]{}
	history := make([]historyHTMLSample, len(report.History))
	for i, sample := range report.History {
		minute := historyHTMLSample{Time: sample.Time, Expected: sample.Expected}
		if sample.Nodes != nil {
			minute.Nodes = make([][2]int, 0, len(sample.Nodes))
		}
		for _, node := range sample.Nodes {
			m, err := metadata.intern(historyHTMLMetadata{node.Cluster, node.Name, node.Pool, node.SKU, node.Inventory, node.SwiftAdvertised})
			if err != nil {
				return nil, err
			}
			q, err := quantities.intern([4]utilizationHistoryResources{node.Capacity, node.Allocatable, node.Usage, node.Requests})
			if err != nil {
				return nil, err
			}
			minute.Nodes = append(minute.Nodes, [2]int{m, q})
		}
		for _, warning := range sample.Warnings {
			w, err := warnings.intern(warning)
			if err != nil {
				return nil, err
			}
			minute.Warnings = append(minute.Warnings, w)
		}
		history[i] = minute
	}
	return json.Marshal(struct {
		utilizationReport
		History    []historyHTMLSample `json:"history"`
		Metadata   []json.RawMessage   `json:"metadata"`
		Quantities []json.RawMessage   `json:"quantities"`
		Messages   []json.RawMessage   `json:"messages"`
	}{report, history, metadata.values, quantities.values, warnings.values})
}

type historyInternTable[T any] struct {
	indices map[string]int
	values  []json.RawMessage
}

func (table *historyInternTable[T]) intern(value T) (int, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	if index, ok := table.indices[string(data)]; ok {
		return index, nil
	}
	if table.indices == nil {
		table.indices = map[string]int{}
	}
	index := len(table.values)
	table.indices[string(data)] = index
	table.values = append(table.values, data)
	return index, nil
}
