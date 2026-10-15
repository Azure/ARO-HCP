// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

func New() *analysis.Analyzer {
	return NewWithEntrypoints([]string{
		repo + "backend/cmd.RunRootCmd",
		repo + "fleet/cmd/controller.run",
	})
}

// NewWithEntrypoints lets tests analyze a real bootstrap package with the same
// driver as production. Entrypoints are functions, not invented client bindings.
func NewWithEntrypoints(entrypoints []string) *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:       "controllerwatches",
		Doc:        "check that registered Cosmos controllers watch exactly the document types they access",
		FactTypes:  []analysis.Fact{new(PackageFact)},
		ResultType: reflect.TypeOf((*Result)(nil)),
	}
	a.Run = func(pass *analysis.Pass) (any, error) {
		if !strings.HasPrefix(pass.Pkg.Path(), repo) {
			return &Result{}, nil
		}
		fact := summarize(pass)
		pass.ExportPackageFact(fact)
		result := &Result{}
		var roots []string
		for _, id := range entrypoints {
			if fact.Functions[id] != nil {
				roots = append(roots, id)
			}
		}
		if len(roots) == 0 {
			return result, nil
		}
		program := &PackageFact{Functions: map[string]*Function{}, Types: map[string]*Type{}, Globals: map[string]*Expr{}}
		seen := map[string]bool{}
		var visit func(*types.Package)
		visit = func(pkg *types.Package) {
			if seen[pkg.Path()] {
				return
			}
			seen[pkg.Path()] = true
			for _, dep := range pkg.Imports() {
				visit(dep)
			}
			var f PackageFact
			if pkg == pass.Pkg {
				f = *fact
			} else if !pass.ImportPackageFact(pkg, &f) {
				return
			}
			for id, fn := range f.Functions {
				program.Functions[id] = fn
			}
			for id, typ := range f.Types {
				program.Types[id] = typ
			}
			for id, expr := range f.Globals {
				program.Globals[id] = expr
			}
		}
		visit(pass.Pkg)
		result = checkBootstrap(program, roots, pass.Pkg.Path())
		// Full paths are also in Result for standalone output. Keep analyzer
		// diagnostics usable by standard go/analysis drivers and module plugins.
		for _, finding := range result.Findings {
			pos := pass.Files[0].Pos()
			// Prefer a call site in the package owning this diagnostic. The full
			// evidence may live in dependencies and is retained in the message.
			if len(finding.Evidence) > 0 {
				for _, at := range finding.Evidence[0].Path {
					for _, file := range pass.Files {
						if location(pass.Fset, file.Pos(), "").File == at.File {
							tf := pass.Fset.File(file.Pos())
							if at.Line > 0 && at.Line <= tf.LineCount() {
								pos = tf.LineStart(at.Line)
							}
						}
					}
				}
			}
			pass.Report(analysis.Diagnostic{Pos: pos, Category: finding.Invariant, Message: FormatFinding(finding)})
		}
		return result, nil
	}
	return a
}

type builder struct {
	pass             *analysis.Pass
	fact             *PackageFact
	function         string
	reachesEndpoints bool
}

func typeName(t types.Type) string {
	if t == nil {
		return ""
	}
	return types.TypeString(types.Unalias(t), func(p *types.Package) string { return p.Path() })
}

func objectID(obj types.Object) string {
	if obj == nil {
		return ""
	}
	if fn, ok := obj.(*types.Func); ok {
		sig := fn.Type().(*types.Signature)
		if sig.Recv() != nil {
			return baseType(typeName(sig.Recv().Type())) + "." + fn.Name()
		}
	}
	if obj.Pkg() == nil {
		return obj.Name()
	}
	if obj.Parent() == obj.Pkg().Scope() {
		return obj.Pkg().Path() + "." + obj.Name()
	}
	return fmt.Sprintf("%s.%s@%d", obj.Pkg().Path(), obj.Name(), obj.Pos())
}

