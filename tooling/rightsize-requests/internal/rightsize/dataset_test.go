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

package rightsize

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadDatasetPaths(t *testing.T) {
	valid := inputFile(t, "valid.json", inputJSON(t))
	missing := filepath.Join(t.TempDir(), "missing.json")
	for _, paths := range [][]string{nil, {}, {""}, {" \t\n"}, {valid, ""}, {missing}, {valid, missing}} {
		dataset, err := readDataset(paths)
		if err == nil || !reflect.DeepEqual(dataset, inputDataset{}) {
			t.Fatalf("paths %q: expected error and no partial dataset, got %+v, %v", paths, dataset, err)
		}
		if len(paths) > 0 && paths[len(paths)-1] == missing && (!errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), missing)) {
			t.Fatalf("missing file error lost path or cause: %v", err)
		}
	}
}

func TestReadDatasetInvalidSecondReport(t *testing.T) {
	valid := inputJSON(t, inputRow("cpu", .239, .24, .1, "240m"))
	for name, invalid := range map[string]string{
		"schema":               strings.Replace(valid, `"version":3`, `"version":3,"extra":true`, 1),
		"source":               strings.Replace(valid, `"cluster":"svc"`, `"source":"forged","cluster":"svc"`, 1),
		"old version":          strings.Replace(valid, `"version":3`, `"version":1`, 1),
		"old headroom version": strings.Replace(valid, `"version":3`, `"version":2`, 1),
		"future version":       strings.Replace(valid, `"version":3`, `"version":4`, 1),
		"trailing object":      valid + `{}`,
		"trailing junk":        valid + `oops`,
		"replicas":             strings.Replace(valid, `"replicas":2`, `"replicas":-1`, 1),
		"headroom":             strings.Replace(valid, `"headroom":1`, `"headroom":1.3`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
			for path, content := range map[string]string{a: valid, b: invalid} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			dataset, err := readDataset([]string{a, b})
			if err == nil || !strings.Contains(err.Error(), b) || !reflect.DeepEqual(dataset, inputDataset{}) {
				t.Fatalf("expected second report error and no partial dataset, got %+v, %v", dataset, err)
			}
			if fileContents(t, a) != valid || fileContents(t, b) != invalid {
				t.Fatal("dataset reader modified input files")
			}
		})
	}
}

func TestReadDatasetCPUWindow(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			data := inputJSON(t)
			if !empty {
				data = inputJSON(t, inputRow("cpu", .239, .24, .1, "240m"))
			}
			a := inputFile(t, "10m.json", data)
			b := inputFile(t, "2m.json", strings.Replace(data, `"10m"`, `"2m"`, 1))
			for _, path := range []string{a, b} {
				if _, err := readDataset([]string{path}); err != nil {
					t.Fatal(err)
				}
			}
			for _, paths := range [][]string{{a, b}, {b, a}} {
				if _, err := readDataset(paths); err == nil || !strings.Contains(err.Error(), "cpuWindow") {
					t.Fatalf("accepted mixed CPU windows: %v", err)
				}
			}
		})
	}
}

func TestReadDatasetSemanticDuplicates(t *testing.T) {
	data := inputJSON(t, inputRow("cpu", .239, .24, .1, "240m"))
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &object); err != nil {
		t.Fatal(err)
	}
	pretty, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a, b, alias := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "0-alias.json")
	for path, content := range map[string]string{a: data, b: string(pretty)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{b, a, relative}, {b, a, alias, relative}, {alias, relative, b, a}} {
		original := append([]string(nil), paths...)
		dataset, err := readDataset(paths)
		if err != nil {
			t.Fatal(err)
		}
		wantPath := a
		if len(paths) == 4 {
			wantPath = alias
		}
		if len(dataset.Reports) != 1 || len(dataset.Recommendations) != 1 || dataset.Reports[0].Path != wantPath || dataset.Recommendations[0].source != wantPath {
			t.Fatalf("duplicates not collapsed to first absolute path: %+v", dataset)
		}
		wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
		if dataset.Reports[0].SHA256 != wantHash {
			t.Fatalf("hash=%s, want canonical hash=%s", dataset.Reports[0].SHA256, wantHash)
		}
		if !reflect.DeepEqual(paths, original) {
			t.Fatal("dataset reader reordered caller's paths")
		}
		encoded, err := json.Marshal(dataset.Recommendations[0])
		if err != nil || bytes.Contains(encoded, []byte(`"source"`)) {
			t.Fatalf("source leaked into JSON: %s, %v", encoded, err)
		}
	}
}

