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

// verify-mi-containers walks Go source files under the given directories
// (skipping _test.go files and known non-spec files like setup.go and
// e2e_test.go) and reports any It() or DescribeTable() blocks that are
// missing a labels.MIContainers(N) decorator. It also verifies that the
// declared container count matches the integer literal passed to
// AssignIdentityContainers inside the test function body. It also enforces
// that the one intentional resource-scoped RBAC test remains the complete
// customer-resource-group deletion lifecycle test.
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
	"strconv"
	"strings"
)

var skipFiles = map[string]bool{
	"setup.go":     true,
	"e2e_test.go":  true,
	"e2e_suite.go": true,
}

const (
	resourceScopedTestFile = "cluster_delete_cx_rg.go"
	resourceScopedTestName = "should be able to create an HCP cluster then delete it by deleting the customer resource group"
)

type resourceScopedSpec struct {
	file string
	line int
	kind string
	name string
	uses int
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: verify-mi-containers <dir> [<dir> ...]")
		os.Exit(2)
	}

	var violations []string
	var resourceScopedSpecs []resourceScopedSpec
	for _, dir := range dirs {
		v, err := checkDir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error processing %s: %v\n", dir, err)
			os.Exit(2)
		}
		violations = append(violations, v...)

		specs, err := findResourceScopedSpecs(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error processing resource-scoped specs in %s: %v\n", dir, err)
			os.Exit(2)
		}
		resourceScopedSpecs = append(resourceScopedSpecs, specs...)
	}
	violations = append(violations, validateResourceScopedSpecs(resourceScopedSpecs)...)

	if len(violations) > 0 {
		fmt.Fprint(os.Stderr, formatViolations(violations))
		os.Exit(1)
	}
}

func formatViolations(violations []string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "ERROR: %d E2E test invariant violation(s):\n", len(violations))
	for _, violation := range violations {
		fmt.Fprintf(&output, "  - %s\n", violation)
	}
	return output.String()
}

func findResourceScopedSpecs(dir string) ([]resourceScopedSpec, error) {
	var specs []resourceScopedSpec
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		base := filepath.Base(path)
		if skipFiles[base] || strings.HasSuffix(base, "_test.go") {
			return nil
		}

		found, err := findResourceScopedSpecsInFile(path)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		specs = append(specs, found...)
		return nil
	})
	return specs, err
}

func findResourceScopedSpecsInFile(path string) ([]resourceScopedSpec, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}

	var specs []resourceScopedSpec
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		kind := identifySpecCall(call)
		uses := countResourceScopedRBACUses(call)
		if kind == "" || uses == 0 {
			return true
		}
		specs = append(specs, resourceScopedSpec{
			file: path,
			line: fset.Position(call.Pos()).Line,
			kind: kind,
			name: extractSpecName(call),
			uses: uses,
		})
		return true
	})
	return specs, nil
}

func countResourceScopedRBACUses(specCall *ast.CallExpr) int {
	count := 0
	for _, arg := range specCall.Args {
		funcLit, ok := arg.(*ast.FuncLit)
		if !ok {
			continue
		}
		ast.Inspect(funcLit, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RBACScopeResource" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "framework" {
				count++
			}
			return true
		})
	}
	return count
}

