// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ci

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cifailures"
)

func TestFailurePatternsRejectsInvalidOptionsBeforeAuthentication(t *testing.T) {
	live := []string{"--kusto", "hcp-dev-us-2", "--region", "eastus2", "--environment", "dev"}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no target", nil, "--kusto"},
		{"no window", live, "--from"},
		{"negative since", appendCopy(live, "--since", "-1h"), "--since"},
		{"mixed windows", appendCopy(live, "--since", "24h", "--from", "2026-10-28T00:00:00Z"), "mutually exclusive"},
		{"invalid timestamp", appendCopy(live, "--from", "yesterday", "--to", "now"), "RFC3339"},
		{"empty window", appendCopy(live, "--from", "2026-10-28T00:00:00Z", "--to", "2026-10-28T00:00:00Z"), "--from < --to"},
		{"reversed window", appendCopy(live, "--from", "2026-10-29T00:00:00Z", "--to", "2026-10-28T00:00:00Z"), "--from < --to"},
		{"unbounded timeout", appendCopy(live, "--since", "24h", "--timeout", "0s"), "--timeout"},
		{"excessive timeout", appendCopy(live, "--since", "24h", "--timeout", "31m"), "--timeout"},
		{"save stdout", []string{"--input", "any.json", "--save-input", "-"}, "not stdout"},
		{"invalid environment", appendCopy(live, "--since", "24h", "--environment", "invalid"), "--environment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newFailurePatternsCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
			if output.Len() != 0 {
				t.Fatalf("validation corrupted output: %q", output.String())
			}
		})
	}
	for _, flag := range []string{"kusto", "region", "database", "environment", "from", "to", "source", "job-filter", "since", "timeout"} {
		t.Run("replay excludes "+flag, func(t *testing.T) {
			value := "value"
			if flag == "since" || flag == "timeout" {
				value = "1h"
			}
			cmd := newFailurePatternsCommand()
			cmd.SetArgs([]string{"--input", "nonexistent.json", "--" + flag, value})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
				t.Fatalf("live override accepted: %v", err)
			}
		})
	}
}

func TestFailurePatternsCompleteResolvesUTCWindow(t *testing.T) {
	fixed := time.Date(2026, 10, 29, 5, 0, 0, 0, time.FixedZone("offset", 3600))
	o := &failurePatternsOptions{cluster: "hcp-dev-us-2", region: "eastus2", database: "ServiceLogs", environment: "dev",
		since: 24 * time.Hour, now: func() time.Time { return fixed }, source: "rp-provision", jobFilter: "parallel"}
	if err := o.Complete(&cobra.Command{}); err != nil {
		t.Fatal(err)
	}
	if o.metadata.Window.To.Location() != time.UTC || !o.metadata.Window.To.Equal(fixed) || o.metadata.Window.To.Sub(o.metadata.Window.From) != 24*time.Hour {
		t.Fatalf("wrong shorthand window: %+v", o.metadata.Window)
	}
	if o.metadata.Filters.Source != "rp-provision" || o.metadata.Filters.JobFilter != "parallel" || o.metadata.Environment != "dev" {
		t.Fatalf("lost metadata: %+v", o.metadata)
	}
}

func TestFailurePatternsOfflinePreservesMetadataAndKeepsStdoutClean(t *testing.T) {
	from := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	bundle := cifailures.Bundle{SchemaVersion: cifailures.BundleSchemaVersion,
		Metadata:     cifailures.Metadata{Origin: "saved-origin", Environment: "int", Window: cifailures.Window{From: from, To: from.Add(time.Hour)}, Filters: cifailures.Filters{Source: "rp-upgrade", JobFilter: "upgrade"}},
		Runs:         []cifailures.Run{{BuildID: "1", Environment: "int", JobName: "upgrade", FinishedAt: from, Failed: true}},
		Observations: []cifailures.Observation{{Origin: "saved-origin", Environment: "int", BuildID: "1", Source: "rp-upgrade", TestID: "step-1", Text: "context deadline exceeded"}},
	}
	bundle.Observations[0].Fingerprint = cifailures.Fingerprint(bundle.Observations[0])
	input := filepath.Join(t.TempDir(), "input.json")
	if err := cifailures.SaveBundle(input, bundle); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "saved.json")
	// An invalid credential selection must be irrelevant to offline execution.
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "not-a-credential")
	cmd := newFailurePatternsCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--input", input, "--save-input", outputPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var actual cifailures.Report
	if err := json.Unmarshal(stdout.Bytes(), &actual); err != nil {
		t.Fatalf("stdout is not a clean report: %v", err)
	}
	if !reflect.DeepEqual(actual.Metadata, bundle.Metadata) {
		t.Fatal("replay reinterpreted saved window/filters")
	}
	if !strings.Contains(stderr.String(), "Saved raw replay input") {
		t.Fatal("save diagnostic not on stderr")
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatal(err)
	}
}

func TestFailurePatternsRejectsUnknownReplaySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newFailurePatternsCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--input", path})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("unknown schema accepted: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("failed input emitted a report")
	}
}

func appendCopy(base []string, values ...string) []string {
	return append(append([]string(nil), base...), values...)
}
