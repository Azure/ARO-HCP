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
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

const sizingPrefix = "ocm-arohcpci01-"

// Synthetic Helm source, including the same class in the unrestricted branch.
func sizingTemplate() string {
	var requests strings.Builder
	for _, name := range []string{"kube-apiserver", "openshift-controller-manager", "cluster-policy-controller", "kube-controller-manager", "openshift-apiserver", "etcd", "ovnkube-control-plane"} {
		fmt.Fprintf(&requests, "          - containerName: %s\n            deploymentName: %s\n            memory: 200Mi # %s\n            cpu: 200m # %s\n", name, name, name, name)
	}
	branch := "apiVersion: scheduling.hypershift.openshift.io/v1alpha1\nkind: ClusterSizingConfiguration\nmetadata:\n  name: cluster\nspec:\n  sizes:\n    - criteria:\n        from: 0\n      effects:\n        resourceRequests:\n" + requests.String() + "      name: e2e_minimal\n"
	other := "    - criteria:\n        from: 1\n      effects:\n        resourceRequests:\n          - containerName: kube-apiserver\n            deploymentName: kube-apiserver\n            cpu: 900m\n            memory: 900Mi\n      name: small\n"
	return "---\n{{ if .Values.limitClusterSizes }}\n" + branch + other + "{{ else }}\n" + branch + other + "{{ end }}\n"
}

func sizingRow(resource string, peak, suggested, current float64, quantity string) inputRecommendation {
	row := inputRow(resource, peak, suggested, current, quantity)
	row.Cluster, row.Namespace = "arbitrary-management-name", sizingPrefix+"first-hcp"
	row.Workload, row.Container = "kube-apiserver", "kube-apiserver"
	return row
}

func runSizingOutput(t *testing.T, input, template string, opts Options) string {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	old := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = old }()
	if err := RunSizingInput(context.Background(), logr.Discard(), input, template, sizingPrefix, opts); err != nil {
		t.Fatal(err)
	}
	return fileContents(t, out.Name())
}

func TestRunSizingInputMaxScopeAndIdempotence(t *testing.T) {
	large := sizingRow("cpu", .401, .49, .2, "490m")
	small := sizingRow("cpu", .2, .24, .2, "240m")
	small.Namespace, small.Cluster = sizingPrefix+"second-hcp", "anything-goes"
	etcd := sizingRow("memory", 200.1*(1<<20), 250*(1<<20), 200*(1<<20), "250Mi")
	etcd.Kind, etcd.Workload, etcd.Container = "StatefulSet", "etcd", "etcd"
	for _, rows := range [][]inputRecommendation{{large, small, etcd}, {etcd, small, large}} {
		before := sizingTemplate()
		template := inputFile(t, "sizing.yaml", before)
		input := inputFile(t, "input.json", inputJSON(t, rows...))
		opts := Options{ConfigPath: "/must-not-be-read/config.yaml", SourcePrefix: "defaults"}
		output := runSizingOutput(t, input, template, opts)
		want := strings.Replace(before, "cpu: 200m # kube-apiserver", "cpu: 490m # kube-apiserver", 1)
		want = strings.Replace(want, "memory: 200Mi # etcd", "memory: 250Mi # etcd", 1)
		if got := fileContents(t, template); got != want {
			t.Fatalf("wrong max, StatefulSet mapping or edits outside selected class/branch:\n%s", got)
		}
		for _, warning := range []string{"user assertion only", "does not prove size class", "Use only an e2e_minimal sample", "Pod limits were not checked", "No limits will be changed"} {
			if !strings.Contains(output, warning) {
				t.Fatalf("missing %q: %s", warning, output)
			}
		}
		output = runSizingOutput(t, input, template, opts)
		if strings.Count(output, "NOOP") != 2 || strings.Contains(output, "stale") || fileContents(t, template) != want {
			t.Fatalf("expected idempotent noops: %s", output)
		}
	}
}

