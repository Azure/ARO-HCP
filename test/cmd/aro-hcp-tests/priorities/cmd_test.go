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

package priorities

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// run executes the command with the given arguments and returns stdout, stderr.
// fixturePath is the synthetic observations file shared with the scoring tests.
var fixturePath = filepath.Join("..", "..", "..", "pkg", "citests", "testdata", "priority-observations.json")

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	command := NewCommand()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

func TestValidateRejectsInvalidInvocations(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		substring string
	}{
		{name: "replay with live flags", args: []string{"--input", "saved.json", "--kusto", "hcp-dev-us-2"}, substring: "mutually exclusive"},
		{name: "replay with timeout", args: []string{"--input", "saved.json", "--timeout", "5m"}, substring: "mutually exclusive"},
		{name: "unknown output", args: []string{"--input", "saved.json", "--output", "yaml"}, substring: "--output must be table or json"},
		{name: "too few samples", args: []string{"--input", "saved.json", "--min-samples", "2"}, substring: "--min-samples must be at least 3"},
		{name: "save to stdout", args: []string{"--input", "saved.json", "--save-input", "-"}, substring: "not stdout"},
		{name: "no window", args: []string{"--kusto", "hcp-dev-us-2", "--region", "eastus2"}, substring: "--from must be RFC3339"},
		{name: "since with from", args: []string{"--kusto", "hcp-dev-us-2", "--region", "eastus2", "--since", "24h", "--from", "2026-01-01T00:00:00Z", "--to", "2026-02-01T00:00:00Z"}, substring: "mutually exclusive"},
		{name: "unbounded timeout", args: []string{"--kusto", "hcp-dev-us-2", "--region", "eastus2", "--since", "24h", "--timeout", "31m"}, substring: "at most 30m"},
		{name: "invalid environment", args: []string{"--kusto", "hcp-dev-us-2", "--region", "eastus2", "--since", "24h", "--environment", "saas"}, substring: "--environment must be"},
		{name: "positional arguments", args: []string{"--input", "saved.json", "extra"}, substring: "unknown command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
		})
	}
}

func TestReplayNeedsNoAuthentication(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")
	t.Setenv("AZURE_TENANT_ID", "")
	stdout, stderr, err := run(t, "--input", fixturePath, "--output", "json")
	if err != nil {
		t.Fatalf("replay failed: %v (%s)", err, stderr)
	}
	var report citests.PriorityReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decoding report: %v\n%s", err, stdout)
	}
	if report.Algorithm != citests.AlgorithmEnvironmentNormalizedMean || report.GlobalMedianMinutes == nil || *report.GlobalMedianMinutes != 30 {
		t.Fatalf("unexpected report header: %+v", report)
	}
	want := map[string]int{"[sig-aro] should take the longest to finish": 90, "[sig-aro] should take a medium amount of time": 50, "[sig-aro] should take the shortest time": 20}
	scored := 0
	for _, row := range report.Tests {
		priority, ok := want[row.Name]
		if !ok {
			continue
		}
		scored++
		if row.ProposedPriority == nil || *row.ProposedPriority != priority {
			t.Fatalf("%q priority = %v, want %d", row.Name, row.ProposedPriority, priority)
		}
	}
	if scored != len(want) {
		t.Fatalf("report is missing scored rows: %+v", report.Tests)
	}
}

func TestReplayRejectsInvalidInput(t *testing.T) {
	directory := t.TempDir()
	original, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	documents := map[string][]byte{
		"trailing.json": append(append([]byte(nil), original...), []byte("{}\n")...),
		"version.json":  bytes.Replace(original, []byte(`"schema_version": 1`), []byte(`"schema_version": 9`), 1),
	}
	for name, content := range documents {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, _, err := run(t, "--input", path, "--output", "json")
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
		if stdout != "" {
			t.Fatalf("%s produced partial output: %s", name, stdout)
		}
	}
}

func TestTableOutputShowsCoverageAndProposals(t *testing.T) {
	stdout, _, err := run(t, "--input", fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"Selection  https://", "Algorithm  environment-normalized-mean-v1", "Coverage   8 selected",
		"dev      median 20.0m, 3 builds, 9 samples", "[sig-aro] should take the longest to finish",
		"insufficient-data",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("table output lost %q:\n%s", fragment, stdout)
		}
	}
}

func TestTableShowsExclusionsAndInsufficientStrata(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	selection := citests.Selection{Origin: "https://example.invalid/ServiceLogs", From: start, To: start.Add(24 * time.Hour), JobFilter: "e2e-parallel"}
	bundle := citests.Bundle{SchemaVersion: citests.BundleSchemaVersion, Selection: selection}
	observation := func(build, test, name string, minutes int) citests.Observation {
		return citests.Observation{BuildID: build, TestID: test, Name: name, NameVariants: 1, Source: "e2e", Result: "passed",
			StartedAt: start, FinishedAt: start.Add(time.Duration(minutes) * time.Minute)}
	}
	for index := 1; index <= 4; index++ {
		bundle.Runs = append(bundle.Runs, citests.Run{BuildID: "dev-" + strconv.Itoa(index), JobName: "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
			Environment: citests.EnvironmentDev, OverallResult: "SUCCESS", Processed: true,
			StartedAt: start, FinishedAt: start.Add(2 * time.Hour)})
	}
	bundle.Observations = append(bundle.Observations,
		observation("dev-1", "test-a", "example scored", 30),
		observation("dev-2", "test-a", "example scored", 30),
		observation("dev-3", "test-a", "example scored", 30),
		observation("dev-1", "test-b", "example thin", 30),
		observation("dev-2", "test-b", "example thin", 30),
	)
	timed := observation("dev-4", "test-a", "example scored", 30)
	timed.StartedAt, timed.FinishedAt = time.Time{}, time.Time{}
	bundle.Observations = append(bundle.Observations, timed)

	report, err := citests.ScorePriorities(bundle, citests.PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeTable(&output, report); err != nil {
		t.Fatal(err)
	}
	table := output.String()
	if !strings.Contains(table, "Excluded") || !strings.Contains(table, "invalid-timing: 1") {
		t.Fatalf("exclusions are hidden:\n%s", table)
	}
	// The thin row reports the two builds it does have instead of zero.
	found := false
	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "example thin") {
			continue
		}
		found = true
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[4] != "2" || fields[5] != "dev" || fields[6] != "insufficient-data" {
			t.Fatalf("insufficient row hides its evidence: %q", line)
		}
	}
	if !found {
		t.Fatalf("thin row is missing:\n%s", table)
	}
}
