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

// Package priorityrefresh maps priority proposals onto the exact StartPriority
// calls in the E2E test sources. Planning is process-local and side-effect free:
// nothing is written until Apply, and a single unmappable proposal aborts the
// whole batch.
package priorityrefresh

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// labelsImportPath is the only package whose StartPriority calls are edited.
const labelsImportPath = "github.com/Azure/ARO-HCP/test/util/labels"

// e2eDirectory is the only subtree this package may edit.
const e2eDirectory = "test/e2e"

// leafCallNames are the Ginkgo leaf declarations that declare a test spec.
var leafCallNames = map[string]bool{"It": true, "Entry": true, "Specify": true}

// containerCallNames are the Ginkgo containers whose decorators a spec inherits.
var containerCallNames = map[string]bool{"Describe": true, "Context": true, "DescribeTable": true}

// textEdit replaces the byte range [start:end) of a file.
type textEdit struct {
	start int
	end   int
	text  string
}

// fileContext is one parsed source file that may receive edits.
type fileContext struct {
	absPath    string
	relPath    string
	source     []byte
	mode       fs.FileMode
	digest     [sha256.Size]byte
	fset       *token.FileSet
	file       *ast.File
	parents    map[ast.Node]ast.Node
	labelsName string
	dotImport  bool
	leaves     []leafDecl
	edits      []textEdit
}

// sourceCall is the resolved source form of one spec's start priority.
type sourceCall struct {
	file     *fileContext
	leaf     *ast.CallExpr
	table    *ast.CallExpr
	node     *ast.CallExpr
	value    int
	onTable  bool
	insertAt int
}

// loadFile parses one source file and resolves how the labels package is named.
func loadFile(repo, absPath string) (*fileContext, error) {
	source, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", absPath, err)
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", absPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; refusing to edit it", absPath)
	}
	if generated(source) {
		return nil, fmt.Errorf("%s is generated; refusing to edit it", absPath)
	}
	formatted, err := format.Source(source)
	if err != nil {
		return nil, fmt.Errorf("format %s: %w", absPath, err)
	}
	if string(formatted) != string(source) {
		return nil, fmt.Errorf("%s is not gofmt-formatted; refusing to edit it", absPath)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", absPath, err)
	}
	relPath, err := filepath.Rel(repo, absPath)
	if err != nil {
		return nil, fmt.Errorf("resolve %s relative to the checkout: %w", absPath, err)
	}
	context := &fileContext{
		absPath: absPath,
		relPath: filepath.ToSlash(relPath),
		source:  source,
		mode:    info.Mode().Perm(),
		digest:  sha256.Sum256(source),
		fset:    fset,
		file:    file,
		parents: map[ast.Node]ast.Node{},
	}
	name, dot, found := labelsImport(file)
	if found {
		context.labelsName, context.dotImport = name, dot
	}
	buildParents(file, context.parents)
	context.indexLeaves()
	return context, nil
}

// ensureNoSymlinkComponents rejects any symlinked path component between the
// checkout root and the file, so an edit can never leave the checkout.
func ensureNoSymlinkComponents(repo, absPath string) error {
	relative, err := filepath.Rel(repo, absPath)
	if err != nil {
		return fmt.Errorf("resolve %s within the checkout: %w", absPath, err)
	}
	current := repo
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("stat %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s contains the symlink %s; refusing to edit it", absPath, current)
		}
	}
	return nil
}

// commentBetween reports whether any comment overlaps the byte range.
func (f *fileContext) commentBetween(start, end int) bool {
	for _, group := range f.file.Comments {
		groupStart := f.offset(group.Pos())
		groupEnd := f.offset(group.End())
		if groupStart < end && groupEnd > start {
			return true
		}
	}
	return false
}

// generated reports whether a file carries the standard generated-code marker.
func generated(source []byte) bool {
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
		if strings.HasPrefix(trimmed, "// Code generated ") && strings.HasSuffix(trimmed, "DO NOT EDIT.") {
			return true
		}
	}
	return false
}

// labelsImport returns the local name of the repository labels import.
func labelsImport(file *ast.File) (string, bool, bool) {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != labelsImportPath {
			continue
		}
		if imp.Name == nil {
			return "labels", false, true
		}
		switch imp.Name.Name {
		case ".":
			return "", true, true
		case "_":
			return "", false, false
		default:
			return imp.Name.Name, false, true
		}
	}
	return "", false, false
}

// buildParents records each node's parent so decorators can be traced upward.
func buildParents(root ast.Node, parents map[ast.Node]ast.Node) {
	var stack []ast.Node
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
}