func TestRunSizingInputScopeAndUnknownMappings(t *testing.T) {
	good := sizingRow("cpu", .2, .24, .2, "240m")
	rows := []inputRecommendation{good}
	for _, ns := range []string{"ocm-arohcpprod-hcp", "ocm-arohcpci010-hcp", "ocm-arohcpci01", sizingPrefix, "x" + sizingPrefix + "hcp"} {
		bad := good
		bad.Namespace, bad.Eligible = ns, false
		rows = append(rows, bad)
	}
	for _, mutate := range []func(*inputRecommendation){
		func(r *inputRecommendation) { r.Container = "other-container" },
		func(r *inputRecommendation) { r.Workload = "other-workload" },
		func(r *inputRecommendation) { r.Workload = "other-workload"; r.Kind = "StatefulSet" },
		func(r *inputRecommendation) { r.Container = "other-container"; r.Kind = "Pod" },
		func(r *inputRecommendation) { r.Workload = "kube-scheduler"; r.Container = "kube-scheduler" },
	} {
		bad := good
		bad.Eligible = false
		mutate(&bad)
		rows = append(rows, bad)
	}
	before := sizingTemplate()
	template := inputFile(t, "sizing.yaml", before)
	output := runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, rows...)), template, Options{})
	want := strings.Replace(before, "cpu: 200m # kube-apiserver", "cpu: 240m # kube-apiserver", 1)
	if fileContents(t, template) != want || !strings.Contains(output, "SKIP unknown sizing mappings: 5") {
		t.Fatalf("incorrect scope/mapping: %s", output)
	}
}

func TestRunSizingInputBlocksEntireResource(t *testing.T) {
	for name, mutate := range map[string]func(*inputRecommendation){
		"ineligible":        func(r *inputRecommendation) { r.Eligible = false },
		"short sample":      func(r *inputRecommendation) { r.Eligible = false; r.Warnings = []string{"short sample"} },
		"init":              func(r *inputRecommendation) { r.InitContainer = true },
		"wrong kind":        func(r *inputRecommendation) { r.Kind = "StatefulSet" },
		"unknown kind":      func(r *inputRecommendation) { r.Kind = "CustomController" },
		"unknown workload":  func(r *inputRecommendation) { r.Workload = "unknown" },
		"missing workload":  func(r *inputRecommendation) { r.Workload = "" },
		"pod":               func(r *inputRecommendation) { r.Kind = "Pod"; r.Workload = "random-pod-name" },
		"unresolved owner":  func(r *inputRecommendation) { r.Kind = "Unknown"; r.Workload = "random-name" },
		"missing peak":      func(r *inputRecommendation) { r.Peak = nil },
		"missing suggested": func(r *inputRecommendation) { r.Suggested = nil; r.SuggestedQuantity = "" },
		"missing min":       func(r *inputRecommendation) { r.RequestMin = nil },
		"missing max":       func(r *inputRecommendation) { r.RequestMax = nil },
		"missing replicas":  func(r *inputRecommendation) { r.MeasuredReplicas = 1 },
		"zero replicas":     func(r *inputRecommendation) { r.Replicas = 0; r.MeasuredReplicas = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			good := sizingRow("cpu", .2, .24, .2, "240m")
			bad := sizingRow("cpu", .4, .48, .2, "480m")
			bad.Cluster, bad.Namespace = "unrelated-cluster-name", sizingPrefix+"second-hcp"
			mutate(&bad)
			memory := sizingRow("memory", 200*(1<<20), 240*(1<<20), 200*(1<<20), "240Mi")
			for _, rows := range [][]inputRecommendation{{good, bad, memory}, {bad, memory, good}} {
				before := sizingTemplate()
				template := inputFile(t, "sizing.yaml", before)
				output := runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, rows...)), template, Options{AllowDecrease: true})
				want := strings.Replace(before, "memory: 200Mi # kube-apiserver", "memory: 240Mi # kube-apiserver", 1)
				if fileContents(t, template) != want || !strings.Contains(output, "SKIP blocked") {
					t.Fatalf("failed to block whole CPU mapping independently of memory: %s", output)
				}
				if !strings.Contains(output, "1 of 2 rows") || !strings.Contains(output, "details at verbosity 1") {
					t.Fatalf("missing aggregate evidence warning: %s", output)
				}
			}
		})
	}
}

func TestRunSizingInputKnownKinds(t *testing.T) {
	for _, name := range []string{"kube-apiserver", "openshift-controller-manager", "cluster-policy-controller", "kube-controller-manager", "openshift-apiserver", "etcd", "ovnkube-control-plane"} {
		for _, kind := range []string{"Deployment", "StatefulSet", "ReplicaSet", "DaemonSet", "Pod"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				row := sizingRow("cpu", .2, .24, .2, "240m")
				row.Kind, row.Workload, row.Container = kind, name, name
				before := sizingTemplate()
				template := inputFile(t, "sizing.yaml", before)
				runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, row)), template, Options{})
				want := before
				if (name == "etcd" && kind == "StatefulSet") || (name != "etcd" && kind == "Deployment") {
					want = strings.Replace(before, "cpu: 200m # "+name, "cpu: 240m # "+name, 1)
				}
				if fileContents(t, template) != want {
					t.Fatal("incorrect controller-kind mapping")
				}
			})
		}
	}
}

