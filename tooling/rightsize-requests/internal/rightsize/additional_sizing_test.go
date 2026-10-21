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
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/editor"
	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

const additionalBaseline = `defaults:
  unrelated: '{{ .ctx.region }}'
  hypershift:
    additionalMinimalResourceRequests:
      scheduler:
        deploymentName: kube-scheduler
        containerName: kube-scheduler
        cpu: 200m
        memory: 200Mi
clouds:
  dev:
    defaults:
      hypershift:
        additionalMinimalResourceRequests: {}
`

func additionalConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"config", "hypershiftoperator/deploy"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := os.ReadFile("../../../../hypershiftoperator/deploy/regular-resource-targets.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config", "config.yaml")
	for file, data := range map[string][]byte{path: []byte(content), filepath.Join(dir, "hypershiftoperator/deploy/regular-resource-targets.yaml"): catalog} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func additionalRow(resource string, peak, suggested, current float64, quantity string) inputRecommendation {
	row := sizingRow(resource, peak, suggested, current, quantity)
	row.Workload, row.Container = "kube-scheduler", "kube-scheduler"
	return row
}

func runAdditionalOutput(t *testing.T, inputs []string, config string, opts Options) string {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old }()
	if err := RunAdditionalSizingInputs(context.Background(), logr.Discard(), inputs, config, sizingPrefix, opts); err != nil {
		t.Fatal(err)
	}
	return fileContents(t, out.Name())
}

func TestAdditionalSizingDataset(t *testing.T) {
	quiet := additionalRow("cpu", .119, .12, .2, "120m")
	busy := additionalRow("cpu", .489, .49, .2, "490m")
	busy.Cluster, busy.Namespace = "other-management-cluster", sizingPrefix+"second"
	etcd := additionalRow("memory", 249*(1<<20), 250*(1<<20), 200*(1<<20), "250Mi")
	etcd.Kind, etcd.Workload, etcd.Container = "StatefulSet", "etcd", "etcd-metrics"
	before := strings.Replace(additionalBaseline, "clouds:\n", "      metrics:\n        deploymentName: etcd\n        containerName: etcd-metrics\n        memory: 200Mi\nclouds:\n", 1)
	for _, reverse := range []bool{false, true} {
		config := additionalConfigFile(t, before)
		a := inputFile(t, "quiet.json", inputJSON(t, quiet, etcd))
		b := inputFile(t, "busy.json", inputJSON(t, busy, etcd))
		inputs := []string{a, b}
		if reverse {
			inputs = []string{b, a}
		}
		opts := Options{AllowDecrease: true, ConfigPath: "/must-not-read", SourcePrefix: "defaults"}
		output := runAdditionalOutput(t, inputs, config, opts)
		ed, err := editor.NewAdditionalSizing(config)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range ed.Entries() {
			if entry.ID == "metrics" && entry.Memory != "250Mi" || entry.ID == "scheduler" && entry.CPU != "490m" {
				t.Fatalf("wrong dataset max or etcd kind: %+v", ed.Entries())
			}
		}
		got := fileContents(t, config)
		if !strings.HasPrefix(got, strings.Split(before, "clouds:\n")[0]) || strings.Contains(got, "resources:") || strings.Contains(got, "120m") {
			t.Fatalf("changed defaults or wrote wrong schema: %s", got)
		}
		for _, text := range []string{"SOURCE", "sha256=", "reports=2", "user assertion only", "Pod limits were not checked", "governing source"} {
			if !strings.Contains(output, text) {
				t.Fatalf("missing %q: %s", text, output)
			}
		}
		output = runAdditionalOutput(t, inputs, config, opts)
		if strings.Count(output, "NOOP") != 2 || fileContents(t, config) != got {
			t.Fatalf("not idempotent: %s", output)
		}
	}
}

