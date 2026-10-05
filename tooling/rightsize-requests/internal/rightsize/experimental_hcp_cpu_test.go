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
	"os"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
)

func experimentalOutput(t *testing.T, inputs []string, template, config string, opts Options) (string, error) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old }()
	err = RunExperimentalHCPCPUInputs(context.Background(), logr.Discard(), inputs, template, config, sizingPrefix, opts)
	return fileContents(t, out.Name()), err
}

func TestExperimentalCPUMaximaAndPreservation(t *testing.T) {
	for _, additional := range []bool{false, true} {
		t.Run(map[bool]string{false: "template", true: "additional"}[additional], func(t *testing.T) {
			quiet := sizingRow("cpu", .019, .02, .2, "20m")
			quiet.Cluster = "ci01-test-mgmt-1"
			if additional {
				quiet.Workload, quiet.Container = "kube-scheduler", "kube-scheduler"
			}
			busy := quiet
			busy.Peak, busy.Suggested, busy.SuggestedQuantity = number(.079), nil, ""
			busy.RequestMin, busy.RequestMax, busy.Eligible, busy.MeasuredReplicas = nil, nil, false, 0
			busy.Warnings = []string{"low sample coverage"}
			unknown := quiet
			unknown.Peak, unknown.Suggested, unknown.SuggestedQuantity = nil, nil, ""
			unresolved := busy
			unresolved.Kind, unresolved.Peak = "Pod", number(.9)
			memory := quiet
			memory.Resource, memory.Peak, memory.Suggested, memory.SuggestedQuantity = "memory", number(9<<20), number(10<<20), "10Mi"
			a := inputFile(t, "quiet.json", inputJSON(t, quiet, memory, unresolved))
			b := inputFile(t, "busy.json", inputJSON(t, busy, unknown))
			c := inputFile(t, "absent.json", inputJSON(t))
			inputs := []string{a, b, c}
			template, config, before := "", "", sizingTemplate()
			if additional {
				before = additionalBaseline
				config = additionalConfigFile(t, before)
			} else {
				template = inputFile(t, "sizing.yaml", before)
			}
			path := template
			if additional {
				path = config
			}
			opts := Options{AllowDecrease: true, DryRun: true}
			output, err := experimentalOutput(t, inputs, template, config, opts)
			if err != nil || fileContents(t, path) != before {
				t.Fatalf("dry run failed or wrote: %v", err)
			}
			reverse, err := experimentalOutput(t, []string{c, b, a, b}, template, config, opts)
			if err != nil || output != reverse {
				t.Fatalf("duplicate/order changed output: %v", err)
			}
			for _, text := range []string{"reports=3", "sha256=", "latest-window-end=", "governing-max=0.079 target=80m source=" + b, "changes=1 seeds=0", "unknown-identities=1", "missing-peak-rows=1", "missing-request-rows=1", "low sample coverage", "missing report evidence", "Gross CPU reductions only"} {
				if !strings.Contains(output, text) {
					t.Fatalf("missing %q in %s", text, output)
				}
			}
			opts.DryRun = false
			if _, err := experimentalOutput(t, inputs, template, config, opts); err != nil {
				t.Fatal(err)
			}
			got := fileContents(t, path)
			if additional {
				ed, err := editor.NewAdditionalSizing(config)
				if err != nil || ed.Entries()[0].CPU != "80m" || ed.Entries()[0].Memory != "200Mi" || !strings.HasPrefix(got, strings.Split(before, "clouds:\n")[0]) {
					t.Fatalf("changed defaults/memory or lost max: %v\n%s", err, got)
				}
			} else if want := strings.Replace(before, "cpu: 200m # kube-apiserver", "cpu: 80m # kube-apiserver", 1); got != want {
				t.Fatalf("CPU-only source edit mismatch:\n%s", got)
			}
			output, err = experimentalOutput(t, inputs, template, config, opts)
			if err != nil || !strings.Contains(output, "NOOP "+quiet.Workload+"/"+quiet.Container) || strings.Contains(output, "WARNING stale") || fileContents(t, path) != got {
				t.Fatalf("not idempotent before stale check: %v\n%s", err, output)
			}
		})
	}
}

