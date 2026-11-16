// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cifailures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

func testBundle() Bundle {
	from := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	metadata := Metadata{Origin: "https://kusto.example.test/ServiceLogs", Environment: "dev",
		Window: Window{From: from, To: from.Add(24 * time.Hour)}, Filters: Filters{JobFilter: "e2e"}}
	observation := Observation{Origin: metadata.Origin, Environment: metadata.Environment,
		BuildID: "run-1", Source: "e2e", TestID: "test-1", TestName: "create cluster", Text: "context deadline exceeded"}
	observation.Fingerprint = Fingerprint(observation)
	return Bundle{SchemaVersion: BundleSchemaVersion, Metadata: metadata,
		Runs: []Run{{BuildID: "run-1", Environment: "dev", JobName: "periodic-e2e", FinishedAt: from, Failed: true, Processed: true},
			{BuildID: "run-2", Environment: "dev", JobName: "periodic-e2e", FinishedAt: from.Add(time.Hour), Failed: true}},
		Observations: []Observation{observation}}
}

func TestReportVersionIsIndependentOfReplaySchema(t *testing.T) {
	for _, schema := range []int{1, 2} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("schema-%d/empty-%t", schema, empty), func(t *testing.T) {
				bundle := testBundle()
				bundle.SchemaVersion = schema
				if schema == 1 {
					for i := range bundle.Runs {
						bundle.Runs[i].Environment = ""
					}
				}
				if empty {
					bundle.Runs, bundle.Observations = nil, nil
				}
				input, err := json.Marshal(bundle)
				if err != nil {
					t.Fatal(err)
				}
				replayed, err := ReadBundle(bytes.NewReader(input))
				if err != nil {
					t.Fatal(err)
				}
				report, err := BuildReport(replayed)
				if err != nil {
					t.Fatal(err)
				}
				if BundleSchemaVersion != 2 || ReportSchemaVersion != 1 || report.SchemaVersion != 1 || report.ExtractorVersion != cifailurepatterns.Version {
					t.Fatalf("wrong extraction contract: bundle=%d report=%d version=%q", BundleSchemaVersion, report.SchemaVersion, report.ExtractorVersion)
				}
				if !reflect.DeepEqual(report.Metadata, bundle.Metadata) {
					t.Fatalf("report changed metadata: got %+v, want %+v", report.Metadata, bundle.Metadata)
				}
				if report.Results == nil || len(report.Results) != len(bundle.Observations) {
					t.Fatalf("wrong result population: %+v", report.Results)
				}
				output, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(output, &fields); err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"schema_version", "extractor_version", "metadata", "summary", "patterns", "results"} {
					if _, exists := fields[field]; !exists {
						t.Fatalf("missing report field %q: %s", field, output)
					}
				}
				var version string
				if err := json.Unmarshal(fields["extractor_version"], &version); err != nil || version != cifailurepatterns.Version {
					t.Fatalf("missing package-owned JSON version: %q, %v", version, err)
				}
				if empty && string(fields["results"]) != "[]" {
					t.Fatalf("empty results must encode as [], got %s", fields["results"])
				}
				if empty && (string(fields["patterns"]) != "[]" || report.Summary.InputCount != 0 ||
					report.Summary.FindingCount != 0 || report.Summary.PatternCount != 0 ||
					report.Summary.AffectedRunCount != 0 || string(fields["summary"]) == "null" ||
					report.Summary.SourceInputCounts == nil || len(report.Summary.SourceInputCounts) != 0) {
					t.Fatalf("empty report must have zero totals and empty collections: %s", output)
				}
				if bytes.Index(output, []byte(`"patterns":`)) > bytes.Index(output, []byte(`"results":`)) {
					t.Fatal("pattern index must precede raw results")
				}
				unchanged, err := json.Marshal(replayed)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(input, unchanged) {
					t.Fatal("report generation changed saved replay inputs")
				}
			})
		}
	}
}