func summarize(pass *analysis.Pass) *PackageFact {
	f := &PackageFact{Functions: map[string]*Function{}, Types: map[string]*Type{}, Globals: map[string]*Expr{}}
	b := &builder{pass: pass, fact: f, reachesEndpoints: reachesEndpoints(pass.Pkg, map[string]bool{})}
	for _, name := range pass.Pkg.Scope().Names() {
		obj, ok := pass.Pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || obj.IsAlias() {
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			continue
		}
		t := &Type{Fields: map[string]string{}, Methods: map[string]string{}, MethodPaths: map[string][]string{}}
		_, t.Interface = named.Underlying().(*types.Interface)
		for p := range named.TypeParams().Len() {
			t.Params = append(t.Params, named.TypeParams().At(p).Obj().Name())
		}
		if s, ok := named.Underlying().(*types.Struct); ok {
			for i := range s.NumFields() {
				field := s.Field(i)
				t.Fields[field.Name()] = typeName(field.Type())
				if field.Embedded() {
					t.Embedded = append(t.Embedded, field.Name())
				}
			}
		}
		methods := types.NewMethodSet(types.NewPointer(named))
		for i := range methods.Len() {
			m := methods.At(i)
			t.Methods[m.Obj().Name()] = objectID(m.Obj())
			var current types.Type = named
			indices := m.Index()
			for _, index := range indices[:len(indices)-1] {
				if p, ok := current.(*types.Pointer); ok {
					current = p.Elem()
				}
				if s, ok := current.Underlying().(*types.Struct); ok {
					field := s.Field(index)
					t.MethodPaths[m.Obj().Name()] = append(t.MethodPaths[m.Obj().Name()], field.Name())
					current = field.Type()
				}
			}
		}
		// Infer lister/client document types from typed Get/List results, including
		// embedded generic CRUD interfaces. No per-resource name table is needed.
		for _, method := range []string{"Get", "List", "GetByID"} {
			obj, _, _ := types.LookupFieldOrMethod(named, true, pass.Pkg, method)
			if obj == nil {
				continue
			}
			sig, ok := obj.Type().(*types.Signature)
			if !ok || sig.Results().Len() == 0 {
				continue
			}
			if doc := documentType(sig.Results().At(0).Type()); doc != "" {
				t.Document = doc
				break
			}
		}
		f.Types[pass.Pkg.Path()+"."+name] = t
	}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				obj := pass.TypesInfo.Defs[d.Name].(*types.Func)
				b.addFunction(objectID(obj), d.Type, d.Recv, d.Body, obj.Type().(*types.Signature))
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						for i, name := range v.Names {
							if i < len(v.Values) {
								f.Globals[objectID(pass.TypesInfo.Defs[name])] = b.expr(v.Values[i])
							}
						}
					}
				}
			}
		}
	}
	// Keep local forwarding chains to indirect dispatch, even when the package
	// cannot import storage (Run -> worker -> syncer.SyncOnce, for example).
	for changed := true; changed; {
		changed = false
		for _, fn := range f.Functions {
			if fn.MayDispatch {
				continue
			}
			var expr func(*Expr)
			var stmts func([]*Stmt)
			expr = func(x *Expr) {
				if x == nil {
					return
				}
				id := x.Name
				if x.Kind == "method" && len(x.Keys) > 0 {
					id = x.Keys[0]
				}
				if target := f.Functions[id]; target != nil && target.MayDispatch {
					fn.MayDispatch = true
				}
				for _, arg := range x.Args {
					expr(arg)
				}
			}
			stmts = func(body []*Stmt) {
				for _, s := range body {
					for _, x := range s.Right {
						expr(x)
					}
					stmts(s.Body)
					stmts(s.Else)
				}
			}
			stmts(fn.Body)
			if fn.MayDispatch {
				changed = true
			}
		}
	}
	return f
}

func documentType(t types.Type) string {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Pointer:
		return documentType(t.Elem())
	case *types.Slice:
		return documentType(t.Elem())
	case *types.TypeParam:
		return t.Obj().Name()
	case *types.Named:
		name := typeName(t.Obj().Type())
		if strings.HasPrefix(name, repo+"internal/api/") || name == repo+"internal/database/cosmosstorage/billingcosmosstorage.BillingDocument" {
			return typeName(t)
		}
		if strings.Contains(name, "cosmosstorageutils.DBClientIterator") && t.TypeArgs().Len() > 0 {
			return documentType(t.TypeArgs().At(0))
		}
	}
	return ""
}

func (b *builder) addFunction(id string, syntax *ast.FuncType, recv *ast.FieldList, body *ast.BlockStmt, sig *types.Signature) {
	old := b.function
	b.function = id
	defer func() { b.function = old }()
	f := &Function{ID: id, At: location(b.pass.Fset, syntax.Pos(), id)}
	f.ReachesEndpoints = b.reachesEndpoints
	if body != nil {
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if selection := b.pass.TypesInfo.Selections[fun]; selection != nil {
					if _, ok := selection.Recv().Underlying().(*types.Interface); ok {
						f.MayDispatch = true
					}
				}
			case *ast.Ident:
				if obj, ok := b.pass.TypesInfo.Uses[fun].(*types.Var); ok {
					if _, ok := obj.Type().Underlying().(*types.Signature); ok {
						f.MayDispatch = true
					}
				}
			case *ast.IndexExpr:
				if !b.pass.TypesInfo.Types[fun.Index].IsType() {
					f.MayDispatch = true
				}
			case *ast.CallExpr:
				f.MayDispatch = true
			}
			return true
		})
	}
	f.Variadic = sig.Variadic()
	for _, fields := range []*ast.FieldList{recv, syntax.Params} {
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				f.Params = append(f.Params, objectID(b.pass.TypesInfo.Defs[name]))
				f.ParamTypes = append(f.ParamTypes, typeName(b.pass.TypesInfo.TypeOf(field.Type)))
			}
		}
	}
	for _, params := range []*types.TypeParamList{sig.RecvTypeParams(), sig.TypeParams()} {
		for i := range params.Len() {
			f.TypeParams = append(f.TypeParams, params.At(i).Obj().Name())
		}
	}
	for i := range sig.Results().Len() {
		f.Results = append(f.Results, typeName(sig.Results().At(i).Type()))
	}
	f.Body = b.block(body)
	b.fact.Functions[id] = f
}