func TestAdditionalSizingBlocksResourceAcrossReports(t *testing.T) {
	for name, mutate := range map[string]func(*inputRecommendation){
		"ineligible":       func(r *inputRecommendation) { r.Eligible = false },
		"init":             func(r *inputRecommendation) { r.InitContainer = true },
		"wrong kind":       func(r *inputRecommendation) { r.Kind = "StatefulSet" },
		"unknown kind":     func(r *inputRecommendation) { r.Kind = "CustomController" },
		"unknown workload": func(r *inputRecommendation) { r.Workload = "unknown" },
		"missing workload": func(r *inputRecommendation) { r.Workload = "" },
		"pod":              func(r *inputRecommendation) { r.Kind = "Pod"; r.Workload = "unresolved-pod" },
		"peak":             func(r *inputRecommendation) { r.Peak = nil },
		"suggested":        func(r *inputRecommendation) { r.Suggested = nil; r.SuggestedQuantity = "" },
		"minimum":          func(r *inputRecommendation) { r.RequestMin = nil },
		"maximum":          func(r *inputRecommendation) { r.RequestMax = nil },
		"replicas":         func(r *inputRecommendation) { r.MeasuredReplicas = 0 },
		"zero replicas":    func(r *inputRecommendation) { r.Replicas = 0; r.MeasuredReplicas = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			good := additionalRow("cpu", .239, .24, .2, "240m")
			bad := good
			bad.Namespace = sizingPrefix + "second"
			mutate(&bad)
			memory := additionalRow("memory", 239*(1<<20), 240*(1<<20), 200*(1<<20), "240Mi")
			inputs := []string{inputFile(t, "a.json", inputJSON(t, good, memory)), inputFile(t, "b.json", inputJSON(t, bad))}
			config := additionalConfigFile(t, additionalBaseline)
			output := runAdditionalOutput(t, inputs, config, Options{AllowDecrease: true})
			ed, err := editor.NewAdditionalSizing(config)
			if err != nil {
				t.Fatal(err)
			}
			if got := ed.Entries()[0]; got.CPU != "200m" || got.Memory != "240Mi" || !strings.Contains(output, "SKIP blocked") {
				t.Fatalf("failed to block whole CPU independently of memory: %+v %s", got, output)
			}
		})
	}
}

func TestAdditionalSizingScope(t *testing.T) {
	good := additionalRow("cpu", .239, .24, .2, "240m")
	rows := []inputRecommendation{good}
	for _, ns := range []string{"ocm-arohcpprod-hcp", "ocm-arohcpci010-hcp", sizingPrefix, "x" + sizingPrefix + "hcp"} {
		bad := good
		bad.Namespace, bad.Eligible = ns, false
		rows = append(rows, bad)
	}
	for _, mutate := range []func(*inputRecommendation){
		func(r *inputRecommendation) { r.Container = "other" },
		func(r *inputRecommendation) { r.Workload = "other" },
		func(r *inputRecommendation) { r.Workload = "other"; r.Kind = "StatefulSet" },
		func(r *inputRecommendation) { r.Workload = "kube-apiserver"; r.Container = "kube-apiserver" },
	} {
		bad := good
		bad.Eligible = false
		mutate(&bad)
		rows = append(rows, bad)
	}
	config := additionalConfigFile(t, additionalBaseline)
	output := runAdditionalOutput(t, []string{inputFile(t, "input.json", inputJSON(t, rows...))}, config, Options{})
	if !strings.Contains(output, "CHANGE scheduler/cpu") || !strings.Contains(output, "unknown additional sizing mappings: 4") {
		t.Fatalf("wrong namespace or workload scope: %s", output)
	}
}

func TestAdditionalSizingDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, current, reason string
		peak, suggested       float64
		quantity              string
		opts                  Options
		missingReport         bool
	}{
		{name: "gap", current: "200m", peak: .239, suggested: .24, quantity: "240m", reason: "WARNING stale"},
		{name: "first range", current: "110m", peak: .239, suggested: .24, quantity: "240m", reason: "CHANGE"},
		{name: "decrease permission", current: "310m", peak: .239, suggested: .24, quantity: "240m", reason: "requires --allow-decrease"},
		{name: "decrease", current: "310m", peak: .239, suggested: .24, quantity: "240m", reason: "CHANGE", opts: Options{AllowDecrease: true}},
		{name: "incomplete decrease", current: "310m", peak: .239, suggested: .24, quantity: "240m", reason: "incomplete dataset", opts: Options{AllowDecrease: true}, missingReport: true},
		{name: "incomplete increase", current: "110m", peak: .239, suggested: .24, quantity: "240m", reason: "CHANGE", missingReport: true},
		{name: "equal stale", current: "240m", peak: .239, suggested: .24, quantity: "240m", reason: "NOOP"},
		{name: "dry run", current: "110m", peak: .239, suggested: .24, quantity: "240m", reason: "CHANGE", opts: Options{DryRun: true}},
		{name: "deadband boundary", current: "100m", peak: .109, suggested: .11, quantity: "110m", reason: "SKIP deadband", opts: Options{ChangeThreshold: .1}},
		{name: "risk bypass", current: "100m", peak: .121, suggested: .13, quantity: "130m", reason: "CHANGE", opts: Options{ChangeThreshold: 1}},
		{name: "risk equality", current: "100m", peak: .12, suggested: .12, quantity: "120m", reason: "SKIP deadband", opts: Options{ChangeThreshold: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := additionalRow("cpu", tc.peak, tc.suggested, .1, tc.quantity)
			a.RequestMax = number(.11)
			b := a
			b.Namespace, b.RequestMin, b.RequestMax = sizingPrefix+"second", number(.3), number(.31)
			inputs := []string{inputFile(t, "a.json", inputJSON(t, a, b))}
			if tc.missingReport {
				inputs = append(inputs, inputFile(t, "missing.json", inputJSON(t)))
			}
			before := strings.Replace(additionalBaseline, "cpu: 200m", "cpu: "+tc.current, 1)
			config := additionalConfigFile(t, before)
			output := runAdditionalOutput(t, inputs, config, tc.opts)
			if !strings.Contains(output, tc.reason) {
				t.Fatalf("expected %q: %s", tc.reason, output)
			}
			if tc.reason != "CHANGE" || tc.opts.DryRun {
				if fileContents(t, config) != before {
					t.Fatal("unexpected config edit")
				}
			} else {
				ed, err := editor.NewAdditionalSizing(config)
				if err != nil || ed.Entries()[0].CPU != tc.quantity {
					t.Fatalf("missing expected update: %v", err)
				}
			}
		})
	}
}

