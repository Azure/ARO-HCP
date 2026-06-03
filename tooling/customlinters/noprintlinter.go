// Copyright 2025 Microsoft Corporation
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

package customlinters

import (
	"go/ast"
	"strings"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

var _ register.LinterPlugin = (*NoPrintLinter)(nil)

type NoPrintLinter struct{}

func (l *NoPrintLinter) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{{
		Name: "noprint",
		Doc:  "detects print statements to standard output in test files",
		Run:  l.run,
	}}, nil
}

func (l *NoPrintLinter) GetLoadMode() string {
	return register.LoadModeTypesInfo
}

func (l *NoPrintLinter) run(pass *analysis.Pass) (any, error) {
	// Map of forbidden functions by package
	forbiddenFuncs := map[string][]string{
		"fmt": {"Print", "Printf", "Println"},
		"log": {"Print", "Printf", "Println"},
	}

	for _, file := range pass.Files {
		// Only check files in test directories or testdata (for testing the linter itself)
		filename := pass.Fset.Position(file.Pos()).Filename
		if !strings.Contains(filename, "/test/") && !strings.Contains(filename, "/testdata/") {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Check for selector expressions (e.g., fmt.Println, log.Print)
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok {
					pkgName := ident.Name
					funcName := sel.Sel.Name

					if funcs, exists := forbiddenFuncs[pkgName]; exists {
						for _, forbidden := range funcs {
							if funcName == forbidden {
								pass.Reportf(call.Pos(),
									"do not use %s.%s in test files - use Ginkgo's GinkgoLogr or GinkgoWriter instead",
									pkgName, funcName)
								return true
							}
						}
					}
				}
			}

			// Check for builtin println
			if ident, ok := call.Fun.(*ast.Ident); ok {
				if ident.Name == "println" || ident.Name == "print" {
					pass.Reportf(call.Pos(),
						"do not use builtin %s in test files - use Ginkgo's GinkgoLogr or GinkgoWriter instead",
						ident.Name)
				}
			}

			return true
		})
	}

	return nil, nil
}

func NewNoPrintLinter(any) (register.LinterPlugin, error) {
	return &NoPrintLinter{}, nil
}

func init() {
	register.Plugin("noprint", NewNoPrintLinter)
}
