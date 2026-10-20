// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package main

import (
	"bytes"
	"encoding/gob"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"

	"github.com/Azure/ARO-HCP/hack/verify-controller-watches/analyzer"
)

func TestRealLibraryRegistrationAndInjection(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := packages.Load(&packages.Config{Dir: root, Mode: packages.LoadSyntax},
		"./hack/verify-controller-watches/testdata/controllers", "./hack/verify-controller-watches/testdata/dispatch", "./internal/...", "./backend/pkg/...", "./fleet/pkg/...")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) != 0 {
		t.Fatal("fixture or real libraries no longer type-check")
	}
	const fixture = "github.com/Azure/ARO-HCP/hack/verify-controller-watches/testdata/controllers"
	checker := analyzer.NewWithEntrypoints([]string{fixture + ".Bootstrap"})
	// Exercise fact serialization with actual library summaries. No synthetic
	// ASTs, private evaluator calls, or copied library interfaces are involved.
	check := checker.Run
	checker.Run = func(pass *analysis.Pass) (any, error) {
		export := pass.ExportPackageFact
		pass.ExportPackageFact = func(fact analysis.Fact) {
			var data bytes.Buffer
			if err := gob.NewEncoder(&data).Encode(fact); err != nil {
				t.Fatal(err)
			}
			var decoded analyzer.PackageFact
			if err := gob.NewDecoder(&data).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			export(&decoded)
		}
		return check(pass)
	}
	result, err := runAnalyzer(pkgs, checker)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Gaps) != 0 {
		t.Fatalf("fixture coverage must be complete: %+v", result.Gaps)
	}
	const core = "github.com/Azure/ARO-HCP/internal/api/coreapi."
	const fleet = "github.com/Azure/ARO-HCP/internal/api/fleetapi."
	const kubeapplier = "github.com/Azure/ARO-HCP/internal/api/kubeapplierapi."
	want := []analyzer.Inventory{
		{Controller: "hack/filter-desires", Accessed: []string{kubeapplier + "ReadDesire"}, Watched: []string{kubeapplier + "ReadDesire"}},
		{Controller: "hack/filter-unconfigured", Accessed: []string{core + "Cluster"}, Watched: []string{core + "Cluster", core + "ServiceProviderCluster"}},
		{Controller: "hack/filter-cluster", Accessed: []string{core + "Cluster"}, Watched: []string{core + "Cluster"}},
		{Controller: "hack/filter-provider", Accessed: []string{core + "Cluster"}, Watched: []string{core + "ServiceProviderCluster"}},
		{Controller: "hack/filter-custom", Accessed: []string{core + "Cluster"}, Watched: []string{core + "Cluster", core + "ServiceProviderCluster"}},
		{Controller: "hack/callbacks", Accessed: []string{core + "Cluster", core + "ExternalAuth", core + "NodePool", core + "ServiceProviderCluster", core + "Subscription"}, Watched: []string{}},
		{Controller: "hack/constant-keys", Accessed: []string{core + "ExternalAuth", core + "Subscription"}, Watched: []string{}},
		{Controller: "hack/cooldown-missing", Accessed: []string{fleet + "ManagementCluster", fleet + "Stamp"}, Watched: []string{fleet + "Stamp"}},
		{Controller: "hack/cooldown-watched", Accessed: []string{fleet + "ManagementCluster", fleet + "Stamp"}, Watched: []string{fleet + "ManagementCluster", fleet + "Stamp"}},
		{Controller: "hack/first", Accessed: []string{core + "Cluster", core + "NodePool", core + "Subscription"}, Watched: []string{core + "Cluster", core + "ServiceProviderCluster"}},
		{Controller: "hack/indexed", Accessed: []string{core + "Cluster"}, Watched: []string{core + "Cluster"}},
		{Controller: "hack/inherited", Accessed: []string{core + "Cluster", core + "ExternalAuth", core + "NodePool"}, Watched: []string{core + "Cluster", core + "ServiceProviderCluster"}},
		{Controller: "hack/operations", Accessed: []string{core + "Controller", core + "Subscription"}, Watched: []string{}},
		{Controller: "hack/optional-nil", Accessed: []string{core + "ServiceProviderCluster"}, Watched: []string{}},
		{Controller: "hack/optional-watch", Accessed: []string{core + "ServiceProviderCluster"}, Watched: []string{core + "ServiceProviderCluster"}},
		{Controller: "hack/second", Accessed: []string{core + "Cluster", core + "ExternalAuth", core + "NodePool"}, Watched: []string{core + "Cluster", core + "ServiceProviderCluster"}},
		{Controller: "hack/wrapped", Accessed: []string{core + "Cluster", core + "ServiceProviderCluster"}, Watched: []string{core + "Cluster", core + "ManagementClusterContent", core + "ServiceProviderCluster"}},
		{Controller: "hack/three-callbacks", Accessed: []string{core + "Cluster", core + "ExternalAuth", core + "Subscription"}, Watched: []string{}},
		{Controller: "hack/three-implementations", Accessed: []string{core + "Cluster", core + "ExternalAuth", core + "Subscription"}, Watched: []string{}},
	}
	slices.SortFunc(want, func(a, b analyzer.Inventory) int { return strings.Compare(a.Controller, b.Controller) })
	var names []string
	for _, inventory := range want {
		names = append(names, inventory.Controller)
	}
	if !reflect.DeepEqual(names, result.Controllers) {
		t.Fatalf("wrong registry inventory: %v", result.Controllers)
	}
	if !reflect.DeepEqual(want, result.Inventories) {
		t.Fatalf("controller intent changed or analysis broke:\nwant %#v\ngot  %#v", want, result.Inventories)
	}
	wantFindings := map[string]bool{
		"hack/filter-unconfigured | unused-watch | " + core + "ServiceProviderCluster": true,
		"hack/filter-provider | unused-watch | " + core + "ServiceProviderCluster":     true,
		"hack/filter-provider | missing-watch | " + core + "Cluster":                   true,
		"hack/filter-custom | unused-watch | " + core + "ServiceProviderCluster":       true,
		"hack/constant-keys | missing-watch | " + core + "ExternalAuth":                true,
		"hack/constant-keys | missing-watch | " + core + "Subscription":                true,
		"hack/cooldown-missing | missing-watch | " + fleet + "ManagementCluster":       true,
		"hack/three-callbacks | missing-watch | " + core + "Cluster":                   true,
		"hack/three-callbacks | missing-watch | " + core + "ExternalAuth":              true,
		"hack/three-callbacks | missing-watch | " + core + "Subscription":              true,
		"hack/three-implementations | missing-watch | " + core + "Cluster":             true,
		"hack/three-implementations | missing-watch | " + core + "ExternalAuth":        true,
		"hack/three-implementations | missing-watch | " + core + "Subscription":        true,
		"hack/callbacks | missing-watch | " + core + "Cluster":                         true,
		"hack/callbacks | missing-watch | " + core + "ExternalAuth":                    true,
		"hack/callbacks | missing-watch | " + core + "NodePool":                        true,
		"hack/callbacks | missing-watch | " + core + "ServiceProviderCluster":          true,
		"hack/callbacks | missing-watch | " + core + "Subscription":                    true,
		"hack/operations | missing-watch | " + core + "Controller":                     true,
		"hack/operations | missing-watch | " + core + "Subscription":                   true,
		"hack/optional-nil | missing-watch | " + core + "ServiceProviderCluster":       true,
		"hack/inherited | missing-watch | " + core + "ExternalAuth":                    true,
		"hack/inherited | missing-watch | " + core + "NodePool":                        true,
		"hack/inherited | unused-watch | " + core + "ServiceProviderCluster":           true,
		"hack/first | missing-watch | " + core + "NodePool":                            true,
		"hack/first | missing-watch | " + core + "Subscription":                        true,
		"hack/first | unused-watch | " + core + "ServiceProviderCluster":               true,
		"hack/second | missing-watch | " + core + "ExternalAuth":                       true,
		"hack/second | missing-watch | " + core + "NodePool":                           true,
		"hack/second | unused-watch | " + core + "ServiceProviderCluster":              true,
		"hack/wrapped | unused-watch | " + core + "ManagementClusterContent":           true,
	}
	for _, f := range result.Findings {
		if !wantFindings[f.Identity()] {
			t.Errorf("unexpected finding %s", f.Identity())
		}
		delete(wantFindings, f.Identity())
		text := analyzer.FormatFinding(f)
		if f.Controller == "hack/cooldown-missing" && !strings.Contains(text, "RegistrationAwareCooldown.CanSync") {
			t.Errorf("missing cooldown evidence: %s", text)
		}
		if !strings.Contains(text, "instantiateControllers") {
			t.Errorf("missing actual injection path: %s", text)
		}
		if f.Type == core+"NodePool" && !strings.Contains(text, "writeNodePool") {
			t.Errorf("missing write helper evidence: %s", text)
		}
		if f.Type == core+"Subscription" && f.Controller == "hack/first" && !strings.Contains(text, "subscriptionLookup.Resolve") {
			t.Errorf("missing injected implementation evidence: %s", text)
		}
		if f.Controller == "hack/operations" && f.Type == core+"Subscription" {
			if len(f.Evidence) != 3 {
				t.Errorf("expected one generic Get site and two transaction sites: %s", text)
			}
			if strings.Contains(text, "longRead(...)") {
				t.Errorf("evidence must prefer direct readDocument path: %s", text)
			}
			for _, call := range []string{"readDocument", "AddCreateToTransaction", "AddReplaceToTransaction"} {
				if !strings.Contains(text, call) {
					t.Errorf("missing %s evidence", call)
				}
			}
		}
	}
	if len(wantFindings) != 0 {
		t.Fatalf("missing expected findings: %v", wantFindings)
	}
	t.Run("unsupported types remain explicit", func(t *testing.T) {
		r, err := runAnalyzer(pkgs, analyzer.NewWithEntrypoints([]string{fixture + ".BootstrapIncomplete"}))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.Controllers, []string{"hack/incomplete"}) {
			t.Fatalf("incomplete controller was lost: %v", r.Controllers)
		}
		if len(r.Findings) != 0 {
			t.Fatalf("unknown types must not become fabricated findings: %+v", r.Findings)
		}
		if len(r.Gaps) != 2 {
			t.Fatalf("want unknown informer and untyped CRUD gaps: %+v", r.Gaps)
		}
		var reasons []string
		for _, gap := range r.Gaps {
			reasons = append(reasons, gap.Reason)
			if len(gap.Path) < 2 {
				t.Fatalf("missing gap trace: %+v", gap)
			}
		}
		if !strings.Contains(strings.Join(reasons, "\n"), "unresolved informer type:") || !strings.Contains(strings.Join(reasons, "\n"), "UntypedResourceCRUD.ListRecursive") {
			t.Fatalf("wrong coverage gaps: %v", reasons)
		}
	})
	t.Run("command baseline and suppression", func(t *testing.T) {
		dir := t.TempDir()
		binary := filepath.Join(dir, "checker")
		baseline := filepath.Join(dir, "baseline.txt")
		build := exec.Command("go", "build", "-o", binary, "./hack/verify-controller-watches")
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
		run := func(flags ...string) (string, string, error) {
			args := []string{"-baseline", baseline, "-entrypoints", fixture + ".Bootstrap"}
			args = append(args, flags...)
			args = append(args, "./hack/verify-controller-watches/testdata/controllers", "./hack/verify-controller-watches/testdata/dispatch", "./internal/...", "./backend/pkg/...", "./fleet/pkg/...")
			cmd := exec.Command(binary, args...)
			cmd.Dir = root
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stdout.String(), stderr.String(), err
		}
		if _, stderr, err := run("-update"); err != nil {
			t.Fatalf("update: %v %s", err, stderr)
		}
		original, err := os.ReadFile(baseline)
		if err != nil {
			t.Fatal(err)
		}
		if stdout, stderr, err := run(); err != nil || stdout != "" {
			t.Fatalf("known findings must be silent: %v\n%s\n%s", err, stdout, stderr)
		}
		// Compare the generated file to explicit semantic expectations, not a
		// result object fabricated by the test. Evidence must never enter it.
		var expected []string
		for _, inventory := range want {
			for _, typ := range inventory.Accessed {
				if !slices.Contains(inventory.Watched, typ) {
					expected = append(expected, inventory.Controller+" | missing-watch | "+typ)
				}
			}
			for _, typ := range inventory.Watched {
				if !slices.Contains(inventory.Accessed, typ) {
					expected = append(expected, inventory.Controller+" | unused-watch | "+typ)
				}
			}
		}
		slices.Sort(expected)
		var entries []string
		for _, line := range strings.Split(string(original), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				entries = append(entries, line)
			}
		}
		if !reflect.DeepEqual(entries, expected) {
			t.Fatalf("baseline must contain only semantic identities: %s", original)
		}
		if _, stderr, err := run("-entrypoints", fixture+".BootstrapWithExtraEvidence", "-update"); err != nil {
			t.Fatalf("evidence variant: %v %s", err, stderr)
		}
		variant, err := os.ReadFile(baseline)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, variant) {
			t.Fatalf("different paths/additional access sites changed the semantic baseline:\n%s", variant)
		}
		missing := "hack/operations | missing-watch | " + core + "Subscription"
		if err := os.WriteFile(baseline, []byte(strings.ReplaceAll(string(original), missing+"\n", "")), 0600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := run()
		if err == nil || !strings.Contains(stdout, "hack/operations: missing-watch") || strings.Contains(stdout, "hack/first:") {
			t.Fatalf("missing entry should emit only its finding and fail: %v\n%s\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "readDocument") || !strings.Contains(stdout, "Invariant:") {
			t.Fatalf("missing detailed evidence: %s", stdout)
		}
		if err := os.WriteFile(baseline, append(original, []byte("hack/fixed | missing-watch | "+core+"Cluster\n")...), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := run(); err == nil {
			t.Fatal("stale entries must fail verification")
		}
		if _, stderr, err := run("-update"); err != nil {
			t.Fatalf("restore: %v %s", err, stderr)
		}
		restored, err := os.ReadFile(baseline)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, restored) {
			t.Fatal("baseline generation is not deterministic")
		}
	})
}