func TestAdditionalSizingNoUnconfiguredKnobs(t *testing.T) {
	for _, tc := range []struct{ before, reason string }{
		{"defaults:\n  hypershift:\n    additionalMinimalResourceRequests: {}\n", "no configured additional HCP targets"},
		{strings.Replace(additionalBaseline, "        cpu: 200m\n", "", 1), "effective current request is missing"},
	} {
		config := additionalConfigFile(t, tc.before)
		input := inputFile(t, "input.json", inputJSON(t, additionalRow("cpu", .239, .24, .2, "240m")))
		output := runAdditionalOutput(t, []string{input}, config, Options{})
		if !strings.Contains(output, tc.reason) || fileContents(t, config) != tc.before {
			t.Fatalf("invented baseline knob: %s", output)
		}
	}
}

func TestAdditionalSizingRejectsBeforeWrites(t *testing.T) {
	config := additionalConfigFile(t, additionalBaseline)
	input := inputFile(t, "input.json", inputJSON(t, additionalRow("cpu", .239, .24, .2, "240m")))
	for _, prefix := range []string{"", "ocm-", "ocm-a.*-", "ocm-a-\n"} {
		if err := RunAdditionalSizingInputs(context.Background(), logr.Discard(), []string{input}, config, prefix, Options{}); err == nil {
			t.Fatalf("accepted invalid prefix %q", prefix)
		}
	}
	for _, opts := range []Options{
		{WritePath: config}, {WritePrefix: "clouds.dev.defaults"}, {SourcePrefix: "clouds.public.defaults"},
		{Commit: true}, {RenderCmd: "false"}, {GrafanaURL: "https://example.com"},
		{ChangeThreshold: -.1}, {ChangeThreshold: 1.1}, {ChangeThreshold: math.NaN()}, {ChangeThreshold: math.Inf(1)},
	} {
		if err := RunAdditionalSizingInputs(context.Background(), logr.Discard(), []string{input}, config, sizingPrefix, opts); err == nil {
			t.Fatalf("accepted unsafe options %+v", opts)
		}
	}
	bad := inputFile(t, "bad.json", strings.Replace(fileContents(t, input), `"version":3`, `"version":1`, 1))
	if err := RunAdditionalSizingInputs(context.Background(), logr.Discard(), []string{input, bad}, config, sizingPrefix, Options{}); err == nil {
		t.Fatal("accepted invalid second report")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunAdditionalSizingInputs(ctx, logr.Discard(), []string{input}, config, sizingPrefix, Options{}); err != context.Canceled {
		t.Fatalf("expected cancellation: %v", err)
	}
	if fileContents(t, config) != additionalBaseline {
		t.Fatal("validation failure wrote config")
	}
	for _, target := range append(targets.MinimalTargets(), targets.MinimalTarget{Workload: "fake", Container: "fake"}) {
		before := strings.ReplaceAll(additionalBaseline, "kube-scheduler", target.Workload)
		config := additionalConfigFile(t, before)
		if err := RunAdditionalSizingInputs(context.Background(), logr.Discard(), []string{input}, config, sizingPrefix, Options{}); err == nil || !strings.Contains(err.Error(), "unsupported additional regular HCP target") {
			t.Fatalf("accepted unknown or original-seven target %+v: %v", target, err)
		}
		if fileContents(t, config) != before {
			t.Fatal("target validation failure wrote config")
		}
	}
	config = inputFile(t, "config.yaml", additionalBaseline)
	if err := RunAdditionalSizingInputs(context.Background(), logr.Discard(), []string{input}, config, sizingPrefix, Options{}); err == nil || !strings.Contains(err.Error(), "read regular HCP targets") {
		t.Fatalf("catalog path fell back to runtime repo: %v", err)
	}
}

func TestAdditionalSizingRealConfigDefaultIsEmpty(t *testing.T) {
	data, err := os.ReadFile("../../../../config/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Defaults struct {
			Hypershift struct {
				AdditionalMinimalResourceRequests map[string]any `yaml:"additionalMinimalResourceRequests"`
			}
		}
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Defaults.Hypershift.AdditionalMinimalResourceRequests) != 0 {
		t.Fatal("global defaults must not contain experimental HCP requests")
	}
}
