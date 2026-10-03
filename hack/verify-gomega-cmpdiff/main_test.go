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

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

type testImporter map[string]*types.Package

func (i testImporter) Import(path string) (*types.Package, error) {
	if pkg, ok := i[path]; ok {
		return pkg, nil
	}
	return nil, fmt.Errorf("unexpected import %q", path)
}

func TestCheckPackages(t *testing.T) {
	imports := testImporter{}
	for _, path := range []string{"github.com/onsi/gomega", "example.com/other"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "stub.go", `package stub
type Resource struct { Name string }
type Enum string
func Equal(any) any { return nil }
func BeEquivalentTo(any) any { return nil }
func BeComparableTo(any) any { return nil }
func Not(any) any { return nil }
func HaveValue(any) any { return nil }
func (Resource) Equal(any) any { return nil }
`, 0)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := new(types.Config).Check(path, fset, []*ast.File{file}, nil)
		if err != nil {
			t.Fatal(err)
		}
		imports[path] = pkg
	}

	for _, tc := range []struct {
		name string
		expr string
		want bool
	}{
		{"literal", "g.Equal(struct{ Name string }{})", true},
		{"local", "g.Equal(Local{})", true},
		{"alias", "g.Equal(Alias{})", true},
		{"imported", "g.Equal(other.Resource{})", true},
		{"pointer", "g.BeEquivalentTo(&other.Resource{})", true},
		{"typed nil", "g.Equal((*Local)(nil))", true},
		{"variable", "g.Equal(want)", true},
		{"function result", "g.Equal(resource())", true},
		{"array", "g.Equal([2]Local{})", true},
		{"slice", "g.Equal([]*Local{})", true},
		{"named collection", "g.Equal(Resources{})", true},
		{"map value", "g.Equal(map[string][]*Local{})", true},
		{"map key", "g.Equal(map[Local]string{})", true},
		{"nested", "g.Not(g.HaveValue(g.Equal(Local{})))", true},
		{"parenthesized", "(g.Equal)(Local{})", true},
		{"recommended", "g.BeComparableTo(Local{})", false},
		{"scalar", "g.Equal(42)", false},
		{"enum", "g.BeEquivalentTo(other.Enum(\"ready\"))", false},
		{"enum slice", "g.Equal([]other.Enum{})", false},
		{"scalar array", "g.Equal([2]string{})", false},
		{"scalar map", "g.Equal(map[string][]int{})", false},
		{"scalar pointer", "g.Equal((*string)(nil))", false},
		{"nil", "g.Equal(nil)", false},
		{"interface limitation", "g.Equal(any(Local{}))", false},
		{"interface collection limitation", "g.Equal([]any{Local{}})", false},
		{"recursive scalar collection", "g.Equal(Recursive{})", false},
		{"recursive structured collection", "g.Equal(RecursiveStruct{})", true},
		{"unrelated package", "other.Equal(Local{})", false},
		{"unrelated method", "other.Resource{}.Equal(Local{})", false},
		{"gomega method", "g.Resource{}.Equal(Local{})", false},
		{"local function", "func() any { Equal := func(any) any { return nil }; return Equal(Local{}) }()", false},
	} {
		for _, alias := range []string{".", "g", "gomega"} {
			t.Run(tc.name+"/"+alias, func(t *testing.T) {
				prefix := ""
				if alias != "." {
					prefix = alias + "."
				}
				expr := strings.ReplaceAll(tc.expr, "g.", prefix)
				source := fmt.Sprintf(`package example
import %s "github.com/onsi/gomega"
import other "example.com/other"
type Local struct { Name string }
type Alias = other.Resource
type Resources []*Local
type Recursive []Recursive
type RecursiveStruct map[*Local]RecursiveStruct
var want Local
func resource() Local { return want }
var _ = other.Enum("")
var _ = %sBeComparableTo
var _ = %s
`, alias, prefix, expr)
				fset := token.NewFileSet()
				file, err := parser.ParseFile(fset, "example.go", source, 0)
				if err != nil {
					t.Fatal(err)
				}
				info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
				config := types.Config{Importer: imports}
				if _, err := config.Check("example.com/test", fset, []*ast.File{file}, info); err != nil {
					t.Fatal(err)
				}
				pkg := &packages.Package{Fset: fset, Syntax: []*ast.File{file}, TypesInfo: info}
				// Duplicate package variants must not duplicate diagnostics.
				got, err := checkPackages([]*packages.Package{pkg, pkg})
				if err != nil {
					t.Fatal(err)
				}
				wantCount := 0
				if tc.want {
					wantCount = 1
				}
				if len(got) != wantCount {
					t.Fatalf("got %v, want %d violations", got, wantCount)
				}
				if tc.want && (!strings.HasPrefix(got[0], "example.go:") || !strings.Contains(got[0], "use BeComparableTo") || !strings.Contains(got[0], "cmp.Diff")) {
					t.Fatalf("missing location or actionable diagnostic: %s", got[0])
				}
			})
		}
	}
}

func TestPackageErrors(t *testing.T) {
	broken := &packages.Package{Errors: []packages.Error{{Msg: "broken package"}}}
	for _, pkgs := range [][]*packages.Package{
		nil,
		{broken},
		{{Imports: map[string]*packages.Package{"broken": broken}}},
	} {
		if _, err := checkPackages(pkgs); err == nil {
			t.Fatal("expected unmatched packages or package errors to fail verification")
		}
	}
}