func TestExperimentalCPUSeedsAndGuards(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		mutate       func(*inputRecommendation)
		configured   bool
	}{
		{"seed", "changes=1 seeds=1", func(r *inputRecommendation) {}, false},
		{"ineligible seed", "changes=1 seeds=1", func(r *inputRecommendation) { r.Eligible = false; r.MeasuredReplicas = 0 }, false},
		{"range", "seed baseline ambiguous", func(r *inputRecommendation) { r.RequestMax = number(.3) }, false},
		{"zero baseline", "seed baseline ambiguous", func(r *inputRecommendation) { r.RequestMin = number(0); r.RequestMax = number(0) }, false},
		{"no baseline", "seed baseline ambiguous", func(r *inputRecommendation) { r.RequestMin = nil; r.RequestMax = nil }, false},
		{"no peak", "no known peak", func(r *inputRecommendation) { r.Peak = nil }, false},
		{"measured zero", "changes=1 seeds=1", func(r *inputRecommendation) {
			r.Peak = number(0)
			r.Suggested = number(.01)
			r.SuggestedQuantity = "10m"
		}, false},
		{"unresolved only", "unknown-identities=1", func(r *inputRecommendation) { r.Kind = "Pod" }, false},
		{"wrong kind", "unknown-identities=1", func(r *inputRecommendation) { r.Kind = "StatefulSet" }, false},
		{"unknown role", "unknown-identities=1", func(r *inputRecommendation) { r.Cluster = "mgmt" }, false},
		{"service role", "unknown-identities=1", func(r *inputRecommendation) { r.Cluster = "ci01-svc" }, false},
		{"init", "unknown-identities=1", func(r *inputRecommendation) { r.InitContainer = true }, false},
		{"original seven", "unknown-identities=1", func(r *inputRecommendation) { r.Workload = "kube-apiserver"; r.Container = "kube-apiserver" }, false},
		{"other namespace", "ignored-rows=1", func(r *inputRecommendation) { r.Namespace = "ocm-arohcpprod-hcp" }, false},
		{"deadband", "SKIP deadband", func(r *inputRecommendation) {
			r.Peak = number(.179)
			r.Suggested = number(.18)
			r.SuggestedQuantity = "180m"
		}, true},
		{"increase", "SKIP increase", func(r *inputRecommendation) {
			r.Peak = number(.299)
			r.Suggested = number(.3)
			r.SuggestedQuantity = "300m"
		}, true},
		{"stale", "WARNING stale", func(r *inputRecommendation) { r.RequestMin = number(.3); r.RequestMax = number(.3) }, true},
		{"no observed range", "WARNING stale", func(r *inputRecommendation) { r.RequestMin = nil; r.RequestMax = nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := additionalRow("cpu", .079, .08, .2, "80m")
			row.Cluster = "ci01-test-mgmt-1"
			tc.mutate(&row)
			before := "defaults:\n  hypershift:\n    additionalMinimalResourceRequests: {}\n"
			if tc.configured {
				before = additionalBaseline
			}
			config := additionalConfigFile(t, before)
			input := inputFile(t, "input.json", inputJSON(t, row))
			output, err := experimentalOutput(t, []string{input}, "", config, Options{AllowDecrease: true, ChangeThreshold: .1})
			if err != nil || !strings.Contains(output, tc.reason) {
				t.Fatalf("expected %q, got %v\n%s", tc.reason, err, output)
			}
			if tc.reason != "changes=1 seeds=1" {
				if fileContents(t, config) != before {
					t.Fatal("guard failed to prevent edit")
				}
				return
			}
			ed, err := editor.NewAdditionalSizing(config)
			if err != nil || len(ed.Entries()) != 1 || ed.Entries()[0].CPU != row.SuggestedQuantity || ed.Entries()[0].ID != "kube-scheduler-kube-scheduler" || ed.Entries()[0].Memory != "" {
				t.Fatalf("invalid CPU seed: %v\n%s", err, fileContents(t, config))
			}
			output, err = experimentalOutput(t, []string{input}, "", config, Options{AllowDecrease: true})
			if err != nil || !strings.Contains(output, "NOOP kube-scheduler/kube-scheduler/cpu") {
				t.Fatalf("seed not idempotent: %v\n%s", err, output)
			}
		})
	}
}

func TestExperimentalCPURequestEvidence(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, scenario := range []string{"unknown bounds", "conflicting bound", "disjoint ranges"} {
			t.Run(scenario+map[bool]string{false: "/seed", true: "/existing"}[configured], func(t *testing.T) {
				a := additionalRow("cpu", .019, .02, .2, "20m")
				a.Cluster = "ci01-mgmt-1"
				b := a
				b.Peak, b.Suggested, b.SuggestedQuantity = number(.079), number(.08), "80m"
				b.RequestMin, b.RequestMax = nil, nil
				if scenario == "conflicting bound" {
					b.RequestMax = number(.3)
				}
				if scenario == "disjoint ranges" {
					a.RequestMin, a.RequestMax = number(.1), number(.11)
					b.RequestMin, b.RequestMax = number(.3), number(.31)
				}
				before := "defaults:\n  hypershift:\n    additionalMinimalResourceRequests: {}\n"
				if configured {
					before = additionalBaseline
				}
				config := additionalConfigFile(t, before)
				inputs := []string{inputFile(t, "a.json", inputJSON(t, a)), inputFile(t, "b.json", inputJSON(t, b))}
				output, err := experimentalOutput(t, inputs, "", config, Options{AllowDecrease: true})
				change := scenario == "unknown bounds" || (configured && scenario == "conflicting bound")
				if err != nil || strings.Contains(output, "CHANGE ") != change || (fileContents(t, config) != before) != change {
					t.Fatalf("bad request guard, expected change=%t: %v\n%s", change, err, output)
				}
			})
		}
	}
}

