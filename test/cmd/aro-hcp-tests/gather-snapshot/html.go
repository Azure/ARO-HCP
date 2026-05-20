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

package gathersnapshot

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

//go:embed artifacts
var artifactsFS embed.FS

// snapshotData pairs manifests with their verification reports. This is the
// raw input from which both the HTML overview and jUnit XML are rendered.
// It is serialized to snapshot-data.json in the artifact directory so that
// the rendering pipeline can be tested locally with real data.
type snapshotData struct {
	Manifests []*snapshot.Manifest           `json:"manifests"`
	Reports   []*snapshot.VerificationReport `json:"reports"`
}

// WriteSnapshotData serializes the raw snapshot inputs to a JSON file in the
// given directory. The resulting file can be used as test fixture data for
// unit-testing the HTML and jUnit rendering without a live Kusto connection.
func WriteSnapshotData(dir string, manifests []*snapshot.Manifest, reports []*snapshot.VerificationReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	data := snapshotData{
		Manifests: manifests,
		Reports:   reports,
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot data: %w", err)
	}
	outPath := filepath.Join(dir, "snapshot-data.json")
	if err := os.WriteFile(outPath, raw, 0o644); err != nil {
		return fmt.Errorf("failed to write snapshot data to %s: %w", outPath, err)
	}
	return nil
}

// htmlTreeData is the top-level data structure passed to the HTML template.
type htmlTreeData struct {
	TotalPassCount int
	TotalFailCount int
	TotalSkipCount int
	Sections       []htmlSection
}

// htmlSection represents a single test+resourceGroup grouping in the HTML overview.
type htmlSection struct {
	ResourceGroup string
	StartTime     string
	EndTime       string
	TestName      string
	PassCount     int
	FailCount     int
	SkipCount     int
	Statuses      string
	Nodes         []htmlNode
}

// htmlNode is a top-level grouping (resource or context).
type htmlNode struct {
	Name      string
	FailCount int
	Statuses  string
	Children  []htmlCategory
}

// htmlCategory groups queries by category within a node.
type htmlCategory struct {
	Name     string
	Statuses string
	Queries  []htmlQuery
}

// htmlQuery represents a single query in the tree.
type htmlQuery struct {
	Key        string
	Icon       string
	BadgeClass string
	BadgeText  string
	KQL        string
	Status     string
}

// htmlStatus constants are the CSS filter tokens used in data-has attributes.
// These must match the selectors in snapshot-overview.html.tmpl exactly.
const (
	htmlStatusPass = "pass"
	htmlStatusFail = "fail"
	htmlStatusSkip = "skip"
)

// allHTMLStatuses is the canonical ordering of status tokens, used by
// joinStatuses to produce deterministic data-has attribute values.
var allHTMLStatuses = []string{htmlStatusPass, htmlStatusFail, htmlStatusSkip}

// joinStatuses returns a deduplicated, space-separated string of status tokens.
func joinStatuses(statuses map[string]bool) string {
	var parts []string
	for _, s := range allHTMLStatuses {
		if statuses[s] {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// WriteHTMLOverview renders a single snapshot tree-viewer HTML page covering all
// manifests and reports, and writes it to the output directory.
func WriteHTMLOverview(dir string, manifests []*snapshot.Manifest, reports []*snapshot.VerificationReport) error {
	tmplBytes, err := artifactsFS.ReadFile("artifacts/snapshot-overview.html.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read HTML template: %w", err)
	}

	tmpl, err := template.New("overview").Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("failed to parse HTML template: %w", err)
	}

	data := buildHTMLData(manifests, reports)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to render HTML template: %w", err)
	}

	// filename must match the Spyglass HTML lens regex .*-summary.*\.html
	// so that Prow renders it inline in the job UI.
	outPath := filepath.Join(dir, "snapshot-summary.html")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write HTML overview: %w", err)
	}
	return nil
}

func buildHTMLData(manifests []*snapshot.Manifest, reports []*snapshot.VerificationReport) htmlTreeData {
	data := htmlTreeData{}

	for i, manifest := range manifests {
		if i >= len(reports) {
			break
		}
		report := reports[i]
		section := buildHTMLSection(manifest, report)
		data.TotalPassCount += section.PassCount
		data.TotalFailCount += section.FailCount
		data.TotalSkipCount += section.SkipCount
		data.Sections = append(data.Sections, section)
	}

	return data
}

func buildHTMLSection(manifest *snapshot.Manifest, report *snapshot.VerificationReport) htmlSection {
	section := htmlSection{
		ResourceGroup: manifest.ResourceGroup,
		StartTime:     manifest.TimeWindow.Start.Format(time.RFC3339),
		EndTime:       manifest.TimeWindow.End.Format(time.RFC3339),
		TestName:      manifest.TestName,
	}

	// Group verification cases by suite, then by category.
	type suiteData struct {
		categories map[string][]htmlQuery
		catOrder   []string
		failCount  int
	}
	suites := make(map[string]*suiteData)
	var suiteOrder []string

	for _, c := range report.Cases {
		sd, ok := suites[c.Suite]
		if !ok {
			sd = &suiteData{categories: make(map[string][]htmlQuery)}
			suites[c.Suite] = sd
			suiteOrder = append(suiteOrder, c.Suite)
		}

		q := htmlQuery{
			Key: c.Query,
			KQL: c.RenderedKQL,
		}

		switch c.Status {
		case snapshot.VerificationPass:
			q.Icon = "\u2713"
			q.BadgeClass = "badge-pass"
			q.BadgeText = "results"
			q.Status = htmlStatusPass
			section.PassCount++
		case snapshot.VerificationFail:
			q.Icon = "\u2717"
			q.BadgeClass = "badge-fail"
			q.BadgeText = "NO RESULTS"
			q.Status = htmlStatusFail
			section.FailCount++
			sd.failCount++
		case snapshot.VerificationSkipped:
			q.Icon = "\u2298"
			q.BadgeClass = "badge-skip"
			q.BadgeText = "skipped"
			q.Status = htmlStatusSkip
			section.SkipCount++
		}

		if _, exists := sd.categories[c.Category]; !exists {
			sd.catOrder = append(sd.catOrder, c.Category)
		}
		sd.categories[c.Category] = append(sd.categories[c.Category], q)
	}

	sectionStatuses := make(map[string]bool)
	for _, suiteName := range suiteOrder {
		sd := suites[suiteName]
		node := htmlNode{
			Name:      suiteName,
			FailCount: sd.failCount,
		}
		nodeStatuses := make(map[string]bool)
		for _, cat := range sd.catOrder {
			catStatuses := make(map[string]bool)
			queries := sd.categories[cat]
			for _, q := range queries {
				if q.Status != "" {
					catStatuses[q.Status] = true
				}
			}
			for s := range catStatuses {
				nodeStatuses[s] = true
			}
			node.Children = append(node.Children, htmlCategory{
				Name:     cat,
				Statuses: joinStatuses(catStatuses),
				Queries:  queries,
			})
		}
		for s := range nodeStatuses {
			sectionStatuses[s] = true
		}
		node.Statuses = joinStatuses(nodeStatuses)
		section.Nodes = append(section.Nodes, node)
	}
	section.Statuses = joinStatuses(sectionStatuses)

	return section
}