func TestRunSizingInputStaleAndDryRun(t *testing.T) {
	for _, tc := range []struct {
		name, current, reason string
		allow, dry            bool
	}{
		{name: "gap", current: "200m", reason: "WARNING stale"},
		{name: "recent bump", current: "500m", reason: "WARNING stale", allow: true},
		{name: "first range", current: "110m", reason: "CHANGE"},
		{name: "second range", current: "310m", reason: "requires --allow-decrease"},
		{name: "decrease", current: "310m", reason: "CHANGE", allow: true},
		{name: "equal outside ranges", current: "240m", reason: "NOOP"},
		{name: "dry run", current: "100m", reason: "CHANGE", dry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := sizingRow("cpu", .2, .24, .1, "240m")
			a.RequestMax = number(.11)
			b := a
			b.Namespace, b.RequestMin, b.RequestMax = sizingPrefix+"second-hcp", number(.3), number(.31)
			before := strings.Replace(sizingTemplate(), "cpu: 200m # kube-apiserver", "cpu: "+tc.current+" # kube-apiserver", 1)
			template := inputFile(t, "sizing.yaml", before)
			output := runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, a, b)), template, Options{AllowDecrease: tc.allow, DryRun: tc.dry})
			want := before
			if tc.reason == "CHANGE" && !tc.dry {
				want = strings.Replace(before, "cpu: "+tc.current+" # kube-apiserver", "cpu: 240m # kube-apiserver", 1)
			}
			if !strings.Contains(output, tc.reason) || fileContents(t, template) != want {
				t.Fatalf("expected %q and appropriate edit: %s", tc.reason, output)
			}
			if tc.dry && (!strings.Contains(output, "mode=dry-run") || !strings.Contains(output, "Pod limits were not checked")) {
				t.Fatalf("missing dry-run warning: %s", output)
			}
		})
	}
}

func TestRunSizingInputDeadband(t *testing.T) {
	for _, tc := range []struct {
		name, quantity, reason     string
		peak, suggested, threshold float64
		allow, risk                bool
	}{
		{name: "inclusive increase", peak: .183, suggested: .22, quantity: "220m", threshold: .1, reason: "SKIP deadband"},
		{name: "increase", peak: .192, suggested: .24, quantity: "240m", threshold: .1, reason: "CHANGE"},
		{name: "inclusive decrease", peak: .15, suggested: .18, quantity: "180m", threshold: .1, allow: true, reason: "SKIP deadband"},
		{name: "decrease", peak: .14, suggested: .17, quantity: "170m", threshold: .1, allow: true, reason: "CHANGE"},
		{name: "decrease permission", peak: .14, suggested: .17, quantity: "170m", threshold: .1, reason: "requires --allow-decrease"},
		{name: "zero disables", peak: .175, suggested: .21, quantity: "210m", reason: "CHANGE"},
		{name: "risk bypass", peak: .241, suggested: .29, quantity: "290m", threshold: 1, reason: "CHANGE"},
		{name: "risk equality", peak: .24, suggested: .29, quantity: "290m", threshold: 1, reason: "SKIP deadband", risk: true},
		{name: "ignore producer risk", peak: .183, suggested: .22, quantity: "220m", threshold: .1, reason: "SKIP deadband", risk: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := sizingRow("cpu", tc.peak, tc.suggested, .2, tc.quantity)
			row.AlertRisk, row.RequestMin = tc.risk, number(.1)
			before := sizingTemplate()
			template := inputFile(t, "sizing.yaml", before)
			output := runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, row)), template, Options{ChangeThreshold: tc.threshold, AllowDecrease: tc.allow})
			want := before
			if tc.reason == "CHANGE" {
				want = strings.Replace(before, "cpu: 200m # kube-apiserver", "cpu: "+tc.quantity+" # kube-apiserver", 1)
			}
			if !strings.Contains(output, tc.reason) || fileContents(t, template) != want {
				t.Fatalf("expected %q: %s", tc.reason, output)
			}
		})
	}
	// The maximum peak matters even when suggestions tie across HCPs.
	a := sizingRow("cpu", .24, .29, .2, "290m")
	b := sizingRow("cpu", .241, .29, .2, "290m")
	b.Namespace = sizingPrefix + "second-hcp"
	for _, rows := range [][]inputRecommendation{{a, b}, {b, a}} {
		template := inputFile(t, "sizing.yaml", sizingTemplate())
		output := runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, rows...)), template, Options{ChangeThreshold: 1})
		if !strings.Contains(output, "CHANGE") {
			t.Fatalf("tied suggestion peak did not bypass deadband: %s", output)
		}
	}
}

