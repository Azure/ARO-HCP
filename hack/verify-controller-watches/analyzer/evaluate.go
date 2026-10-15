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
}

type frame struct {
	vars    map[string]*value
	types   map[string]string
	returns []*value
}

func check(program *PackageFact, roots []string, pkg string) *Result {
	result := &Result{}
	slices.Sort(roots)
	for _, root := range roots {
		e := newEvaluator(program, result)
		registryValues := e.invoke(&value{function: root}, nil, program.Functions[root].At)
		if len(registryValues) == 0 || registryValues[0] == nil {
			continue
		}
		registry := registryValues[0]
		for _, name := range sortedKeys(registry.fields) {
			entry := registry.fields[name]
			if entry == nil || entry.fields["Instantiate"] == nil {
				continue
			}
			e := newEvaluator(program, result)
			service := strings.TrimPrefix(pkg, repo)
			service = strings.Split(service, "/")[0]
			e.controller = service + "/" + name
			result.Controllers = append(result.Controllers, e.controller)
			constructor := entry.fields["Instantiate"]
			registration := entry.origin
			if registration.File == "" {
				registration = program.Functions[root].At
			}
			// A symbolic context has typed fields; constructor calls specialize
			// them without constructing live clients or evaluating Enabled gates.
			var args []*value
			if fn := program.Functions[constructor.function]; fn != nil {
				for _, typ := range fn.ParamTypes {
					args = append(args, e.context(substitute(typ, constructor.typeEnv), pkg))
				}
			}
			instances := e.invoke(constructor, args, registration)
			for _, instance := range instances {
				if instance == nil || instance.nilValue {
					continue
				}
				if method := e.method(instance, "Run"); method.function != "" || len(method.alternatives) > 0 {
					e.invoke(method, []*value{{typ: "context.Context"}, {typ: "int"}}, registration)
				} else if instance.typ != "error" {
					e.gap("unresolved registered controller Run: "+instance.typ, program.Functions[root].At)
				}
			}
			e.finish()
		}
	}
	slices.Sort(result.Controllers)
	slices.SortFunc(result.Findings, func(a, b Finding) int { return strings.Compare(a.Identity(), b.Identity()) })
	return result
}

// Resolve preconstructed instances from the service's context builder. This
// keeps shared controller registration bound to the actual startup allocation.
func (e *evaluator) context(typ, pkg string) *value {
	for _, id := range sortedKeys(e.program.Functions) {
		fn := e.program.Functions[id]
		if strings.HasPrefix(id, pkg+".") && strings.HasSuffix(id, ".newControllerContext") && len(fn.Results) == 1 && fn.Results[0] == typ {
			old := e.controller
			e.controller = ""
			out := e.invoke(&value{function: id}, nil, fn.At)
			e.controller = old
			if len(out) > 0 {
				return out[0]
			}
		}
	}
	return &value{typ: typ}
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
		if len(v.items) > 0 {
			return v.items[0]
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
		f.vars[left.Name] = v
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
		if e.steps > 200000 {
			e.gap("analysis work limit reached", s.At)
			return true
		}
		switch s.Kind {
		case "assign":
			var vals []*value
			for _, rhs := range s.Right {
				vals = append(vals, e.eval(rhs, f))
			}
			if len(vals) == 1 && len(s.Left) > 1 && len(vals[0].items) > 0 {
				vals = vals[0].items
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
			if cond.known && !runtimePath(e.path) {
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

func runtimePath(path []Location) bool {
	for _, at := range path {
		if strings.HasSuffix(at.Function, ".Run") || strings.HasSuffix(at.Function, ".SyncOnce") || strings.HasSuffix(at.Function, ".SynchronizeOperation") {
			return true
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
			if v.typ == "" {
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
		frame.vars[p] = v
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
	if strings.HasSuffix(id, ".WithCacheSyncs") && len(args) > 0 {
		return []*value{args[0]}
	}
	if strings.HasPrefix(id, repo+"internal/database/informers/") && strings.Contains(id, ".New") {
		return unknown()
	}
	if fn.receiver != nil {
		if out, ok := e.boundary(fn, args, x, typ); ok {
			return out
		}
	}
	if f := e.program.Functions[id]; f != nil && len(f.Body) > 0 {
		// API data/formatting helpers cannot add controller subscriptions. Avoid
		// descending into deepcopy/serialization and external client plumbing.
		if !strings.HasPrefix(id, repo+"internal/api/") && !strings.HasPrefix(id, repo+"internal/utils.") {
			out := e.invoke(fn, args, x.At)
			if len(out) > 0 {
				return out
			}
			return unknown()
		}
	}
	// Worker loops/retry helpers invoke function arguments. Follow callbacks
	// with their captured bindings, without analyzing Kubernetes internals.
	if strings.HasPrefix(id, "k8s.io/apimachinery/pkg/util/wait.") || strings.HasPrefix(id, "k8s.io/client-go/util/retry.") {
		for _, arg := range args {
			if arg.function != "" {
				e.invoke(arg, nil, x.At)
			}
		}
	}
	if fn.receiver != nil && strings.HasPrefix(baseType(fn.receiver.typ), repo) && id != "" && e.program.Functions[id] == nil {
		if strings.Contains(fn.receiver.typ, "Syncer") || strings.Contains(fn.receiver.typ, "Validation") || strings.Contains(fn.receiver.typ, "Runnable") {
			e.gap("unresolved interface call "+id, x.At)
		}
	}
	return unknown()
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
	// For is not pure client navigation: its production implementation reads
	// ManagementCluster through a lister to resolve the physical container.
	if recv == repo+"internal/database/cosmosstorage/kubeappliercosmosstorage.KubeApplierDBClients" && name == "For" {
		concrete := &value{typ: "*" + repo + "internal/database/cosmosstorage/kubeappliercosmosstorage.kubeApplierDBClients"}
		e.invoke(e.method(concrete, "For"), args, x.At)
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
		isAccess := name == "Get" || name == "GetByID" || (strings.HasPrefix(name, "List") && name != "Listers") || name == "Create" || name == "Replace" || name == "Delete" || strings.HasPrefix(name, "AddCreateToTransaction") || strings.HasPrefix(name, "AddReplaceToTransaction") || name == "DeleteByCosmosID"
		if isAccess {
			if doc := e.doc(v); doc != "" {
				e.record(doc, recv+"."+name, x.At, false)
			} else {
				e.gap("untyped Cosmos access: "+recv+"."+name, x.At)
			}
			return unknown, true
		}
		// Client navigation is not a read (e.g. HCPClusters().NodePools()).
		if strings.HasPrefix(typ, repo+"internal/database/cosmosstorage/") {
			return unknown, true
		}
		if strings.HasPrefix(typ, "(") {
			parts := splitTuple(typ)
			if len(parts) > 0 && strings.HasPrefix(parts[0], repo+"internal/database/cosmosstorage/") {
				var out []*value
				for _, p := range parts {
					out = append(out, &value{typ: p})
				}
				return out, true
			}
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
