// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import (
	"slices"
	"strings"
)

// checkBootstrap follows the actual startup call path. The only controller
// boundary is invocation of a typed Registration.Instantiate function; its
// arguments already include the caller's per-controller context mutations.
func checkBootstrap(program *PackageFact, roots []string, pkg string) *Result {
	result := &Result{}
	for _, root := range roots {
		e := newEvaluator(program, result)
		e.bootstrap = true
		e.service = strings.Split(strings.TrimPrefix(pkg, repo), "/")[0]
		e.discovered = map[string]bool{}
		e.invoke(&value{function: root}, nil, program.Functions[root].At)
	}
	slices.Sort(result.Controllers)
	slices.SortFunc(result.Findings, func(a, b Finding) int { return strings.Compare(a.Identity(), b.Identity()) })
	return result
}

func (e *evaluator) instantiate(fn *value, args []*value, at Location) []*value {
	name := e.service + "/" + fn.registration
	if e.discovered[name] {
		return []*value{{typ: "error"}, {typ: "error"}}
	}
	e.discovered[name] = true
	child := newEvaluator(e.program, e.result)
	child.controller = name
	e.result.Controllers = append(e.result.Controllers, name)
	// Retain the factory/injection path for diagnostics, but start a fresh
	// analysis budget and effect set for this one controller.
	child.path = slices.Clone(e.path)
	constructor := *fn
	constructor.registration = ""
	instances := child.invoke(&constructor, args, at)
	child.reconciling = true
	for _, instance := range instances {
		if instance == nil || instance.nilValue || instance.typ == "error" {
			continue
		}
		method := child.method(instance, "Run")
		if method.function == "" && len(method.alternatives) == 0 {
			child.gap("unresolved registered controller Run: "+instance.typ, at)
			continue
		}
		child.invoke(method, []*value{{typ: "context.Context"}, {typ: "int"}}, at)
	}
	child.finish()
	// The bootstrap interpreter must not run these controllers a second time
	// as unattributed application goroutines. Their effects were just collected.
	return []*value{{typ: "error"}, {typ: "error"}}
}

// Go copies structs by value while retaining the identity of pointers, maps,
// slices, and interface-held objects. In particular instanceContext := context
// must not overwrite the context used by the next registry entry.
func (e *evaluator) copyValue(v *value) *value {
	if v == nil {
		return nil
	}
	if strings.HasPrefix(v.typ, "*") {
		return v
	}
	t := e.program.Types[baseType(v.typ)]
	if t == nil || t.Interface || len(t.Fields) == 0 {
		return v
	}
	copy := *v
	copy.fields = map[string]*value{}
	for name, field := range v.fields {
		copy.fields[name] = e.copyValue(field)
	}
	return &copy
}
