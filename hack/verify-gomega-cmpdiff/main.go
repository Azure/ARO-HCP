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

// verify-gomega-cmpdiff requires diff-producing matchers for structured values.
package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"slices"

	"golang.org/x/tools/go/packages"

	"k8s.io/apimachinery/pkg/util/sets"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: verify-gomega-cmpdiff <package pattern> [<package pattern> ...]")
		os.Exit(2)
	}
	pkgs, err := packages.Load(&packages.Config{
		Mode:       packages.LoadSyntax,
		Tests:      true,
		BuildFlags: []string{"-tags=E2Etests"},
	}, os.Args[1:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	violations, err := checkPackages(pkgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, violation := range violations {
		fmt.Fprintln(os.Stderr, violation)
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
}

func checkPackages(pkgs []*packages.Package) ([]string, error) {
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages matched")
	}
	var loadErrors []string
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, err := range pkg.Errors {
			loadErrors = append(loadErrors, err.Error())
		}
	})
	if len(loadErrors) > 0 {
		return nil, fmt.Errorf("cannot check packages with load/type errors: %v", loadErrors)
	}

	var violations []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				var ident *ast.Ident
				switch fun := ast.Unparen(call.Fun).(type) {
				case *ast.Ident:
					ident = fun
				case *ast.SelectorExpr:
					ident = fun.Sel
				default:
					return true
				}
				fn, ok := pkg.TypesInfo.Uses[ident].(*types.Func)
				if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/onsi/gomega" || fn.Type().(*types.Signature).Recv() != nil {
					return true
				}
				if (fn.Name() == "Equal" || fn.Name() == "BeEquivalentTo") && containsStruct(pkg.TypesInfo.TypeOf(call.Args[0]), sets.New[types.Type]()) {
					violations = append(violations, fmt.Sprintf("%s: use BeComparableTo instead of %s for structured values to report cmp.Diff on failure (see test/AGENTS.md)", pkg.Fset.Position(call.Pos()), fn.Name()))
				}
				return true
			})
		}
	}
	// Tests produce package variants that can contain the same source files.
	slices.Sort(violations)
	return slices.Compact(violations), nil
}

func containsStruct(t types.Type, seen sets.Set[types.Type]) bool {
	if t == nil || seen.Has(t) {
		return false
	}
	seen.Insert(t)
	switch t := t.Underlying().(type) {
	case *types.Struct:
		return true
	case *types.Pointer:
		return containsStruct(t.Elem(), seen)
	case *types.Array:
		return containsStruct(t.Elem(), seen)
	case *types.Slice:
		return containsStruct(t.Elem(), seen)
	case *types.Map:
		return containsStruct(t.Key(), seen) || containsStruct(t.Elem(), seen)
	default:
		return false
	}
}