func validateResourceScopedSpecs(specs []resourceScopedSpec) []string {
	if len(specs) != 1 {
		var locations []string
		for _, spec := range specs {
			locations = append(locations, fmt.Sprintf("%s:%d: %s(%q)", spec.file, spec.line, spec.kind, spec.name))
		}
		foundLocations := ""
		if len(locations) > 0 {
			foundLocations = ": " + strings.Join(locations, ", ")
		}
		return []string{fmt.Sprintf(
			"expected exactly one framework.RBACScopeResource spec (%s: It(%q)); found %d%s",
			resourceScopedTestFile,
			resourceScopedTestName,
			len(specs),
			foundLocations,
		)}
	}

	spec := specs[0]
	if filepath.Base(spec.file) != resourceScopedTestFile || spec.kind != "It" || spec.name != resourceScopedTestName {
		return []string{fmt.Sprintf(
			"%s:%d: %s(%q) uses framework.RBACScopeResource; expected %s: It(%q)",
			spec.file,
			spec.line,
			spec.kind,
			spec.name,
			resourceScopedTestFile,
			resourceScopedTestName,
		)}
	}
	if spec.uses != 1 {
		return []string{fmt.Sprintf(
			"%s:%d: %s(%q) uses framework.RBACScopeResource %d times; expected exactly once",
			spec.file,
			spec.line,
			spec.kind,
			spec.name,
			spec.uses,
		)}
	}
	return nil
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
		base := filepath.Base(path)
		if skipFiles[base] || strings.HasSuffix(base, "_test.go") {
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

		specKind := identifySpecCall(call)
		if specKind == "" {
			return true
		}

		specName := extractSpecName(call)
		pos := fset.Position(call.Pos())
		label := fmt.Sprintf("%s:%d: %s(%q)", pos.Filename, pos.Line, specKind, specName)

		declared, hasMILabel, labelCount := findMIContainersLabel(call)
		assignCount, hasAssignCall, hasAssignLiteralCount := findAssignIdentityContainersInfo(call)

		if !hasMILabel {
			violations = append(violations, label+" is missing labels.MIContainers(N) decorator")
			return true
		}

		if labelCount > 1 {
			violations = append(violations, fmt.Sprintf("%s has %d MIContainers labels; exactly one is required", label, labelCount))
			return true
		}

		if declared < 0 {
			violations = append(violations, fmt.Sprintf("%s has MIContainers(%d) but N must be >= 0", label, declared))
			return true
		}

		if declared == 0 && hasAssignCall {
			violations = append(violations, fmt.Sprintf("%s has MIContainers(0) but calls AssignIdentityContainers", label))
		} else if declared > 0 && hasAssignLiteralCount && declared != assignCount {
			violations = append(violations, fmt.Sprintf("%s has MIContainers(%d) but calls AssignIdentityContainers with count=%d", label, declared, assignCount))
		} else if declared > 0 && !hasAssignCall {
			violations = append(violations, fmt.Sprintf("%s has MIContainers(%d) but does not call AssignIdentityContainers", label, declared))
		}

		return true
	})

	return violations, nil
}

func identifySpecCall(call *ast.CallExpr) string {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	switch ident.Name {
	case "It":
		return "It"
	case "DescribeTable":
		return "DescribeTable"
	default:
		return ""
	}
}

func extractSpecName(call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return "<unknown>"
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "<unknown>"
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return lit.Value
	}
	return s
}

func findMIContainersLabel(call *ast.CallExpr) (declared int, found bool, labelCount int) {
	for _, arg := range call.Args {
		argCall, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := argCall.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "labels" || sel.Sel.Name != "MIContainers" {
			continue
		}
		if len(argCall.Args) != 1 {
			continue
		}
		if n, ok := parseIntArg(argCall.Args[0]); ok {
			labelCount++
			if !found {
				declared = n
				found = true
			}
		}
	}
	return declared, found, labelCount
}

func parseIntArg(expr ast.Expr) (int, bool) {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.INT {
		n, err := strconv.Atoi(lit.Value)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.SUB {
		if lit, ok := unary.X.(*ast.BasicLit); ok && lit.Kind == token.INT {
			n, err := strconv.Atoi(lit.Value)
			if err != nil {
				return 0, false
			}
			return -n, true
		}
	}
	return 0, false
}

func findAssignIdentityContainersInfo(specCall *ast.CallExpr) (count int, hasCall bool, hasLiteralCount bool) {
	for _, arg := range specCall.Args {
		funcLit, ok := arg.(*ast.FuncLit)
		if !ok {
			continue
		}
		ast.Inspect(funcLit, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isAssignIdentityContainersCall(call) {
				hasCall = true
				if c, ok := extractSecondIntArg(call); ok {
					count = c
					hasLiteralCount = true
				}
			}
			return true
		})
	}
	return count, hasCall, hasLiteralCount
}

func isAssignIdentityContainersCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if ok && sel.Sel.Name == "AssignIdentityContainers" {
		return true
	}
	ident, ok := call.Fun.(*ast.Ident)
	if ok && ident.Name == "AssignIdentityContainers" {
		return true
	}
	return false
}

func extractSecondIntArg(call *ast.CallExpr) (int, bool) {
	if len(call.Args) < 2 {
		return 0, false
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	return n, true
}
