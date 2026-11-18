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

package citests

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// cloneMap deep-copies a decoded JSON document so each mutation is independent.
func cloneMap(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

// requireReplayError asserts that a replay document is rejected with a message
// naming the reason, so a rejected input is never accepted for another cause.
func requireReplayError(t *testing.T, document any, want string) {
	t.Helper()
	var data []byte
	switch value := document.(type) {
	case string:
		data = []byte(value)
	case []byte:
		data = value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		data = encoded
	}
	bundle, err := ReadBundle(bytes.NewReader(data))
	if err == nil {
		t.Fatalf("accepted invalid replay document: %+v", bundle)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("replay error %q does not mention %q", err, want)
	}
}

// loadFixture reads the synthetic observations fixture shared with the scoring tests.
func loadFixture(t *testing.T) Bundle {
	t.Helper()
	file, err := os.Open("testdata/priority-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	bundle, err := ReadBundle(file)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestReadBundleValidation(t *testing.T) {
	data, err := os.ReadFile("testdata/priority-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	bundle, err := ReadBundle(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Selection.JobFilter != "e2e-parallel" || len(bundle.Runs) != 8 || len(bundle.Observations) != 20 {
		t.Fatalf("replay lost population: %+v", bundle)
	}

	unknownVersion := cloneMap(t, raw)
	unknownVersion["schema_version"] = 2
	requireReplayError(t, unknownVersion, "schema_version")

	unknownField := cloneMap(t, raw)
	unknownField["extra"] = true
	requireReplayError(t, unknownField, "unknown field")

	trailing := append(append([]byte(nil), data...), []byte("{}\n")...)
	requireReplayError(t, string(trailing), "exactly one JSON object")

	duplicateRun := cloneMap(t, raw)
	runs := duplicateRun["runs"].([]any)
	duplicateRun["runs"] = append(runs, runs[0])
	requireReplayError(t, duplicateRun, "duplicate run")

	outsideWindow := cloneMap(t, raw)
	run := outsideWindow["runs"].([]any)[0].(map[string]any)
	run["finished_at"] = "2026-03-01T00:00:00Z"
	requireReplayError(t, outsideWindow, "outside the recorded selection window")

	unknownBuild := cloneMap(t, raw)
	observation := unknownBuild["observations"].([]any)[0].(map[string]any)
	observation["build_id"] = "not-selected"
	requireReplayError(t, unknownBuild, "unknown run")

	wrongJob := cloneMap(t, raw)
	wrongJobRun := wrongJob["runs"].([]any)[0].(map[string]any)
	wrongJobRun["job_name"] = "unrelated-job"
	requireReplayError(t, wrongJob, "job_filter")
}

func TestSaveBundleRefusesToReplaceExistingFile(t *testing.T) {
	bundle := loadFixture(t)
	target := t.TempDir() + "/observations.json"
	if err := SaveBundle(target, bundle); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("saved bundle is not private: %v", info.Mode())
	}
	if err := SaveBundle(target, bundle); err == nil {
		t.Fatal("replaced an existing observations file")
	}
}
