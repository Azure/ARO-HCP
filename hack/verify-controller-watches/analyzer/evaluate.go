// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Values retain allocation identity and fields: two instances of the same
// generic wrapper can bind different syncers, listers and optional informers.
type value struct {
	typ, literal, function, method, fallback string
	fields                                   map[string]*value
	items                                    []*value
	env                                      map[string]*value
	receiver                                 *value
	typeArgs                                 []string
	typeEnv                                  map[string]string
	doc                                      string
	nilValue                                 bool
	known                                    bool
	alternatives                             []*value
	origin                                   Location
	registration                             string
}

type evaluator struct {
	program         *PackageFact
	result          *Result
	controller      string
	path            []Location
	access, watches map[string][]Evidence
	gaps            map[string]bool
	active          map[string]bool
	globals         map[string]*value
	steps           int
	exempt          bool
	bootstrap       bool
	service         string
	discovered      map[string]bool
	reconciling     bool
}

type frame struct {
	vars    map[string]*value
	types   map[string]string
	returns []*value
}

func newEvaluator(p *PackageFact, r *Result) *evaluator {
	return &evaluator{program: p, result: r, access: map[string][]Evidence{}, watches: map[string][]Evidence{}, gaps: map[string]bool{}, active: map[string]bool{}, globals: map[string]*value{}}
}

func (e *evaluator) finish() {
	e.result.Inventories = append(e.result.Inventories, Inventory{Controller: e.controller, Accessed: sortedKeys(e.access), Watched: sortedKeys(e.watches)})
	for _, pair := range []struct {
		source, other map[string][]Evidence
		invariant     string
	}{{e.access, e.watches, "missing-watch"}, {e.watches, e.access, "unused-watch"}} {
		for _, doc := range sortedKeys(pair.source) {
			if len(pair.other[doc]) > 0 {
				continue
			}
			bySite := map[string]Evidence{}
			for _, evidence := range pair.source[doc] {
				if len(evidence.Path) == 0 {
					continue
				}
				key := evidence.Operation + "|" + evidence.Path[len(evidence.Path)-1].String()
				if old, ok := bySite[key]; !ok || len(evidence.Path) < len(old.Path) || len(evidence.Path) == len(old.Path) && pathKey(evidence.Path) < pathKey(old.Path) {
					bySite[key] = evidence
				}
			}
			f := Finding{Controller: e.controller, Invariant: pair.invariant, Type: doc, Incomplete: len(e.gaps) > 0}
			for _, site := range sortedKeys(bySite) {
				f.Evidence = append(f.Evidence, bySite[site])
			}
			e.result.Findings = append(e.result.Findings, f)
		}
	}
}

