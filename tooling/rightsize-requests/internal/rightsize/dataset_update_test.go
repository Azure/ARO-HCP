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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func runDatasetOutput(t *testing.T, sizing bool, paths []string, target string, opts Options) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old }()
	if sizing {
		err = RunSizingInputs(context.Background(), logr.Discard(), paths, target, sizingPrefix, opts)
	} else {
		opts.ConfigPath = target
		err = RunInputs(context.Background(), logr.Discard(), paths, opts)
	}
	return fileContents(t, out.Name()), err
}

func TestDatasetUpdates(t *testing.T) {
	for _, mode := range []string{"service", "hcp"} {
		for _, resource := range []string{"cpu", "memory"} {
			t.Run(mode+"/"+resource, func(t *testing.T) {
				sizing := mode == "hcp"
				makeRow := inputRow
				before := inputConfig
				if sizing {
					makeRow, before = sizingRow, sizingTemplate()
				}
				suffix, scale, other := "m", .001, "memory"
				if resource == "memory" {
					suffix, scale, other = "Mi", 1<<20, "cpu"
				}
				row := func(peak, target, current float64) inputRecommendation {
					return makeRow(resource, peak*scale, target*scale, current*scale, fmt.Sprintf("%.0f%s", target, suffix))
				}
				replace := func(content, resource string, old, next int) string {
					suffix, comment := "m", " # effective dev value"
					if resource == "memory" {
						suffix, comment = "Mi", ""
					}
					if sizing {
						comment = " # kube-apiserver"
					}
					return strings.Replace(content, fmt.Sprintf("%s: %d%s%s\n", resource, old, suffix, comment), fmt.Sprintf("%s: %d%s%s\n", resource, next, suffix, comment), 1)
				}
				quiet, busy := row(119, 120, 200), row(359, 360, 200)
				middle := row(239, 240, 200)
				low := row(59, 60, 200)
				unrelated := quiet
				unrelated.Container = "unmapped-container"
				independent := makeRow("memory", 119*(1<<20), 120*(1<<20), 200*(1<<20), "120Mi")
				if other == "cpu" {
					independent = makeRow("cpu", .119, .12, .2, "120m")
				}
				firstRange, secondRange := row(239, 240, 100), row(239, 240, 300)
				firstRange.RequestMax, secondRange.RequestMax = number(110*scale), number(310*scale)
				// Keep identities identical across reports: distinct runs must not be
				// collapsed by workload identity or by their observed request ranges.
				type testCase struct {
					name       string
					reports    [][]inputRecommendation
					current    int
					want       int
					wantOther  int
					allow      bool
					provenance bool
				}
				cases := []testCase{
					{name: "maximum across all rows", reports: [][]inputRecommendation{{quiet, middle}, {busy, quiet}}, want: 360, allow: true, provenance: true},
					{name: "maximum reduction", reports: [][]inputRecommendation{{low}, {quiet}}, want: 120, allow: true, provenance: true},
					{name: "reduction requires permission", reports: [][]inputRecommendation{{low}, {quiet}}, want: 200},
					{name: "empty report blocks reduction", reports: [][]inputRecommendation{{low, quiet}, {}}, want: 200, allow: true},
					{name: "other target does not cover reduction", reports: [][]inputRecommendation{{quiet}, {unrelated}}, want: 200, allow: true},
					{name: "resource absence is independent", reports: [][]inputRecommendation{{quiet, independent}, {independent}}, want: 200, wantOther: 120, allow: true},
					{name: "empty report allows increase", reports: [][]inputRecommendation{{busy}, {}}, want: 360},
					{name: "other target allows increase", reports: [][]inputRecommendation{{busy}, {unrelated}}, want: 360},
					{name: "request range gap is stale", reports: [][]inputRecommendation{{firstRange}, {secondRange}}, want: 200, allow: true},
					{name: "first request range is sufficient", reports: [][]inputRecommendation{{firstRange}, {secondRange}}, current: 110, want: 240, allow: true},
					{name: "second request range is sufficient", reports: [][]inputRecommendation{{firstRange}, {secondRange}}, current: 310, want: 240, allow: true},
					{name: "request above all ranges is stale", reports: [][]inputRecommendation{{firstRange}, {secondRange}}, current: 500, want: 500, allow: true},
					{name: "target equality outside ranges is noop", reports: [][]inputRecommendation{{firstRange}, {secondRange}}, current: 240, want: 240, allow: true},
				}
				for _, reduction := range []bool{false, true} {
					for _, badIndex := range []int{0, 1} {
						rows := []inputRecommendation{quiet, busy}
						if reduction {
							rows = []inputRecommendation{low, quiet}
						}
						rows[badIndex].Eligible = false
						cases = append(cases, testCase{
							name:    fmt.Sprintf("ineligible row %d reduction=%t", badIndex, reduction),
							reports: [][]inputRecommendation{{rows[0], independent}, {rows[1], independent}},
							want:    200, wantOther: 120, allow: true,
						})
						if !reduction {
							cases = append(cases, testCase{
								name:    fmt.Sprintf("absence does not bypass ineligible row %d", badIndex),
								reports: [][]inputRecommendation{{rows[0]}, {rows[1]}, {}}, want: 200,
							})
						}
					}
				}
				for _, tc := range cases {
					for _, reverse := range []bool{false, true} {
						for _, duplicates := range []bool{false, true} {
							t.Run(fmt.Sprintf("%s/reverse=%t/duplicates=%t", tc.name, reverse, duplicates), func(t *testing.T) {
								current, wantOther := tc.current, tc.wantOther
								if current == 0 {
									current = 200
								}
								if wantOther == 0 {
									wantOther = 200
								}
								initial := replace(before, resource, 200, current)
								want := replace(initial, resource, current, tc.want)
								want = replace(want, other, 200, wantOther)
								target := inputFile(t, "target.yaml", initial)
								dir := t.TempDir()
								var paths []string
								var governing string
								for i, rows := range tc.reports {
									index := i
									rows = slices.Clone(rows)
									if reverse {
										// Reverse canonical filename order and within-report row order,
										// not just argument order (the reader sorts paths).
										index = len(tc.reports) - 1 - i
										slices.Reverse(rows)
									}
									path := filepath.Join(dir, fmt.Sprintf("%d-report.json", index))
									data := inputJSON(t, rows...)
									if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
										t.Fatal(err)
									}
									paths = append(paths, path)
									if i == 1 {
										governing = path
									}
									if duplicates {
										var object map[string]json.RawMessage
										if err := json.Unmarshal([]byte(data), &object); err != nil {
											t.Fatal(err)
										}
										pretty, err := json.MarshalIndent(object, "", "  ")
										if err != nil {
											t.Fatal(err)
										}
										copyPath := filepath.Join(dir, fmt.Sprintf("copy-%d.json", index))
										if err := os.WriteFile(copyPath, pretty, 0o600); err != nil {
											t.Fatal(err)
										}
										paths = append(paths, path, copyPath)
									}
								}
								opts := Options{AllowDecrease: tc.allow, ChangeThreshold: .1}
								for run := range 2 {
									output, err := runDatasetOutput(t, sizing, paths, target, opts)
									if err != nil {
										t.Fatal(err)
									}
									if got := fileContents(t, target); got != want {
										t.Fatalf("run %d made incorrect or out-of-scope edits:\ngot:\n%s\nwant:\n%s", run, got, want)
									}
									if run == 0 && tc.provenance && !strings.Contains(output, "governing source "+governing) {
										t.Fatalf("missing governing source %s: %s", governing, output)
									}
									slices.Reverse(paths)
								}
							})
						}
					}
				}
			})
		}
	}
}