func TestExperimentalCPURejectsBeforeWriting(t *testing.T) {
	row := additionalRow("cpu", .079, .08, .2, "80m")
	row.Cluster = "ci01-mgmt-1"
	input := inputFile(t, "a.json", inputJSON(t, row))
	memory := row
	memory.Resource = "memory" // Invalid CPU quantity on a memory row must still fail.
	bad := inputFile(t, "z.json", inputJSON(t, memory))
	config := additionalConfigFile(t, additionalBaseline)
	template := inputFile(t, "sizing.yaml", sizingTemplate())
	for _, additional := range []bool{false, true} {
		a, b := template, ""
		if additional {
			a, b = "", config
		}
		if _, err := experimentalOutput(t, []string{input, bad}, a, b, Options{AllowDecrease: true}); err == nil {
			t.Fatal("filtered invalid second memory report")
		}
		for _, prefix := range []string{"", "ocm-arohcpprod-", "ocm-arohcpci010-", "ocm-arohcpci01-.*"} {
			if err := RunExperimentalHCPCPUInputs(context.Background(), logr.Discard(), []string{input}, a, b, prefix, Options{AllowDecrease: true}); err == nil {
				t.Fatalf("accepted non-CI prefix %q", prefix)
			}
		}
	}
	if fileContents(t, config) != additionalBaseline || fileContents(t, template) != sizingTemplate() {
		t.Fatal("invalid input wrote files")
	}
}

func TestInvalidResourceOverride(t *testing.T) {
	const route = "openshift-route-controller-manager"
	row := additionalRow("cpu", .009, .01, .1, "10m")
	row.Cluster, row.Workload, row.Container = "ci01-mgmt-1", route, route
	input := inputFile(t, "input.json", inputJSON(t, row))
	for _, dry := range []bool{false, true} {
		before := "defaults:\n  hypershift:\n    additionalMinimalResourceRequests: {}\n"
		config := additionalConfigFile(t, before)
		output, err := experimentalOutput(t, []string{input}, "", config, Options{AllowDecrease: true, DryRun: dry})
		if err != nil || !strings.Contains(output, "SKIP "+route+"/"+route+"/cpu: invalid resource request override annotation key") || !strings.Contains(output, "63 bytes") || !strings.Contains(output, "changes=0 seeds=0") || strings.Contains(output, "CHANGE ") {
			t.Fatalf("invalid target not skipped before planning: %v\n%s", err, output)
		}
		if fileContents(t, config) != before {
			t.Fatal("invalid target was inserted")
		}
		before = strings.ReplaceAll(additionalBaseline, "kube-scheduler", route)
		config = additionalConfigFile(t, before)
		for _, experimental := range []bool{false, true} {
			opts := Options{AllowDecrease: true, DryRun: dry}
			if experimental {
				_, err = experimentalOutput(t, []string{input}, "", config, opts)
			} else {
				err = RunAdditionalSizingInputs(context.Background(), logr.Discard(), []string{input}, config, sizingPrefix, opts)
			}
			if err == nil || !strings.Contains(err.Error(), "invalid resource request override annotation key") {
				t.Fatalf("configured invalid target must fail (experimental=%t): %v", experimental, err)
			}
			if fileContents(t, config) != before {
				t.Fatal("invalid configured target changed file")
			}
		}
	}
}

func TestExperimentalCPUIdentityConflicts(t *testing.T) {
	row := additionalRow("cpu", .079, .08, .2, "80m")
	row.Cluster = "ci01-mgmt-1"
	input := inputFile(t, "input.json", inputJSON(t, row))
	for _, before := range []string{
		strings.NewReplacer("scheduler:", "kube-scheduler-kube-scheduler:", "deploymentName: kube-scheduler", "deploymentName: capi-provider", "containerName: kube-scheduler", "containerName: manager").Replace(additionalBaseline),
		strings.Replace(additionalBaseline, "additionalMinimalResourceRequests: {}", "additionalMinimalResourceRequests:\n          scheduler:\n            deploymentName: capi-provider\n            containerName: manager", 1),
	} {
		for _, dry := range []bool{false, true} {
			config := additionalConfigFile(t, before)
			if _, err := experimentalOutput(t, []string{input}, "", config, Options{AllowDecrease: true, DryRun: dry}); err == nil {
				t.Fatal("accepted ID collision or inherited identity conflict")
			}
			if fileContents(t, config) != before {
				t.Fatal("conflict wrote config")
			}
		}
	}
}

func TestExperimentalOriginalCPURequiresManagementIdentity(t *testing.T) {
	for _, cluster := range []string{"unknown-management", "ci01-svc", "ci01-mgmt-1-other"} {
		row := sizingRow("cpu", .079, .08, .2, "80m")
		row.Cluster = cluster
		template := inputFile(t, "sizing.yaml", sizingTemplate())
		input := inputFile(t, "input.json", inputJSON(t, row))
		output, err := experimentalOutput(t, []string{input}, template, "", Options{AllowDecrease: true})
		if err != nil || !strings.Contains(output, "unknown-identities=1") || fileContents(t, template) != sizingTemplate() {
			t.Fatalf("accepted unknown/wrong management identity: %v\n%s", err, output)
		}
	}
}