// offset converts a token position to a byte offset in the file.
func (f *fileContext) offset(pos token.Pos) int {
	return f.fset.Position(pos).Offset
}

// leafDecl is one leaf declaration indexed for lookups.
type leafDecl struct {
	call        *ast.CallExpr
	line        int
	description string
}

// indexLeaves records every leaf declaration in the file once.
func (f *fileContext) indexLeaves() {
	f.leaves = nil
	ast.Inspect(f.file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !leafCallNames[callName(call)] || len(call.Args) == 0 {
			return true
		}
		description, ok := stringLiteral(call.Args[0])
		if !ok {
			return true
		}
		f.leaves = append(f.leaves, leafDecl{call: call, line: f.fset.Position(call.Pos()).Line, description: description})
		return true
	})
}

// identityPattern returns the pattern a catalog name must match for one leaf:
// literal container fragments anchored in order, computed fragments as
// wildcards, and the leaf description anchored at the end. Anchoring the known
// fragments rejects a renamed container even when a sibling is computed, which a
// suffix comparison cannot.
func (f *fileContext) identityPattern(leaf *ast.CallExpr, description string) *regexp.Regexp {
	var containers []ast.Expr
	for current := f.parents[leaf]; current != nil; current = f.parents[current] {
		call, ok := current.(*ast.CallExpr)
		if !ok || !containerCallNames[callName(call)] || len(call.Args) == 0 {
			continue
		}
		containers = append(containers, call.Args[0])
	}
	for i, j := 0, len(containers)-1; i < j; i, j = i+1, j-1 {
		containers[i], containers[j] = containers[j], containers[i]
	}
	parts := make([]string, 0, len(containers)+1)
	for _, container := range containers {
		parts = append(parts, nodePattern(container))
	}
	parts = append(parts, regexp.QuoteMeta(description))
	return regexp.MustCompile("^" + strings.Join(parts, " ") + "$")
}

// nodePattern renders one description expression, quoting its literal text and
// replacing every computed fragment with a wildcard.
func nodePattern(expression ast.Expr) string {
	if text, ok := stringLiteral(expression); ok {
		return regexp.QuoteMeta(text)
	}
	if binary, ok := expression.(*ast.BinaryExpr); ok && binary.Op == token.ADD {
		return nodePattern(binary.X) + nodePattern(binary.Y)
	}
	return ".*"
}

// matchesName reports whether the resolved declaration hierarchy names the
// catalog spec.
func (f *fileContext) matchesName(leaf *ast.CallExpr, catalogName, description string) bool {
	return f.identityPattern(leaf, description).MatchString(catalogName)
}

// locateAt finds the unique leaf declaration at the annotated line.
func (f *fileContext) locateAt(line int, catalogName string) (*ast.CallExpr, bool) {
	var found *ast.CallExpr
	for _, leaf := range f.leaves {
		if leaf.line != line || !f.matchesName(leaf.call, catalogName, leaf.description) {
			continue
		}
		if found != nil && found != leaf.call {
			return nil, false
		}
		found = leaf.call
	}
	return found, found != nil
}

// locateByName finds the unique leaf declaration whose description matches a
// catalog name, independent of line numbers shifted by edits.
func (f *fileContext) locateByName(catalogName string) (*ast.CallExpr, bool) {
	var found *ast.CallExpr
	for _, leaf := range f.leaves {
		if !f.matchesName(leaf.call, catalogName, leaf.description) {
			continue
		}
		if found != nil && found != leaf.call {
			return nil, false
		}
		found = leaf.call
	}
	return found, found != nil
}

// callName returns the identifier a call is made through, if any.
func callName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	}
	return ""
}