func TestRunSizingInputRejectsBeforeWrites(t *testing.T) {
	before := sizingTemplate()
	template := inputFile(t, "sizing.yaml", before)
	valid := inputJSON(t, sizingRow("cpu", .2, .24, .2, "240m"))
	input := inputFile(t, "input.json", valid)
	for _, prefix := range []string{"", "ocm-", "ocm--", "arohcpci01-", "ocm-arohcpci01", "ocm-Upper-", "ocm-a.b-", "ocm-.*-", "ocm-a_-", "ocm-a-\n"} {
		if err := RunSizingInput(context.Background(), logr.Discard(), input, template, prefix, Options{}); err == nil {
			t.Errorf("accepted invalid namespace prefix %q", prefix)
		}
	}
	for _, opts := range []Options{
		{Commit: true}, {RenderCmd: "false"}, {GrafanaURL: "https://example.com"},
		{SourcePrefix: "clouds.public.defaults"}, {WritePath: template}, {WritePrefix: "defaults"}, {WritePrefix: "clouds.dev.defaults"},
		{ChangeThreshold: -.1}, {ChangeThreshold: 1.1}, {ChangeThreshold: math.NaN()}, {ChangeThreshold: math.Inf(1)},
	} {
		if err := RunSizingInput(context.Background(), logr.Discard(), input, template, sizingPrefix, opts); err == nil {
			t.Errorf("accepted unsafe options: %+v", opts)
		}
	}
	for _, invalid := range []string{
		strings.Replace(valid, `"version":2`, `"version":2,"extra":true`, 1),
		strings.Replace(valid, `"version":2`, `"version":1`, 1),
		strings.Replace(valid, `"eligible":true`, `"eligible":true,"eligible":false`, 1),
		valid + `{}`,
		inputJSON(t, sizingRow("cpu", .2, .24, .2, "240m"), sizingRow("memory", 200, 300, 100, "300Mi")),
	} {
		if err := RunSizingInput(context.Background(), logr.Discard(), inputFile(t, "bad.json", invalid), template, sizingPrefix, Options{}); err == nil {
			t.Error("accepted invalid report")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunSizingInput(ctx, logr.Discard(), input, template, sizingPrefix, Options{}); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if fileContents(t, template) != before {
		t.Fatal("validation error partially modified template")
	}
	malformed := inputFile(t, "malformed.yaml", "not a sizing template\n")
	if err := RunSizingInput(context.Background(), logr.Discard(), input, malformed, sizingPrefix, Options{}); err == nil {
		t.Fatal("accepted malformed template")
	}
	if fileContents(t, malformed) != "not a sizing template\n" {
		t.Fatal("modified malformed template")
	}
}

func TestRunSizingInputOnlyExistingEntriesAndResources(t *testing.T) {
	for _, tc := range []struct {
		name, before, reason string
	}{
		{
			name:   "absent CPU scalar",
			before: strings.Replace(sizingTemplate(), "            cpu: 200m # kube-apiserver\n", "", 1),
			reason: "effective current request is missing or nonnumeric",
		},
		{
			name:   "absent entry",
			before: strings.Replace(sizingTemplate(), "          - containerName: kube-apiserver\n            deploymentName: kube-apiserver\n            memory: 200Mi # kube-apiserver\n            cpu: 200m # kube-apiserver\n", "", 1),
			reason: "SKIP unknown sizing mappings: 1",
		},
		{
			name:   "no workload aliases",
			before: strings.Replace(sizingTemplate(), "deploymentName: kube-apiserver", "deploymentName: renamed-apiserver", 1),
			reason: "SKIP unknown sizing mappings: 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			template := inputFile(t, "sizing.yaml", tc.before)
			input := inputFile(t, "input.json", inputJSON(t, sizingRow("cpu", .2, .24, .2, "240m")))
			output := runSizingOutput(t, input, template, Options{})
			if fileContents(t, template) != tc.before || !strings.Contains(output, tc.reason) {
				t.Fatalf("inserted or inferred a missing target: %s", output)
			}
		})
	}
}

func TestRunSizingInputPerScalarStaleness(t *testing.T) {
	before := sizingTemplate()
	template := inputFile(t, "sizing.yaml", before)
	cpu := sizingRow("cpu", .2, .24, .2, "240m")
	memory := sizingRow("memory", 200*(1<<20), 240*(1<<20), 100*(1<<20), "240Mi")
	output := runSizingOutput(t, inputFile(t, "input.json", inputJSON(t, cpu, memory)), template, Options{})
	want := strings.Replace(before, "cpu: 200m # kube-apiserver", "cpu: 240m # kube-apiserver", 1)
	if fileContents(t, template) != want || !strings.Contains(output, "WARNING stale: SKIP kube-apiserver/kube-apiserver/memory") {
		t.Fatalf("staleness must be checked per scalar: %s", output)
	}
}