func TestFingerprintExactVersionedInputs(t *testing.T) {
	original := testBundle().Observations[0]
	changes := []struct {
		name   string
		change func(*Observation)
	}{
		{"origin", func(o *Observation) { o.Origin += "/other" }},
		{"build", func(o *Observation) { o.BuildID += "x" }},
		{"source", func(o *Observation) { o.Source = "rp-upgrade" }},
		{"test ID", func(o *Observation) { o.TestID += "x" }},
		{"raw newline", func(o *Observation) { o.Text += "\n" }},
		{"raw whitespace", func(o *Observation) { o.Text = " " + o.Text }},
		{"started", func(o *Observation) { o.StartedAt = time.Unix(1, 0) }},
		{"finished", func(o *Observation) { o.FinishedAt = time.Unix(1, 0) }},
		{"setup", func(o *Observation) { o.SetupFinishTime = time.Unix(1, 0) }},
		{"test start", func(o *Observation) { o.TestStartTime = time.Unix(1, 0) }},
		{"cleanup", func(o *Observation) { o.CleanupStartTime = time.Unix(1, 0) }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			changed := original
			tc.change(&changed)
			if Fingerprint(changed) == original.Fingerprint {
				t.Fatal("identity ignored changed raw input")
			}
		})
	}
	left, right := original, original
	left.BuildID, left.Source = "a\x00b", "c"
	right.BuildID, right.Source = "a", "b\x00c"
	if Fingerprint(left) == Fingerprint(right) {
		t.Fatal("ambiguous delimiter encoding")
	}
	changed := original
	changed.Environment = "other"
	changed.Fingerprint = "ignored"
	changed.NameVariants = 2
	changed.TestName = "late-arriving corrected test name"
	if Fingerprint(changed) != original.Fingerprint {
		t.Fatal("report-only context changed identity")
	}
	left.StartedAt = time.Date(2026, 10, 28, 1, 0, 0, 0, time.FixedZone("one", 3600))
	right = left
	right.StartedAt = left.StartedAt.UTC()
	if Fingerprint(left) != Fingerprint(right) {
		t.Fatal("equivalent instants differ")
	}
	if !strings.HasPrefix(original.Fingerprint, "sha256:v1:") {
		t.Fatal("missing fingerprint version")
	}
}

func TestReplayRetainsMetadataAndRawEvidence(t *testing.T) {
	bundle := testBundle()
	bundle.Observations[0].Text = "  diagnostic\r\n\t<secret>  "
	bundle.Observations[0].Fingerprint = Fingerprint(bundle.Observations[0])
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(bundle); err != nil {
		t.Fatal(err)
	}
	replayed, err := ReadBundle(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bundle, replayed) {
		t.Fatal("replay changed raw inputs or saved filters/window")
	}
	before, err := BuildReport(bundle)
	if err != nil {
		t.Fatal(err)
	}
	after, err := BuildReport(replayed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("live/replay report diverged")
	}
}

func TestReplayRejectsInvalidInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Bundle)
	}{
		{"schema", func(b *Bundle) { b.SchemaVersion++ }},
		{"fingerprint", func(b *Bundle) { b.Observations[0].Text += "changed" }},
		{"missing fingerprint", func(b *Bundle) { b.Observations[0].Fingerprint = "" }},
		{"duplicate run", func(b *Bundle) { b.Runs = append(b.Runs, b.Runs[0]) }},
		{"unknown run", func(b *Bundle) { b.Observations[0].BuildID = "unknown" }},
		{"exclusive end", func(b *Bundle) { b.Runs[0].FinishedAt = b.Metadata.Window.To }},
		{"before start", func(b *Bundle) { b.Runs[0].FinishedAt = b.Metadata.Window.From.Add(-time.Nanosecond) }},
		{"filter", func(b *Bundle) { b.Metadata.Filters.Source = "rp-upgrade" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := testBundle()
			tc.mutate(&bundle)
			data, err := json.Marshal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadBundle(bytes.NewReader(data)); err == nil {
				t.Fatal("accepted invalid replay")
			}
			if _, err := BuildReport(bundle); err == nil {
				t.Fatal("accepted invalid report input")
			}
		})
	}
	data, err := json.Marshal(testBundle())
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"{}", "garbage"} {
		if _, err := ReadBundle(strings.NewReader(string(data) + suffix)); err == nil {
			t.Fatal("accepted trailing input")
		}
	}
}

func TestSaveBundleAtomicPrivateAndNoClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	bundle := testBundle()
	if err := SaveBundle(path, bundle); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe permissions %v", info.Mode())
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBundle(path, bundle); err == nil {
		t.Fatal("overwrote existing raw evidence")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, current) {
		t.Fatal("existing file was corrupted")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
	if err := SaveBundle("-", bundle); err == nil {
		t.Fatal("allowed raw data on stdout")
	}
}

func TestFingerprintSurvivesLateNameEnrichment(t *testing.T) {
	observation := testBundle().Observations[0]
	observation.TestName = ""
	before := Fingerprint(observation)
	observation.TestName = "newly ingested test name"
	if Fingerprint(observation) != before {
		t.Fatal("name enrichment changed immutable observation identity")
	}
}
