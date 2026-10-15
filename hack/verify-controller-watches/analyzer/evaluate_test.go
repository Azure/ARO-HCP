// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import "testing"

func TestEvidenceKeepsShortestPathPerCallSite(t *testing.T) {
	r := &Result{}
	e := newEvaluator(&PackageFact{}, r)
	e.controller = "backend/example"
	site := Location{File: "helper.go", Line: 50, Function: "helper.Get"}
	e.path = []Location{{File: "controller.go", Line: 10}, {File: "helper.go", Line: 20}}
	e.record("example.Type", "Get", site, false)
	e.path = []Location{{File: "controller.go", Line: 30}}
	e.record("example.Type", "Get", site, false)
	e.record("example.Type", "Replace", Location{File: "helper.go", Line: 60}, false)
	e.finish()
	if len(r.Findings) != 1 || len(r.Findings[0].Evidence) != 2 {
		t.Fatalf("want one finding with two distinct sites: %+v", r.Findings)
	}
	if len(r.Findings[0].Evidence[0].Path) != 2 {
		t.Fatal("must retain shortest path")
	}
}

func TestUnknownWatchIsVisible(t *testing.T) {
	r := &Result{}
	e := newEvaluator(&PackageFact{}, r)
	e.controller = "backend/example"
	e.watchValue(&value{typ: "cache.SharedIndexInformer"}, Location{File: "controller.go", Line: 10})
	if len(r.Gaps) != 1 || len(e.watches) != 0 {
		t.Fatalf("unknown informer must produce a gap, not a fabricated watch: %+v", r)
	}
}

func TestGenericDocumentIdentity(t *testing.T) {
	p := &PackageFact{Types: map[string]*Type{"example.CRUD": {Params: []string{"T"}, Document: "T"}}}
	e := newEvaluator(p, &Result{})
	if got := e.doc(&value{typ: "example.CRUD[example.Doc]"}); got != "example.Doc" {
		t.Fatalf("generic document: %s", got)
	}
	if got := substitute("example.T[T, *T]", map[string]string{"T": "other.Doc"}); got != "example.T[other.Doc, *other.Doc]" {
		t.Fatalf("substitution changed qualified type: %s", got)
	}
}
