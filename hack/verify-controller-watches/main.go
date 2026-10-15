// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"

	"github.com/Azure/ARO-HCP/hack/verify-controller-watches/analyzer"
)

func main() {
	baseline := flag.String("baseline", "hack/verify-controller-watches/baseline.txt", "stable finding baseline")
	update := flag.Bool("update", false, "replace the baseline with current finding identities")
	all := flag.Bool("all", false, "show evidence for baseline findings as well as new findings")
	jsonOutput := flag.Bool("json", false, "emit the full analysis report as JSON")
	flag.Parse()
	start := time.Now()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./backend/pkg/...", "./fleet/pkg/...", "./admin/server/...", "./internal/..."}
	}
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadSyntax, Tests: false}, patterns...)
	if err != nil {
		fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		fatal(fmt.Errorf("cannot analyze packages with loading/type errors"))
	}
	loaded := time.Now()
	result, err := run(pkgs)
	if err != nil {
		fatal(err)
	}
	finished := time.Now()
	fmt.Fprintf(os.Stderr, "controller-watches: %d controllers, %d findings, %d analysis gaps; loading %s, analysis %s\n", len(result.Controllers), len(result.Findings), len(result.Gaps), loaded.Sub(start).Round(time.Millisecond), finished.Sub(loaded).Round(time.Millisecond))
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fatal(err)
		}
		return
	}
	data, err := os.ReadFile(*baseline)
	if err != nil && !os.IsNotExist(err) {
		fatal(err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			known[line] = true
		}
	}
	for _, finding := range result.Findings {
		if *all || !known[finding.Identity()] {
			fmt.Print(analyzer.FormatFinding(finding))
		}
	}
	for _, gap := range result.Gaps {
		fmt.Fprintf(os.Stderr, "%s: analysis incomplete: %s\n", gap.Controller, gap.Reason)
		for _, at := range gap.Path {
			fmt.Fprintf(os.Stderr, "  %s\n", at)
		}
	}
	content := baselineContent(result)
	if *update {
		if err := os.WriteFile(*baseline, []byte(content), 0644); err != nil {
			fatal(err)
		}
		return
	}
	if string(data) != content {
		fmt.Fprintln(os.Stderr, "controller-watch baseline differs; inspect findings and run make update-controller-watches")
		os.Exit(1)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }

// This small driver shares the same package-local Analyzer used by standard
// analysis hosts. Dependency facts are computed once, then reused by roots.
func run(pkgs []*packages.Package) (*analyzer.Result, error) {
	a := analyzer.New()
	facts := map[string]*analyzer.PackageFact{}
	seen := map[string]bool{}
	result := &analyzer.Result{}
	byPath := map[string]*packages.Package{}
	for _, pkg := range pkgs {
		byPath[pkg.PkgPath] = pkg
	}
	var visit func(*packages.Package) error
	visit = func(pkg *packages.Package) error {
		if seen[pkg.PkgPath] {
			return nil
		}
		seen[pkg.PkgPath] = true
		for _, dep := range pkg.Imports {
			if full := byPath[dep.PkgPath]; full != nil {
				if err := visit(full); err != nil {
					return err
				}
			}
		}
		if len(pkg.Syntax) == 0 {
			return nil
		}
		pass := &analysis.Pass{Analyzer: a, Fset: pkg.Fset, Files: pkg.Syntax, Pkg: pkg.Types, TypesInfo: pkg.TypesInfo, TypesSizes: pkg.TypesSizes, Report: func(analysis.Diagnostic) {}}
		pass.ExportPackageFact = func(f analysis.Fact) { facts[pkg.PkgPath] = f.(*analyzer.PackageFact) }
		pass.ImportPackageFact = func(p *types.Package, f analysis.Fact) bool {
			stored := facts[p.Path()]
			if stored == nil {
				return false
			}
			reflect.ValueOf(f).Elem().Set(reflect.ValueOf(stored).Elem())
			return true
		}
		out, err := a.Run(pass)
		if err != nil {
			return err
		}
		r := out.(*analyzer.Result)
		result.Controllers = append(result.Controllers, r.Controllers...)
		result.Findings = append(result.Findings, r.Findings...)
		result.Gaps = append(result.Gaps, r.Gaps...)
		result.Inventories = append(result.Inventories, r.Inventories...)
		return nil
	}
	for _, pkg := range pkgs {
		if err := visit(pkg); err != nil {
			return nil, err
		}
	}
	slices.Sort(result.Controllers)
	result.Controllers = slices.Compact(result.Controllers)
	slices.SortFunc(result.Findings, func(a, b analyzer.Finding) int { return strings.Compare(a.Identity(), b.Identity()) })
	slices.SortFunc(result.Gaps, func(a, b analyzer.Gap) int {
		return strings.Compare(a.Controller+a.Reason+fmt.Sprint(a.Path), b.Controller+b.Reason+fmt.Sprint(b.Path))
	})
	slices.SortFunc(result.Inventories, func(a, b analyzer.Inventory) int { return strings.Compare(a.Controller, b.Controller) })
	// Fixture modules have no repo-relative marker; normalize against cwd.
	root, _ := os.Getwd()
	for i := range result.Findings {
		for j := range result.Findings[i].Evidence {
			for k := range result.Findings[i].Evidence[j].Path {
				at := &result.Findings[i].Evidence[j].Path[k]
				if filepath.IsAbs(at.File) {
					if rel, err := filepath.Rel(root, at.File); err == nil {
						at.File = rel
					}
				}
			}
		}
	}
	return result, nil
}