func (b *builder) expr(node ast.Expr) *Expr {
	if node == nil {
		return &Expr{Kind: "unknown"}
	}
	e := &Expr{Type: typeName(b.pass.TypesInfo.TypeOf(node)), At: location(b.pass.Fset, node.Pos(), b.function)}
	if tv, ok := b.pass.TypesInfo.Types[node]; ok && tv.Value != nil {
		e.Kind = "literal"
		e.Name = tv.Value.ExactString()
		if tv.Value.Kind() == constant.String {
			e.Name = constant.StringVal(tv.Value)
		}
		return e
	}
	switch n := node.(type) {
	case *ast.Ident:
		if n.Name == "nil" {
			e.Kind = "nil"
			return e
		}
		obj := b.pass.TypesInfo.ObjectOf(n)
		e.Name = objectID(obj)
		e.Kind = "var"
		if _, ok := obj.(*types.Func); ok {
			e.Kind = "func"
		}
		if _, ok := obj.(*types.Builtin); ok {
			e.Kind = "func"
		}
		if instance, ok := b.pass.TypesInfo.Instances[n]; ok {
			for i := range instance.TypeArgs.Len() {
				e.TypeArgs = append(e.TypeArgs, typeName(instance.TypeArgs.At(i)))
			}
		}
	case *ast.SelectorExpr:
		if sel := b.pass.TypesInfo.Selections[n]; sel != nil {
			e.Kind = "field"
			e.Name = n.Sel.Name
			e.Args = []*Expr{b.expr(n.X)}
			if sel.Kind() != types.FieldVal {
				e.Kind = "method"
				e.Keys = []string{objectID(sel.Obj())}
			}
		} else {
			return b.expr(n.Sel)
		}
	case *ast.CallExpr:
		e.Kind = "call"
		var call bytes.Buffer
		if _, ok := n.Fun.(*ast.FuncLit); ok {
			e.At.Call = "func literal(...)"
		} else if err := format.Node(&call, b.pass.Fset, n.Fun); err == nil {
			e.At.Call = call.String() + "(...)"
		}
		e.Args = append(e.Args, b.expr(n.Fun))
		for _, arg := range n.Args {
			e.Args = append(e.Args, b.expr(arg))
		}
		if b.pass.TypesInfo.Types[n.Fun].IsType() {
			e.Kind = "convert"
		}
	case *ast.CompositeLit:
		e.Kind = "object"
		var fields *types.Struct
		typ := b.pass.TypesInfo.TypeOf(n)
		if typ != nil {
			fields, _ = typ.Underlying().(*types.Struct)
		}
		for i, arg := range n.Elts {
			key := fmt.Sprint(i)
			if kv, ok := arg.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok {
					key = id.Name
				} else {
					keyExpr := b.expr(kv.Key)
					key = keyExpr.Name
					if keyExpr.Kind != "literal" {
						key = fmt.Sprintf("$dynamic%d", i)
					}
				}
				arg = kv.Value
			} else if fields != nil && i < fields.NumFields() {
				key = fields.Field(i).Name()
			}
			e.Keys = append(e.Keys, key)
			e.Args = append(e.Args, b.expr(arg))
		}
	case *ast.FuncLit:
		e.Kind = "func"
		e.Name = fmt.Sprintf("%s$%d", b.function, b.pass.Fset.Position(n.Pos()).Offset)
		b.addFunction(e.Name, n.Type, nil, n.Body, b.pass.TypesInfo.TypeOf(n).(*types.Signature))
	case *ast.UnaryExpr:
		e.Kind = "unary"
		e.Name = n.Op.String()
		e.Args = []*Expr{b.expr(n.X)}
	case *ast.StarExpr, *ast.ParenExpr:
		if x, ok := n.(*ast.StarExpr); ok {
			return b.expr(x.X)
		}
		return b.expr(n.(*ast.ParenExpr).X)
	case *ast.IndexExpr:
		if b.pass.TypesInfo.Types[n.Index].IsType() {
			return b.expr(n.X)
		}
		e.Kind = "index"
		e.Args = []*Expr{b.expr(n.X), b.expr(n.Index)}
	case *ast.IndexListExpr:
		return b.expr(n.X)
	case *ast.TypeAssertExpr:
		e.Kind = "convert"
		e.Args = []*Expr{b.expr(n.X)}
	case *ast.BinaryExpr:
		e.Kind = "binary"
		e.Name = n.Op.String()
		e.Args = []*Expr{b.expr(n.X), b.expr(n.Y)}
	case *ast.SliceExpr:
		return b.expr(n.X)
	default:
		e.Kind = "unknown"
	}
	return e
}

