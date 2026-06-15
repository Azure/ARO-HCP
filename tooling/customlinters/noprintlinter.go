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
	"bytes"
	"go/ast"
	"os"
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

// FileCtx holds per-file state for the noprint linter.
type FileCtx struct {
	pass     *analysis.Pass
	file     *ast.File
	src      []byte
	disabled map[int]bool // line number -> true if the check is disabled for that line
}

// buildDisabledMap pre-computes which lines are suppressed via directives.
// A directive on the same line disables that line.
// A directive on the previous line disables the next line only if the comment is standalone
// (i.e. no code precedes it on that line).
//
// Supported directives:
//
//	// nolint:noprint
//	// noprint:ignore
func (fc *FileCtx) buildDisabledMap() {
	fc.disabled = make(map[int]bool)
	if fc.file == nil {
		return
	}
	for _, cg := range fc.file.Comments {
		for _, c := range cg.List {
			txt := c.Text
			if !strings.Contains(txt, "nolint:noprint") && !strings.Contains(txt, "noprint:ignore") {
				continue
			}
			cp := fc.pass.Fset.PositionFor(c.Pos(), true)
			fc.disabled[cp.Line] = true
			if isNoprintStandaloneComment(fc.src, fc.pass, c) {
				fc.disabled[cp.Line+1] = true
			}
		}
	}
}

// isDisabled returns true if the starting line of node is marked as disabled.
func (fc *FileCtx) isDisabled(node ast.Node) bool {
	if node == nil {
		return false
	}
	line := fc.pass.Fset.PositionFor(node.Pos(), true).Line
	return fc.disabled[line]
}

// isNoprintStandaloneComment returns true if the comment starts on a line that contains
// only whitespace before the comment token.
func isNoprintStandaloneComment(src []byte, pass *analysis.Pass, c *ast.Comment) bool {
	cp := pass.Fset.PositionFor(c.Pos(), true)
	tf := pass.Fset.File(c.Pos())
	if tf == nil {
		return false
	}
	if src == nil {
		return cp.Column == 1
	}
	lineStart := pass.Fset.PositionFor(tf.LineStart(cp.Line), true)
	if lineStart.Offset < 0 || cp.Offset < lineStart.Offset || cp.Offset > len(src) {
		return false
	}
	prefix := src[lineStart.Offset:cp.Offset]
	return len(bytes.TrimSpace(prefix)) == 0
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

		src, _ := os.ReadFile(filename) // best-effort; nil on failure is fine
		fc := &FileCtx{pass: pass, file: file, src: src}
		fc.buildDisabledMap()

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			if fc.isDisabled(call) {
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
