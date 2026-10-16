// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

// Package analyzer checks Cosmos controller watch/access symmetry. Package facts
// contain a small, serializable program representation rather than pointers into
// another analysis pass. The registration package specializes these summaries
// with each controller's concrete constructor and interface bindings.
package analyzer

import (
	"fmt"
	"go/token"
	"sort"
	"strings"
)

const repo = "github.com/Azure/ARO-HCP/"

type Location struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
	Call     string `json:"call,omitempty"`
}

func (l Location) String() string {
	text := fmt.Sprintf("%s:%d: %s", l.File, l.Line, l.Function)
	if l.Call != "" {
		text += " -> " + l.Call
	}
	return text
}

type Evidence struct {
	Operation string     `json:"operation"`
	Path      []Location `json:"path"`
}

type Finding struct {
	Controller string     `json:"controller"`
	Invariant  string     `json:"invariant"`
	Type       string     `json:"type"`
	Evidence   []Evidence `json:"evidence"`
	Incomplete bool       `json:"incomplete,omitempty"`
}

// Identity deliberately excludes source locations and paths, so the baseline
// changes only when a controller/type invariant changes.
func (f Finding) Identity() string { return f.Controller + " | " + f.Invariant + " | " + f.Type }

type Gap struct {
	Controller string     `json:"controller"`
	Reason     string     `json:"reason"`
	Path       []Location `json:"path"`
}

type Result struct {
	Controllers []string    `json:"controllers"`
	Findings    []Finding   `json:"findings"`
	Gaps        []Gap       `json:"gaps"`
	Inventories []Inventory `json:"inventories"`
}

type Inventory struct {
	Controller string   `json:"controller"`
	Accessed   []string `json:"accessed"`
	Watched    []string `json:"watched"`
}

type PackageFact struct {
	Functions map[string]*Function
	Types     map[string]*Type
	Globals   map[string]*Expr
}

func (*PackageFact) AFact() {}

type Type struct {
	Params      []string
	Fields      map[string]string
	Embedded    []string
	Methods     map[string]string
	MethodPaths map[string][]string
	Document    string
	Interface   bool
}

type Function struct {
	ID               string
	Params           []string
	ParamTypes       []string
	TypeParams       []string
	Results          []string
	Body             []*Stmt
	Variadic         bool
	ReachesEndpoints bool
	MayDispatch      bool
	At               Location
}

type Expr struct {
	Kind     string
	Name     string
	Type     string
	Args     []*Expr
	Keys     []string
	TypeArgs []string
	At       Location
}

type Stmt struct {
	Kind  string
	Left  []*Expr
	Right []*Expr
	Body  []*Stmt
	Else  []*Stmt
	At    Location
}

func location(fset *token.FileSet, pos token.Pos, function string) Location {
	p := fset.Position(pos)
	name := p.Filename
	// Keep paths stable across worktrees. The runner also accepts standalone
	// fixture modules, where the source filename is sufficient.
	for _, marker := range []string{"/backend/", "/fleet/", "/internal/", "/admin/"} {
		if i := strings.LastIndex(name, marker); i >= 0 {
			name = name[i+1:]
			break
		}
	}
	return Location{File: name, Line: p.Line, Function: function}
}

func baseType(t string) string {
	t = strings.TrimPrefix(t, "*")
	if i := strings.IndexByte(t, '['); i >= 0 {
		t = t[:i]
	}
	return t
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