func TestDatasetUpdatesValidateBeforeWrites(t *testing.T) {
	for _, mode := range []string{"service", "hcp"} {
		t.Run(mode, func(t *testing.T) {
			sizing := mode == "hcp"
			makeRow, before := inputRow, inputConfig
			if sizing {
				makeRow, before = sizingRow, sizingTemplate()
			}
			valid := inputJSON(t, makeRow("cpu", .359, .36, .2, "360m"), makeRow("memory", 359*(1<<20), 360*(1<<20), 200*(1<<20), "360Mi"))
			if _, err := readInput(inputFile(t, "valid.json", valid)); err != nil {
				t.Fatalf("baseline report must be valid: %v", err)
			}
			for name, invalid := range map[string]string{
				"old headroom policy": strings.Replace(valid, `"version":3`, `"version":2`, 1),
				"schema":              strings.Replace(valid, `"version":3`, `"version":3,"unexpected":true`, 1),
				"invalid suggestion":  strings.Replace(valid, `"360Mi"`, `"370Mi"`, 1),
				"mixed windows":       strings.Replace(valid, `"10m"`, `"2m"`, 1),
				"empty mixed window":  strings.Replace(inputJSON(t), `"10m"`, `"2m"`, 1),
				"missing report":      "",
			} {
				t.Run(name, func(t *testing.T) {
					dir := t.TempDir()
					good, bad := filepath.Join(dir, "a-valid.json"), filepath.Join(dir, "z-invalid.json")
					if err := os.WriteFile(good, []byte(valid), 0o600); err != nil {
						t.Fatal(err)
					}
					if invalid != "" {
						if err := os.WriteFile(bad, []byte(invalid), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					for _, paths := range [][]string{{good, bad}, {bad, good}} {
						target := inputFile(t, "target.yaml", before)
						if _, err := runDatasetOutput(t, sizing, paths, target, Options{}); err == nil {
							t.Fatal("accepted invalid dataset")
						}
						if fileContents(t, target) != before {
							t.Fatal("edited target before validating every report")
						}
					}
				})
			}
		})
	}
}
