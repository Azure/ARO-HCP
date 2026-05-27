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

// verify-gomega-assertions walks Go source files under the given directories
// and reports any gomega assertions that are missing a descriptive context
// string. Every Expect().To/NotTo/ToNot/Should/ShouldNot() call must include
// an annotation argument so that test failures are self-describing. For example:
//
//	Expect(err).NotTo(HaveOccurred())                          // flagged
//	Expect(err).NotTo(HaveOccurred(), "failed to create cluster") // OK
//	Expect(x).To(Equal(42))                                    // flagged
//	Expect(x).To(Equal(42), "widget count should be 42")       // OK
//
// Exit code is 1 if any violations are found.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// assertionMethods are the gomega assertion methods that accept
// (matcher, ...annotation) arguments.
var assertionMethods = map[string]bool{
	"To":        true,
	"NotTo":     true,
	"ToNot":     true,
	"Should":    true,
	"ShouldNot": true,
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: verify-gomega-assertions <dir> [<dir> ...]")
		os.Exit(2)
	}

	var violations []string
	for _, dir := range dirs {
		v, err := checkDir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error processing %s: %v\n", dir, err)
			os.Exit(2)
		}
		violations = append(violations, v...)
	}

	if len(violations) > 0 {
		fmt.Println("ERROR: The following gomega assertions are missing a descriptive context string.")
		fmt.Println("       Every Expect(...).To/NotTo/Should/ShouldNot(matcher) call must include an")
		fmt.Println("       annotation describing what is being checked, e.g.:")
		fmt.Println()
		fmt.Println(`         Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")`)
		fmt.Println(`         Expect(x).To(Equal(42), "widget count should be 42")`)
		fmt.Println()
		for _, v := range violations {
			fmt.Println("  " + v)
		}
		fmt.Println()
		fmt.Println("Add a context string to each assertion listed above.")
		fmt.Println("See test/AGENTS.md '## Assertion Messages' for guidance.")
		os.Exit(1)
	}
}

func checkDir(dir string) ([]string, error) {
	var violations []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		v, err := checkFile(path)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		violations = append(violations, v...)
		return nil
	})
	return violations, err
}

func checkFile(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// We're looking for: Expect(...).To(matcher) or similar
		// The outer call is .To/.NotTo/.ToNot/.Should/.ShouldNot(matcher, [optionalAnnotation...])
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if !assertionMethods[sel.Sel.Name] {
			return true
		}

		// The receiver must be an Expect() call
		if !isExpectCall(sel.X) {
			return true
		}

		// Must have at least the matcher argument
		if len(call.Args) == 0 {
			return true
		}

		// If there's only one argument (the matcher) and no annotation, flag it
		if len(call.Args) < 2 {
			pos := fset.Position(call.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d: Expect(...).%s(matcher) is missing an annotation string", pos.Filename, pos.Line, sel.Sel.Name))
		}

		return true
	})

	return violations, nil
}

// isExpectCall checks whether expr is a call to Expect (with any arguments).
func isExpectCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "Expect"
}