func TestReadDatasetPreservesReportsAndWarnings(t *testing.T) {
	row := inputRow("cpu", .239, .24, .1, "240m")
	samples := 12
	row.Samples = &samples
	row.Warnings = []string{"row warning", "report_missing"}
	unmapped := row
	unmapped.Container = "unknown"
	unmapped.Warnings = []string{"unmapped warning"}
	data := inputJSON(t, row, unmapped)
	data = strings.Replace(data, `"warnings":[]`, `"warnings":["report_missing","global warning","global warning"]`, 1)
	data = strings.Replace(data, `"version":3`, `"version":3,"savings":`+savingsJSON, 1)
	dir := t.TempDir()
	a, b, empty := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "empty.json")
	later := strings.NewReplacer("2026-09-01", "2026-09-03", "2026-09-02", "2026-09-04", `"changeThreshold":0.1`, `"changeThreshold":0.2`).Replace(data)
	emptyData := strings.Replace(inputJSON(t), `"warnings":[]`, `"warnings":["empty report warning"]`, 1)
	emptyData = strings.NewReplacer("2026-09-01", "2026-08-01", "2026-09-02", "2026-10-01").Replace(emptyData)
	for path, content := range map[string]string{a: data, b: later, empty: emptyData} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dataset, err := readDataset([]string{empty, b, a})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := readDataset([]string{a, b, empty})
	if err != nil || !reflect.DeepEqual(dataset, reversed) {
		t.Fatalf("dataset depends on input order: %v", err)
	}
	if len(dataset.Reports) != 3 || len(dataset.Recommendations) != 4 {
		t.Fatalf("distinct runs with matching identities were collapsed: %+v", dataset)
	}
	if dataset.CPUWindow != "10m" || !dataset.Start.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) || !dataset.End.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("incorrect metadata envelope: %+v", dataset)
	}
	warnings := []string{a + ": report_missing", a + ": global warning", a + ": global warning", b + ": report_missing", b + ": global warning", b + ": global warning", empty + ": empty report warning"}
	if !reflect.DeepEqual(dataset.Warnings, warnings) {
		t.Fatalf("global warnings=%q, want %q", dataset.Warnings, warnings)
	}
	for i, path := range []string{a, b, empty} {
		report, err := readInput(path)
		if err != nil || !reflect.DeepEqual(dataset.Reports[i].Report, report) || dataset.Reports[i].Path != path {
			t.Fatalf("report metadata, savings or evidence changed: %s, %v", path, err)
		}
		for j, want := range report.Recommendations {
			want.source = path
			if !reflect.DeepEqual(dataset.Recommendations[i*2+j], want) {
				t.Fatalf("row order, source, counts or warnings changed: report %d row %d", i, j)
			}
		}
	}
}

func TestReadDatasetRowOrderIsSignificant(t *testing.T) {
	a := inputRow("cpu", .239, .24, .1, "240m")
	b := a
	b.Container = "other"
	paths := []string{
		inputFile(t, "a.json", inputJSON(t, a, b)),
		inputFile(t, "b.json", inputJSON(t, b, a)),
	}
	dataset, err := readDataset(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Reports) != 2 || len(dataset.Recommendations) != 4 || dataset.Reports[0].SHA256 == dataset.Reports[1].SHA256 {
		t.Fatal("reordered reports must remain distinct with row-order-sensitive hashing")
	}
}