// stringLiteral returns the value of a plain string literal expression.
func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// startPriorityIn finds the single direct StartPriority decorator of a
// declaration. Only the declaration's own arguments are inspected: function
// literals are the test body, so a call inside one is never a decorator. Spread
// arguments and repeated decorators are rejected rather than rewritten, because
// neither can be mapped onto one catalog label.
func (f *fileContext) startPriorityIn(call *ast.CallExpr) (*ast.CallExpr, int, error) {
	if f.labelsName == "" && !f.dotImport {
		return nil, 0, nil
	}
	if call.Ellipsis.IsValid() {
		return nil, 0, fmt.Errorf("%s:%d declaration spreads its arguments; refusing to edit it",
			f.relPath, f.fset.Position(call.Pos()).Line)
	}
	var found *ast.CallExpr
	matches := 0
	for _, argument := range call.Args[1:] {
		ast.Inspect(argument, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.FuncLit:
				return false
			case *ast.CallExpr:
				// A nested leaf declaration (a table's Entry rows) owns its own
				// priority; it must never be read as a table-level decorator.
				if leafCallNames[callName(current)] {
					return false
				}
				if f.isStartPriority(current) {
					matches++
					found = current
				}
			}
			return true
		})
	}
	switch {
	case matches == 0:
		return nil, 0, nil
	case matches > 1:
		return nil, 0, fmt.Errorf("%s:%d declares StartPriority more than once; refusing to edit it",
			f.relPath, f.fset.Position(call.Pos()).Line)
	}
	if len(found.Args) != 1 {
		return nil, 0, fmt.Errorf("%s:%d StartPriority must take exactly one argument", f.relPath, f.fset.Position(found.Pos()).Line)
	}
	value, err := priorityLiteral(found.Args[0])
	if err != nil {
		return nil, 0, fmt.Errorf("%s:%d: %w", f.relPath, f.fset.Position(found.Pos()).Line, err)
	}
	return found, value, nil
}

// isStartPriority reports whether a call is the imported labels package's StartPriority.
func (f *fileContext) isStartPriority(call *ast.CallExpr) bool {
	if f.dotImport {
		ident, ok := call.Fun.(*ast.Ident)
		return ok && ident.Name == "StartPriority"
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "StartPriority" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == f.labelsName
}

// priorityLiteral reads the single integer literal argument of StartPriority.
func priorityLiteral(expression ast.Expr) (int, error) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return 0, fmt.Errorf("StartPriority argument must be an integer literal, not %T", expression)
	}
	value, err := strconv.Atoi(literal.Value)
	if err != nil {
		return 0, fmt.Errorf("StartPriority argument %q is not an integer: %w", literal.Value, err)
	}
	if value < 0 || value > labels.StartPriorityMax {
		return 0, fmt.Errorf("StartPriority argument %d is outside 0..%d", value, labels.StartPriorityMax)
	}
	return value, nil
}

// enclosingTable returns the nearest enclosing DescribeTable call, if any.
func (f *fileContext) enclosingTable(node ast.Node) *ast.CallExpr {
	for current := f.parents[node]; current != nil; current = f.parents[current] {
		call, ok := current.(*ast.CallExpr)
		if ok && callName(call) == "DescribeTable" {
			return call
		}
	}
	return nil
}

// resolve builds the source form for one leaf call and its effective priority.
// A leaf and its containers all contribute labels, so a declaration that carries
// StartPriority while an enclosing container also carries one is rejected rather
// than edited into a conflicting pair.
func (f *fileContext) resolve(leaf *ast.CallExpr) (*sourceCall, error) {
	result := &sourceCall{file: f, leaf: leaf}
	description, ok := stringLiteral(leaf.Args[0])
	if !ok {
		return nil, fmt.Errorf("%s:%d leaf description is not a plain string literal", f.relPath, f.fset.Position(leaf.Pos()).Line)
	}
	result.insertAt = f.offset(leaf.Args[0].End())
	leafNode, leafValue, err := f.startPriorityIn(leaf)
	if err != nil {
		return nil, err
	}
	var container *ast.CallExpr
	var containerNode *ast.CallExpr
	containerValue := 0
	for current := f.parents[leaf]; current != nil; current = f.parents[current] {
		call, ok := current.(*ast.CallExpr)
		if !ok || !containerCallNames[callName(call)] {
			continue
		}
		node, value, err := f.startPriorityIn(call)
		if err != nil {
			return nil, err
		}
		if node == nil {
			continue
		}
		if container != nil {
			return nil, fmt.Errorf("%s:%d inherits StartPriority from more than one container; refusing to edit it",
				f.relPath, f.fset.Position(leaf.Pos()).Line)
		}
		container, containerNode, containerValue = call, node, value
	}
	switch {
	case leafNode != nil && container != nil:
		return nil, fmt.Errorf("%s:%d declares StartPriority while its %s container already inherits one; refusing to edit it",
			f.relPath, f.fset.Position(leaf.Pos()).Line, callName(container))
	case leafNode != nil:
		result.node, result.value = leafNode, leafValue
	case container != nil:
		result.node, result.value = containerNode, containerValue
		result.table, result.onTable = container, true
	default:
		if f.labelsName == "" && !f.dotImport {
			return nil, fmt.Errorf("%s does not import %s, so a StartPriority cannot be added for %q", f.relPath, labelsImportPath, description)
		}
	}
	return result, nil
}
