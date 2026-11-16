// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package cifailures collects CI facts and builds deterministic failure reports.
// Extraction rules live in cifailurepatterns; this package owns provenance and IO.
package cifailures

import (
	"time"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

// BundleSchemaVersion identifies saved raw inputs, independently of report output.
const BundleSchemaVersion = 2

// ReportSchemaVersion identifies report output, independently of raw replay inputs.
const ReportSchemaVersion = 1

type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type Filters struct {
	Source    string `json:"source"`
	JobFilter string `json:"job_filter"`
}

type Metadata struct {
	Origin      string  `json:"origin"`
	Environment string  `json:"environment"`
	Window      Window  `json:"window"`
	Filters     Filters `json:"filters"`
}

// Observation preserves all available test timestamps and exact raw message.
// Repeated identical rows remain separate elements with the same fingerprint.
type Observation struct {
	Origin           string    `json:"origin"`
	Environment      string    `json:"environment"`
	BuildID          string    `json:"build_id"`
	Source           string    `json:"source"`
	TestID           string    `json:"test_id"`
	TestName         string    `json:"test_name"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	SetupFinishTime  time.Time `json:"setup_finish_time"`
	TestStartTime    time.Time `json:"test_start_time"`
	CleanupStartTime time.Time `json:"cleanup_start_time"`
	Text             string    `json:"text"`
	Fingerprint      string    `json:"fingerprint"`
	NameVariants     int64     `json:"name_variants"`
}

type Run struct {
	BuildID     string    `json:"build_id"`
	Environment string    `json:"environment,omitempty"`
	JobName     string    `json:"job_name"`
	ProwURL     string    `json:"prow_url"`
	FinishedAt  time.Time `json:"finished_at"`
	Failed      bool      `json:"failed"`
	Processed   bool      `json:"processed"`
}

type Bundle struct {
	SchemaVersion int           `json:"schema_version"`
	Metadata      Metadata      `json:"metadata"`
	Runs          []Run         `json:"runs"`
	Observations  []Observation `json:"observations"`
}

// Result retains one exact input occurrence and the extractor's complete output.
type Result struct {
	Observation Observation                 `json:"observation"`
	Findings    []cifailurepatterns.Finding `json:"findings"`
}

// Summary counts input occurrences independently of overlapping pattern memberships.
type Summary struct {
	InputCount        int            `json:"input_count"`
	FindingCount      int            `json:"finding_count"`
	PatternCount      int            `json:"pattern_count"`
	AffectedRunCount  int            `json:"affected_run_count"`
	SourceInputCounts map[string]int `json:"source_input_counts"`
}

// Pattern indexes findings with exactly the same environment, source, and key.
// Canonical is the lexicographically smallest description among those findings.
type Pattern struct {
	Environment        string         `json:"environment"`
	Source             string         `json:"source"`
	Key                string         `json:"key"`
	Canonical          string         `json:"canonical"`
	AffectedInputCount int            `json:"affected_input_count"`
	AffectedRunCount   int            `json:"affected_run_count"`
	OutcomeCounts      map[string]int `json:"outcome_counts"`
	RuleIDs            []string       `json:"rule_ids"`
	QualityReasons     []string       `json:"quality_reasons"`
	ResultIndexes      []int          `json:"result_indexes"`
}

type Report struct {
	SchemaVersion    int       `json:"schema_version"`
	ExtractorVersion string    `json:"extractor_version"`
	Metadata         Metadata  `json:"metadata"`
	Summary          Summary   `json:"summary"`
	Patterns         []Pattern `json:"patterns"`
	Results          []Result  `json:"results"`
}