func (b *builder) block(block *ast.BlockStmt) []*Stmt {
	if block == nil {
		return nil
	}
	return b.statements(block.List)
}

func (b *builder) statements(nodes []ast.Stmt) []*Stmt {
	var out []*Stmt
	for _, node := range nodes {
		s := &Stmt{At: location(b.pass.Fset, node.Pos(), b.function)}
		switch n := node.(type) {
		case *ast.AssignStmt:
			s.Kind = "assign"
			for _, e := range n.Lhs {
				s.Left = append(s.Left, b.expr(e))
			}
			for _, e := range n.Rhs {
				s.Right = append(s.Right, b.expr(e))
			}
		case *ast.DeclStmt:
			if gen, ok := n.Decl.(*ast.GenDecl); ok {
				for _, spec := range gen.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						a := &Stmt{Kind: "assign", At: s.At}
						for _, name := range v.Names {
							a.Left = append(a.Left, b.expr(name))
						}
						for _, val := range v.Values {
							a.Right = append(a.Right, b.expr(val))
						}
						out = append(out, a)
					}
				}
			}
			continue
		case *ast.ExprStmt:
			s.Kind = "expr"
			s.Right = []*Expr{b.expr(n.X)}
		case *ast.GoStmt:
			s.Kind = "expr"
			s.Right = []*Expr{b.expr(n.Call)}
		case *ast.DeferStmt:
			s.Kind = "expr"
			s.Right = []*Expr{b.expr(n.Call)}
		case *ast.ReturnStmt:
			s.Kind = "return"
			for _, e := range n.Results {
				s.Right = append(s.Right, b.expr(e))
			}
		case *ast.IfStmt:
			if n.Init != nil {
				out = append(out, b.statements([]ast.Stmt{n.Init})...)
			}
			s.Kind = "if"
			s.Right = []*Expr{b.expr(n.Cond)}
			s.Body = b.block(n.Body)
			if n.Else != nil {
				s.Else = b.statements([]ast.Stmt{n.Else})
			}
		case *ast.RangeStmt:
			s.Kind = "range"
			s.Left = []*Expr{b.expr(n.Key), b.expr(n.Value)}
			s.Right = []*Expr{b.expr(n.X)}
			s.Body = b.block(n.Body)
		case *ast.ForStmt:
			if n.Init != nil {
				out = append(out, b.statements([]ast.Stmt{n.Init})...)
			}
			s.Kind = "block"
			s.Body = b.block(n.Body)
			if n.Cond != nil {
				s.Right = []*Expr{b.expr(n.Cond)}
			}
			if n.Post != nil {
				s.Body = append(s.Body, b.statements([]ast.Stmt{n.Post})...)
			}
		case *ast.BlockStmt:
			s.Kind = "block"
			s.Body = b.block(n)
		case *ast.SwitchStmt:
			if n.Init != nil {
				out = append(out, b.statements([]ast.Stmt{n.Init})...)
			}
			s.Kind = "block"
			s.Right = []*Expr{b.expr(n.Tag)}
			s.Body = b.block(n.Body)
		case *ast.TypeSwitchStmt:
			if n.Init != nil {
				out = append(out, b.statements([]ast.Stmt{n.Init})...)
			}
			out = append(out, b.statements([]ast.Stmt{n.Assign})...)
			s.Kind = "block"
			s.Body = b.block(n.Body)
		case *ast.CaseClause:
			s.Kind = "branch"
			s.Body = b.statements(n.Body)
		case *ast.SelectStmt:
			s.Kind = "block"
			s.Body = b.block(n.Body)
		case *ast.CommClause:
			s.Kind = "branch"
			if n.Comm != nil {
				s.Body = b.statements([]ast.Stmt{n.Comm})
			}
			s.Body = append(s.Body, b.statements(n.Body)...)
		case *ast.LabeledStmt:
			out = append(out, b.statements([]ast.Stmt{n.Stmt})...)
			continue
		default:
			continue
		}
		out = append(out, s)
	}
	return out
}
