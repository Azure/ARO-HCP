// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"sort"
	"time"

	_ "embed"
)

//go:embed viewer.html
var viewerTemplate string

// DayRun summarizes one run's snapshot for the day index.
type DayRun struct {
	Report    string    `json:"report"`    // Report data file name, relative to the day index.
	URL       string    `json:"url"`       // Prow job URL.
	Job       string    `json:"job"`       // Prow job name.
	PR        string    `json:"pr"`        // Pull request number; empty for periodic and postsubmit builds.
	BuildID   string    `json:"buildID"`   // Prow build ID.
	Result    string    `json:"result"`    // Prow result, e.g. SUCCESS or FAILURE.
	StartedAt time.Time `json:"startedAt"` // Job start.
	InfraUSD  float64   `json:"infraUSD"`  // Net cost of infrastructure groups.
	TestsUSD  float64   `json:"testsUSD"`  // Net cost of test groups.
	NetUSD    float64   `json:"netUSD"`    // Net cost of the run.
	Complete  bool      `json:"complete"`  // Every group's billing was retrieved without errors.
}

// DayIndex is a day's run list for the site viewer.
type DayIndex struct {
	Version int        `json:"version"`
	Day     string     `json:"day"`
	Summary DaySummary `json:"summary"`
	Runs    []DayRun   `json:"runs"` // Most expensive first.
}

// DaySummary is one day's entry in the site manifest.
type DaySummary struct {
	Day        string  `json:"day"` // UTC day, YYYY-MM-DD.
	Runs       int     `json:"runs"`
	Incomplete int     `json:"incomplete"`
	NetUSD     float64 `json:"netUSD"`
}

// SiteManifest lists published days so the site index can be rebuilt without
// listing the whole bucket.
type SiteManifest struct {
	Version int          `json:"version"`
	Days    []DaySummary `json:"days"`
}

// SummarizeRun reduces a run snapshot to its day-index row.
func SummarizeRun(s *Snapshot, report string) DayRun {
	run := DayRun{Report: report, URL: s.Job.URL, Job: s.Job.Name, PR: s.Job.PR, BuildID: s.Job.BuildID, Result: s.Job.Result, StartedAt: s.Job.StartedAt, Complete: !s.HasErrors()}
	for _, group := range s.Groups {
		if group.BillingStatus != "complete" {
			run.Complete = false
		}
		var net float64
		for _, resource := range group.Resources {
			for _, charge := range resource.Charges {
				net += charge.CostUSD
			}
		}
		switch group.Category {
		case "Infra":
			run.InfraUSD += net
		case "Tests":
			run.TestsUSD += net
		}
		run.NetUSD += net
	}
	return run
}

// SummarizeDay totals a day's runs for the site manifest.
func SummarizeDay(day string, runs []DayRun) DaySummary {
	summary := DaySummary{Day: day, Runs: len(runs)}
	for _, run := range runs {
		summary.NetUSD += run.NetUSD
		if !run.Complete {
			summary.Incomplete++
		}
	}
	return summary
}

// ReadManifest reads the site manifest; a missing file is an empty manifest.
func ReadManifest(path string) (*SiteManifest, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &SiteManifest{Version: SchemaVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest SiteManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.Version != SchemaVersion {
		return nil, fmt.Errorf("unsupported manifest version %d (want %d)", manifest.Version, SchemaVersion)
	}
	return &manifest, nil
}

// Upsert replaces or adds a day and keeps days newest first, so rerunning a
// day refreshes its entry.
func (m *SiteManifest) Upsert(summary DaySummary) {
	kept := m.Days[:0]
	for _, day := range m.Days {
		if day.Day != summary.Day {
			kept = append(kept, day)
		}
	}
	m.Days = append(kept, summary)
	sort.Slice(m.Days, func(a, b int) bool { return m.Days[a].Day > m.Days[b].Day })
}

// WriteDayIndex writes the day's run list as JSON, most expensive first.
func WriteDayIndex(w io.Writer, day string, runs []DayRun) error {
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return fmt.Errorf("write day index: invalid day %q", day)
	}
	sorted := append([]DayRun(nil), runs...)
	for _, run := range sorted {
		if math.IsNaN(run.NetUSD) || math.IsInf(run.NetUSD, 0) {
			return fmt.Errorf("write day index: run %s has non-finite cost", run.BuildID)
		}
	}
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].NetUSD > sorted[b].NetUSD })
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(DayIndex{Version: SchemaVersion, Day: day, Summary: SummarizeDay(day, runs), Runs: sorted}); err != nil {
		return fmt.Errorf("write day index: %w", err)
	}
	return nil
}

// RenderViewer writes the site viewer: one page that lists published days and
// renders day indexes and run reports from their JSON, with the single-file
// report's styles and scripts.
func RenderViewer(w io.Writer) error {
	licenses, err := json.Marshal(map[string]string{"notice": echartsNotice, "license": echartsLicense + "\n\n" + d3License})
	if err != nil {
		return fmt.Errorf("render viewer: encode licenses: %w", err)
	}
	tmpl, err := template.New("viewer").Parse(viewerTemplate)
	if err != nil {
		return fmt.Errorf("render viewer: parse template: %w", err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, struct {
		CSS      template.CSS
		Runtime  template.JS
		Script   template.JS
		Licenses template.JS
	}{template.CSS(reportCSS), template.JS(echartsRuntime), template.JS(reportScript), template.JS(licenses)}); err != nil {
		return fmt.Errorf("render viewer: execute template: %w", err)
	}
	if _, err := output.WriteTo(w); err != nil {
		return fmt.Errorf("render viewer: write: %w", err)
	}
	return nil
}
