// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package main

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/hack/verify-controller-watches/analyzer"
)

func TestBaselineStableIdentity(t *testing.T) {
	a := analyzer.Finding{Controller: "fleet/example", Invariant: "missing-watch", Type: "example.Type"}
	before := baselineContent(&analyzer.Result{Findings: []analyzer.Finding{a}})
	a.Evidence = []analyzer.Evidence{{Operation: "Get", Path: []analyzer.Location{{File: "moved.go", Line: 900}}}}
	a.Incomplete = true
	after := baselineContent(&analyzer.Result{Findings: []analyzer.Finding{a, a}})
	if before != after {
		t.Fatalf("evidence or duplicate paths changed baseline:\n%s", after)
	}
	if strings.Contains(after, "moved.go") {
		t.Fatal("baseline contains location")
	}
	b := a
	b.Type = "example.Other"
	added := baselineContent(&analyzer.Result{Findings: []analyzer.Finding{a, b}})
	if added == before {
		t.Fatal("new controller/type violation must change baseline")
	}
	if baselineContent(&analyzer.Result{}) == before {
		t.Fatal("fixed violation must require baseline removal")
	}
}