func pathKey(path []Location) string {
	var b strings.Builder
	for _, at := range path {
		b.WriteString(at.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func (e *evaluator) record(doc, op string, at Location, watch bool) {
	if e.controller == "" || doc == "" {
		return
	}
	if e.exempt && doc == repo+"internal/api/coreapi.Controller" {
		return
	}
	if !strings.Contains(doc, ".") {
		e.gap("unresolved document type "+doc, at)
		return
	}
	path := append(slices.Clone(e.path), at)
	target := e.access
	if watch {
		target = e.watches
	}
	target[doc] = append(target[doc], Evidence{Operation: op, Path: path})
}

func (e *evaluator) gap(reason string, at Location) {
	if e.controller == "" {
		return
	}
	key := reason + at.String()
	if e.gaps[key] {
		return
	}
	e.gaps[key] = true
	e.result.Gaps = append(e.result.Gaps, Gap{Controller: e.controller, Reason: reason, Path: append(slices.Clone(e.path), at)})
}

func substitute(t string, bindings map[string]string) string {
	// Replace whole identifiers, not substrings of package paths or type names.
	var out strings.Builder
	for i := 0; i < len(t); {
		if t[i] == '_' || t[i] >= 'A' && t[i] <= 'Z' || t[i] >= 'a' && t[i] <= 'z' {
			j := i + 1
			for j < len(t) && (t[j] == '_' || t[j] >= 'A' && t[j] <= 'Z' || t[j] >= 'a' && t[j] <= 'z' || t[j] >= '0' && t[j] <= '9') {
				j++
			}
			word := t[i:j]
			if replacement, ok := bindings[word]; ok && (i == 0 || t[i-1] != '.' && t[i-1] != '/') {
				out.WriteString(replacement)
			} else {
				out.WriteString(word)
			}
			i = j
		} else {
			out.WriteByte(t[i])
			i++
		}
	}
	return out.String()
}

func typeArguments(t string) []string {
	i := strings.IndexByte(t, '[')
	if i < 0 || !strings.HasSuffix(t, "]") {
		return nil
	}
	text := t[i+1 : len(t)-1]
	var args []string
	depth, start := 0, 0
	for i, c := range text {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return append(args, strings.TrimSpace(text[start:]))
}

func (e *evaluator) typeBindings(v *value) map[string]string {
	b := map[string]string{}
	if v == nil {
		return b
	}
	if t := e.program.Types[baseType(v.typ)]; t != nil {
		args := typeArguments(v.typ)
		for i, p := range t.Params {
			if i < len(args) {
				b[p] = args[i]
			}
		}
	}
	return b
}

func (e *evaluator) field(v *value, name, typ string) *value {
	if v == nil {
		return &value{typ: typ}
	}
	if e.bootstrap && name == "Enabled" && strings.Contains(v.typ, "controllerregistry.Registration[") {
		return &value{nilValue: true, known: true}
	}
	if len(v.alternatives) > 0 {
		var out *value
		for _, alternative := range v.alternatives {
			out = merge(out, e.field(alternative, name, typ))
		}
		return out
	}
	if v.fields == nil {
		v.fields = map[string]*value{}
	}
	if f := v.fields[name]; f != nil {
		return f
	}
	if t := e.program.Types[baseType(v.typ)]; t != nil {
		if ft, ok := t.Fields[name]; ok {
			typ = substitute(ft, e.typeBindings(v))
		} else {
			for _, embedded := range t.Embedded {
				inner := e.field(v, embedded, "")
				if it := e.program.Types[baseType(inner.typ)]; it != nil {
					if _, ok := it.Fields[name]; ok {
						return e.field(inner, name, typ)
					}
				}
			}
		}
	}
	f := &value{typ: typ}
	v.fields[name] = f
	return f
}

func (e *evaluator) method(v *value, name string) *value {
	m := &value{method: name, receiver: v}
	if v == nil {
		return m
	}
	if len(v.alternatives) > 0 {
		var out *value
		for _, alternative := range v.alternatives {
			out = merge(out, e.method(alternative, name))
		}
		return out
	}
	if t := e.program.Types[baseType(v.typ)]; t != nil {
		if fn := t.Methods[name]; fn != "" {
			if fields := t.MethodPaths[name]; len(fields) > 0 {
				inner := v
				for _, field := range fields {
					inner = e.field(inner, field, "")
				}
				return e.method(inner, name)
			}
			m.function = fn
			return m
		}
		for _, embedded := range t.Embedded {
			inner := e.method(e.field(v, embedded, ""), name)
			if inner.function != "" {
				return inner
			}
		}
	}
	return m
}

func (e *evaluator) eval(x *Expr, f *frame) *value {
	if x == nil {
		return &value{}
	}
	typ := substitute(x.Type, f.types)
	switch x.Kind {
	case "nil":
		return &value{typ: typ, nilValue: true, known: true}
	case "literal":
		return &value{typ: typ, literal: x.Name, known: true}
	case "var":
		if v := f.vars[x.Name]; v != nil {
			return v
		}
		if v := e.globals[x.Name]; v != nil {
			return v
		}
		if init := e.program.Globals[x.Name]; init != nil {
			e.globals[x.Name] = &value{typ: typ}
			v := e.eval(init, f)
			e.globals[x.Name] = v
			return v
		}
		return &value{typ: typ}
	case "func":
		args := slices.Clone(x.TypeArgs)
		for i := range args {
			args[i] = substitute(args[i], f.types)
		}
		return &value{typ: typ, function: x.Name, env: cloneEnv(f.vars), typeArgs: args, typeEnv: f.types}
	case "field":
		return e.field(e.eval(x.Args[0], f), x.Name, typ)
	case "method":
		v := e.eval(x.Args[0], f)
		m := e.method(v, x.Name)
		if len(x.Keys) > 0 {
			m.fallback = x.Keys[0]
		}
		return m
	case "object":
		v := &value{typ: typ, fields: map[string]*value{}, known: true}
		for i, arg := range x.Args {
			child := e.eval(arg, f)
			v.fields[x.Keys[i]] = child
			v.items = append(v.items, child)
		}
		return v
	case "call":
		fn := e.eval(x.Args[0], f)
		var args []*value
		for _, arg := range x.Args[1:] {
			args = append(args, e.eval(arg, f))
		}
		out := e.call(fn, args, x, typ)
		if len(out) == 1 {
			return out[0]
		}
		return &value{typ: typ, items: out}
	case "convert":
		if len(x.Args) == 0 {
			return &value{typ: typ}
		}
		v := e.eval(x.Args[len(x.Args)-1], f)
		if v.typ == "" {
			v.typ = typ
		}
		return v
	case "unary":
		v := e.eval(x.Args[0], f)
		if x.Name == "&" {
			copy := *v
			copy.typ = typ
			return &copy
		}
		if x.Name == "!" && v.known {
			return &value{typ: "bool", known: true, literal: strconv.FormatBool(v.literal != "true")}
		}
		return v
	case "binary":
		a, b := e.eval(x.Args[0], f), e.eval(x.Args[1], f)
		if x.Name == "!=" || x.Name == "==" {
			known := a.known && b.known
			equal := a.nilValue == b.nilValue && a.literal == b.literal
			if (a.nilValue && b.known && b.fields != nil) || (b.nilValue && a.known && a.fields != nil) {
				known = true
				equal = false
			}
			if x.Name == "!=" {
				equal = !equal
			}
			return &value{typ: "bool", known: known, literal: strconv.FormatBool(equal)}
		}
		if a.known && b.known && x.Name == "+" {
			return &value{typ: typ, known: true, literal: a.literal + b.literal}
		}
		return &value{typ: typ}
	case "index":
		v, k := e.eval(x.Args[0], f), e.eval(x.Args[1], f)
		if item := v.fields[k.literal]; k.known && item != nil {
			return item
		}
		dynamicKeys := false
		for key := range v.fields {
			if strings.HasPrefix(key, "$dynamic") {
				dynamicKeys = true
			}
		}
		if (!k.known || dynamicKeys) && len(v.fields) > 0 {
			var out *value
			for _, key := range sortedKeys(v.fields) {
				out = merge(out, v.fields[key])
			}
			return out
		}
		if len(v.items) > 0 && !strings.HasPrefix(v.typ, "map[") {
			return v.items[0]
		}
		if k.known && v.fields != nil {
			return &value{typ: typ, nilValue: true, known: true}
		}
		return &value{typ: typ}
	}
	return &value{typ: typ}
}

func cloneEnv(env map[string]*value) map[string]*value {
	out := map[string]*value{}
	for k, v := range env {
		out[k] = v
	}
	return out
}

func (e *evaluator) assign(left *Expr, v *value, f *frame) {
	switch left.Kind {
	case "var":
		f.vars[left.Name] = e.copyValue(v)
	case "field":
		parent := e.eval(left.Args[0], f)
		if parent.fields == nil {
			parent.fields = map[string]*value{}
		}
		parent.fields[left.Name] = v
	case "index":
		parent := e.eval(left.Args[0], f)
		key := e.eval(left.Args[1], f)
		if parent.fields == nil {
			parent.fields = map[string]*value{}
		}
		if key.known {
			if strings.Contains(v.typ, "controllerregistry.Registration[") {
				v.origin = left.At
				if constructor := v.fields["Instantiate"]; constructor != nil {
					copy := *constructor
					copy.registration = key.literal
					v.fields["Instantiate"] = &copy
				}
			}
			parent.fields[key.literal] = v
		} else {
			parent.items = append(parent.items, v)
		}
	}
}

func (e *evaluator) statements(body []*Stmt, f *frame) bool {
	for _, s := range body {
		e.steps++
		limit := 200000
		if e.bootstrap {
			limit = 2000000
		}
		if e.steps > limit {
			e.gap("analysis work limit reached", s.At)
			return true
		}
		switch s.Kind {
		case "assign":
			var vals []*value
			for _, rhs := range s.Right {
				vals = append(vals, e.eval(rhs, f))
			}
			if len(vals) == 1 && len(s.Left) > 1 && len(vals[0].items) > 0 && strings.HasPrefix(vals[0].typ, "(") {
				vals = vals[0].items
			}
			if len(s.Right) == 1 && s.Right[0].Kind == "index" && len(s.Left) == 2 && len(vals) == 1 {
				index := s.Right[0]
				parent := e.eval(index.Args[0], f)
				key := e.eval(index.Args[1], f)
				if key.known && parent.fields != nil {
					_, found := parent.fields[key.literal]
					vals = append(vals, &value{typ: "bool", known: true, literal: strconv.FormatBool(found)})
				}
			}
			for i, lhs := range s.Left {
				v := &value{typ: substitute(lhs.Type, f.types)}
				if i < len(vals) {
					v = vals[i]
				}
				e.assign(lhs, v, f)
			}
		case "expr":
			for _, rhs := range s.Right {
				e.eval(rhs, f)
			}
		case "return":
			var vals []*value
			for _, rhs := range s.Right {
				vals = append(vals, e.eval(rhs, f))
			}
			if len(vals) == 1 && strings.HasPrefix(s.Right[0].Type, "(") {
				vals = vals[0].items
			}
			for i, v := range vals {
				for len(f.returns) <= i {
					f.returns = append(f.returns, nil)
				}
				f.returns[i] = merge(f.returns[i], v)
			}
			return true
		case "if":
			cond := e.eval(s.Right[0], f)
			// Prune constructor wiring branches only. Reconciliation reads are
			// potential dependencies even when a helper returns a literal after
			// examining runtime document state.
			if cond.known && !e.reconciling {
				branch := s.Else
				if cond.literal == "true" {
					branch = s.Body
				}
				if e.statements(branch, f) {
					return true
				}
			} else {
				a := &frame{vars: cloneEnv(f.vars), types: f.types, returns: f.returns}
				b := &frame{vars: cloneEnv(f.vars), types: f.types, returns: f.returns}
				ar, br := e.statements(s.Body, a), e.statements(s.Else, b)
				for k, v := range a.vars {
					f.vars[k] = merge(v, b.vars[k])
				}
				for i, v := range a.returns {
					for len(f.returns) <= i {
						f.returns = append(f.returns, nil)
					}
					f.returns[i] = merge(f.returns[i], v)
				}
				for i, v := range b.returns {
					for len(f.returns) <= i {
						f.returns = append(f.returns, nil)
					}
					f.returns[i] = merge(f.returns[i], v)
				}
				if ar && br {
					return true
				}
			}
		case "range":
			v := e.eval(s.Right[0], f)
			if strings.HasPrefix(v.typ, "map[") && len(v.fields) > 0 {
				for _, key := range sortedKeys(v.fields) {
					e.assign(s.Left[0], &value{typ: "string", literal: key, known: true}, f)
					e.assign(s.Left[1], v.fields[key], f)
					e.statements(s.Body, f)
				}
				continue
			}
			items := v.items
			if len(items) == 0 {
				for _, key := range sortedKeys(v.fields) {
					items = append(items, v.fields[key])
				}
			}
			if len(items) == 0 {
				items = []*value{{}}
			}
			for _, item := range items {
				if len(s.Left) > 1 {
					e.assign(s.Left[1], item, f)
				}
				e.statements(s.Body, f)
			}
		case "branch":
			branch := &frame{vars: cloneEnv(f.vars), types: f.types}
			e.statements(s.Body, branch)
			for k, v := range branch.vars {
				f.vars[k] = merge(f.vars[k], v)
			}
			for i, v := range branch.returns {
				for len(f.returns) <= i {
					f.returns = append(f.returns, nil)
				}
				f.returns[i] = merge(f.returns[i], v)
			}
		case "block":
			for _, x := range s.Right {
				e.eval(x, f)
			}
			if e.statements(s.Body, f) {
				return true
			}
		}
	}
	return false
}

func merge(a, b *value) *value {
	if a == nil || a.nilValue {
		return b
	}
	if b == nil || b.nilValue || a == b {
		return a
	}
	// An alternatives wrapper is already a set of concrete bindings, not an
	// unknown value. Union it before the concrete-over-unknown preferences.
	if len(a.alternatives) > 0 || len(b.alternatives) > 0 {
		return alternatives(a, b)
	}
	if a.function != "" && b.function != "" && (a.function != b.function || a.receiver != b.receiver) {
		return alternatives(a, b)
	}
	if a.fields != nil && b.fields != nil && a != b {
		return alternatives(a, b)
	}
	// Prefer a concrete allocation/binding over an unknown value. Ambiguous
	// concrete alternatives are retained for interface dispatch.
	if a.function == "" && b.function != "" {
		return b
	}
	if a.fields == nil && b.fields != nil {
		return b
	}
	if a.known != b.known || a.literal != b.literal {
		copy := *a
		copy.known = false
		return &copy
	}
	return a
}

func alternatives(a, b *value) *value {
	items := []*value{}
	seen := map[*value]bool{}
	for _, v := range []*value{a, b} {
		vs := v.alternatives
		if len(vs) == 0 {
			vs = []*value{v}
		}
		for _, item := range vs {
			if !seen[item] {
				items = append(items, item)
				seen[item] = true
			}
		}
	}
	return &value{typ: a.typ, alternatives: items}
}

func (e *evaluator) invoke(fn *value, args []*value, at Location) []*value {
	if fn == nil {
		return nil
	}
	if e.bootstrap && fn.registration != "" {
		return e.instantiate(fn, args, at)
	}
	if len(fn.alternatives) > 0 {
		var out []*value
		for _, alt := range fn.alternatives {
			values := e.invoke(alt, args, at)
			for i, v := range values {
				for len(out) <= i {
					out = append(out, nil)
				}
				out[i] = merge(out[i], v)
			}
		}
		return out
	}
	id := fn.function
	if id == "" {
		id = fn.fallback
	}
	f := e.program.Functions[id]
	if f == nil {
		return nil
	}
	key := fmt.Sprintf("%s/%p", id, fn.receiver)
	if e.active[key] {
		return nil
	}
	if len(e.path) > 80 {
		e.gap("call depth limit reached: "+id, at)
		return nil
	}
	e.active[key] = true
	defer delete(e.active, key)
	old := e.path
	e.path = append(slices.Clone(e.path), at)
	defer func() { e.path = old }()
	bindings := e.typeBindings(fn.receiver)
	for param, typ := range fn.typeEnv {
		bindings[param] = typ
	}
	for i, param := range f.TypeParams {
		if i < len(fn.typeArgs) {
			bindings[param] = fn.typeArgs[i]
		}
	}
	frame := &frame{vars: cloneEnv(fn.env), types: bindings}
	if fn.receiver != nil {
		args = append([]*value{fn.receiver}, args...)
	}
	for i, p := range f.Params {
		v := &value{typ: substitute(f.ParamTypes[i], bindings)}
		if i < len(args) && args[i] != nil {
			v = args[i]
			if v.typ == "" || v.typ == "untyped nil" {
				copy := *v
				copy.typ = substitute(f.ParamTypes[i], bindings)
				v = &copy
			}
		}
		if f.Variadic && i == len(f.Params)-1 && i < len(args) {
			if len(args) != len(f.Params) || !strings.HasPrefix(args[i].typ, "[]") {
				v = &value{typ: substitute(f.ParamTypes[i], bindings), items: args[i:]}
			}
		}
		frame.vars[p] = e.copyValue(v)
	}
	// Only the framework's calls to its own status bookkeeping are exempt.
	wasExempt := e.exempt
	if isFrameworkStatusCall(id, at.Function) {
		e.exempt = true
	}
	defer func() { e.exempt = wasExempt }()
	e.statements(f.Body, frame)
	for i, typ := range f.Results {
		for len(frame.returns) <= i {
			frame.returns = append(frame.returns, nil)
		}
		if frame.returns[i] == nil {
			frame.returns[i] = &value{typ: substitute(typ, bindings)}
		}
	}
	return frame.returns
}

func isFrameworkStatusCall(target, caller string) bool {
	if !strings.HasSuffix(target, ".WriteController") && !strings.HasSuffix(target, ".DegradedControllerPanicHandler") {
		return false
	}
	return strings.Contains(caller, repo+"backend/pkg/utils/controllerutils.") || strings.Contains(caller, repo+"fleet/pkg/controllers/base.")
}

func (e *evaluator) call(fn *value, args []*value, x *Expr, typ string) []*value {
	unknown := func() []*value { return unknownResults(typ) }
	if len(fn.alternatives) > 0 {
		var out []*value
		for _, alt := range fn.alternatives {
			values := e.call(alt, args, x, typ)
			for i, v := range values {
				for len(out) <= i {
					out = append(out, nil)
				}
				out[i] = merge(out[i], v)
			}
		}
		return out
	}
	id := fn.function
	if id == "" {
		id = fn.fallback
	}
	if id == "make" {
		return []*value{{typ: typ, fields: map[string]*value{}, known: true}}
	}
	if id == "append" {
		if len(args) > 0 {
			v := args[0]
			v.items = append(v.items, args[1:]...)
			return []*value{v}
		}
		return unknown()
	}
	if id == "strings.ToLower" && len(args) > 0 {
		return []*value{{typ: "string", literal: strings.ToLower(args[0].literal), known: args[0].known}}
	}
	if id == repo+"backend/pkg/controllers/controllerconfig.WithCacheSyncs" && len(args) > 0 {
		return []*value{args[0]}
	}
	if strings.HasPrefix(id, repo+"internal/database/informers/") && strings.Contains(id, ".New") {
		return unknown()
	}
	if fn.receiver != nil {
		if out, ok := e.boundary(fn, args, x, typ); ok {
			return out
		}
		if t := e.program.Types[baseType(fn.receiver.typ)]; t != nil && t.Interface && endpointPackage(packageOf(fn.receiver.typ)) {
			e.gap("unresolved storage interface call "+id, x.At)
			return unknown()
		}
	}
	if f := e.program.Functions[id]; f != nil && len(f.Body) > 0 {
		if f.ReachesEndpoints || f.MayDispatch || e.carriesBinding(args, map[*value]bool{}) {
			out := e.invoke(fn, args, x.At)
			if len(out) > 0 {
				return out
			}
			return unknown()
		}
	}
	// A pruned package cannot call our endpoints directly, but can invoke
	// caller-supplied functions. Preserve those edges without walking its body.
	e.callbacks(args, x.At, map[*value]bool{})
	if fn.receiver != nil && strings.HasPrefix(baseType(fn.receiver.typ), repo) && id != "" && e.program.Functions[id] == nil {
		if strings.Contains(fn.receiver.typ, "Syncer") || strings.Contains(fn.receiver.typ, "Validation") || strings.Contains(fn.receiver.typ, "Runnable") {
			e.gap("unresolved interface call "+id, x.At)
		}
	}
	return unknown()
}

func (e *evaluator) carriesBinding(values []*value, seen map[*value]bool) bool {
	for _, v := range values {
		if v == nil || seen[v] {
			continue
		}
		seen[v] = true
		if v.function != "" {
			return true
		}
		if t := e.program.Types[baseType(v.typ)]; t != nil {
			for _, id := range t.Methods {
				if f := e.program.Functions[id]; f != nil && (f.ReachesEndpoints || f.MayDispatch) {
					return true
				}
			}
		}
		if e.carriesBinding(v.items, seen) || e.carriesBinding(v.alternatives, seen) {
			return true
		}
	}
	return false
}

func packageOf(typ string) string {
	base := baseType(typ)
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		return base[:i]
	}
	return ""
}

func (e *evaluator) callbacks(values []*value, at Location, seen map[*value]bool) {
	for _, v := range values {
		if v == nil || seen[v] {
			continue
		}
		seen[v] = true
		if v.function != "" {
			e.invoke(v, nil, at)
		}
		e.callbacks(v.items, at, seen)
		e.callbacks(v.alternatives, at, seen)
		// Follow explicitly supplied callback fields, not the entire object
		// graph of clients and controllers passed to logging functions.
		for _, field := range v.fields {
			if field.function != "" {
				e.invoke(field, nil, at)
			}
		}
	}
}

func (e *evaluator) doc(v *value) string {
	if v == nil {
		return ""
	}
	if v.doc != "" {
		return v.doc
	}
	if t := e.program.Types[baseType(v.typ)]; t != nil && t.Document != "" {
		return substitute(t.Document, e.typeBindings(v))
	}
	return ""
}

func (e *evaluator) boundary(fn *value, args []*value, x *Expr, typ string) ([]*value, bool) {
	v := fn.receiver
	name := fn.method
	recv := baseType(v.typ)
	unknown := unknownResults(typ)
	// Iteration belongs to the typed List endpoint already recorded. It is
	// trusted storage implementation plumbing, not another client factory.
	if recv == repo+"internal/database/cosmosstorage/cosmosstorageutils.DBClientIterator" {
		return unknown, true
	}
	if strings.HasPrefix(recv, repo+"internal/database/informers/") || strings.HasPrefix(recv, repo+"internal/database/unioninformers/") {
		// Informer accessors return an informer and its typed lister. Derive the
		// informer document from that lister's Get/List signature.
		if strings.HasPrefix(typ, "(") {
			parts := splitTuple(typ)
			if len(parts) == 2 {
				lister := &value{typ: parts[1]}
				if doc := e.doc(lister); doc != "" {
					return []*value{{typ: parts[0], doc: doc}, lister}, true
				}
			}
		}
		if name == "AllOperations" {
			return []*value{{typ: typ, doc: repo + "internal/api/coreapi.Operation"}}, true
		}
		if name == "Union" {
			return []*value{{typ: typ}}, true
		}
	}
	if strings.HasPrefix(recv, repo+"internal/controllerutils.GenericWatchingController") {
		if name == "QueueForInformers" || name == "QueueForInformersWithMaxDepth" {
			start := 1
			if name == "QueueForInformersWithMaxDepth" {
				start = 2
			}
			for _, arg := range args[start:] {
				e.watchValue(arg, x.At)
			}
			return unknown, true
		}
		if name == "Run" {
			syncer := e.field(v, "syncer", "")
			e.invoke(e.method(syncer, "SyncOnce"), nil, x.At)
			cool := e.invoke(e.method(syncer, "CooldownChecker"), nil, x.At)
			for _, c := range cool {
				e.invoke(e.method(c, "CanSync"), nil, x.At)
			}
			return unknown, true
		}
	}
	if name == "AddEventHandler" || name == "AddEventHandlerWithOptions" || name == "AddEventHandlerWithResyncPeriod" {
		e.watchValue(v, x.At)
		for _, arg := range args {
			for _, cb := range arg.fields {
				if cb.function != "" {
					e.invoke(cb, nil, x.At)
				}
			}
		}
		return unknown, true
	}
	if (name == "RunWithContext" || name == "Run") && strings.HasPrefix(recv, repo+"internal/database/informers/") {
		return unknown, true
	}
	if name == "GetIndexer" || name == "GetStore" {
		return []*value{{typ: typ, doc: v.doc}}, true
	}
	if v.doc != "" && (name == "GetByKey" || name == "ByIndex" || name == "List") {
		e.record(v.doc, name, x.At, false)
		return unknown, true
	}
	if strings.HasPrefix(recv, repo+"internal/database/listers/") {
		if doc := e.doc(v); doc != "" {
			e.record(doc, recv+"."+name, x.At, false)
			return unknown, true
		}
	}
	if strings.HasPrefix(recv, repo+"internal/database/cosmosstorage/") {
		// The typed CRUD/lister surface includes typed child accessors. Trust
		// their result contract without attributing an access until Get/List/etc.
		if len(unknown) > 0 && e.doc(unknown[0]) != "" && !accessMethod(name) && e.program.Functions[fn.function] == nil {
			return unknown, true
		}
		isAccess := accessMethod(name)
		if isAccess {
			if doc := e.doc(v); doc != "" {
				e.record(doc, recv+"."+name, x.At, false)
			} else {
				e.gap("untyped Cosmos access: "+recv+"."+name, x.At)
			}
			return unknown, true
		}
	}
	return nil, false
}

func unknownResults(typ string) []*value {
	parts := []string{typ}
	if strings.HasPrefix(typ, "(") {
		parts = splitTuple(typ)
	}
	values := make([]*value, 0, len(parts))
	for _, part := range parts {
		values = append(values, &value{typ: part})
	}
	return values
}

func splitTuple(t string) []string {
	return typeArguments("tuple[" + strings.TrimSuffix(strings.TrimPrefix(t, "("), ")") + "]")
}

func (e *evaluator) watchValue(v *value, at Location) {
	if v == nil || v.nilValue {
		return
	}
	if len(v.alternatives) > 0 {
		for _, alt := range v.alternatives {
			e.watchValue(alt, at)
		}
		return
	}
	if v.doc != "" {
		e.record(v.doc, "watch", at, true)
		return
	}
	if len(v.items) > 0 {
		for _, item := range v.items {
			e.watchValue(item, at)
		}
		return
	}
	e.gap("unresolved informer type: "+v.typ, at)
}

func FormatFinding(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s for %s\n", f.Controller, f.Invariant, f.Type)
	if f.Invariant == "missing-watch" {
		b.WriteString("Invariant: every document type read or written must have a watch.\n")
	} else {
		b.WriteString("Invariant: every watched document type must have a lister/CRUD read or write.\n")
	}
	if f.Incomplete {
		b.WriteString("Coverage is incomplete for this controller; review its analysis-gap diagnostics.\n")
	}
	for _, ev := range f.Evidence {
		fmt.Fprintf(&b, "  %s:\n", ev.Operation)
		for _, at := range ev.Path {
			fmt.Fprintf(&b, "    %s\n", at)
		}
	}
	return b.String()
}
