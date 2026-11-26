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

// writtenby reads a JSON mapping of struct field writers and updates
// "// Written by:" annotations in Go source files.
//
// Usage:
//
//	go run ./hack/writtenby/
//	go run ./hack/writtenby/ -dry-run
//	go run ./hack/writtenby/ -mapping path/to/mapping.json -api path/to/api/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// safeWriterPattern matches writer strings containing only safe characters:
// alphanumeric, spaces, hyphens, underscores, slashes, periods, parentheses,
// commas, colons, semicolons, and asterisks.
var safeWriterPattern = regexp.MustCompile(`^[a-zA-Z0-9 \-_/.(,):;*]+$`)

// validateWriterString checks that a writer string contains no control
// characters or other unsafe content that could inject code into source
// comments. Returns true if the string is safe.
func validateWriterString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return safeWriterPattern.MatchString(s)
}

// fieldMod describes a modification to apply to a source file.
type fieldMod struct {
	// fieldLine is the 0-indexed line number of the field declaration.
	fieldLine int
	// writtenByLine is the 0-indexed line of the existing "// Written by:" comment,
	// or -1 if none exists.
	writtenByLine int
	// insertBefore is the 0-indexed line before which to insert a new annotation
	// (only used when writtenByLine == -1).
	insertBefore int
	// newContent is the fully formatted "// Written by:" line to write.
	newContent string
}

func main() {
	mappingPath := flag.String("mapping", "docs/generated/written-by-mapping.json",
		"Path to the JSON mapping file")
	apiDir := flag.String("api", "arohcp-cosmos-client/api",
		"Base directory for API source files; JSON keys are resolved relative to this path")
	dryRun := flag.Bool("dry-run", false,
		"Print what would change without modifying files")
	flag.Parse()

	data, err := os.ReadFile(*mappingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading mapping file: %v\n", err)
		os.Exit(1)
	}

	// mapping schema: file (relative to apiDir) → struct → field → [writers]
	var mapping map[string]map[string]map[string][]string
	if err := json.Unmarshal(data, &mapping); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing mapping JSON: %v\n", err)
		os.Exit(1)
	}

	// Validate all writer strings contain only safe characters.
	for relPath, structMap := range mapping {
		for structName, fieldMap := range structMap {
			for fieldName, writers := range fieldMap {
				for _, w := range writers {
					if !validateWriterString(w) {
						fmt.Fprintf(os.Stderr, "Error: unsafe writer string %q for %s.%s in %s\n",
							w, structName, fieldName, relPath)
						os.Exit(1)
					}
				}
			}
		}
	}

	// Resolve the apiDir flag to an absolute, cleaned path for containment checks.
	absAPIDir, err := filepath.Abs(*apiDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving api directory: %v\n", err)
		os.Exit(1)
	}

	var totalUpdated, totalAdded, totalUnchanged, totalFiles int

	// Sort file keys for deterministic processing order.
	fileKeys := sortedKeys(mapping)

	for _, relPath := range fileKeys {
		// Path containment: reject paths with ".." components.
		if strings.Contains(relPath, "..") {
			fmt.Fprintf(os.Stderr, "warning: skipping mapping entry with path traversal: %s\n", relPath)
			continue
		}

		// Path containment: require .go file extension.
		if !strings.HasSuffix(relPath, ".go") {
			fmt.Fprintf(os.Stderr, "warning: skipping non-.go mapping entry: %s\n", relPath)
			continue
		}

		// Path containment: ensure resolved path stays within the API directory.
		fullPath := filepath.Join(absAPIDir, relPath)
		cleanPath := filepath.Clean(fullPath)
		if !strings.HasPrefix(cleanPath, absAPIDir+string(filepath.Separator)) && cleanPath != absAPIDir {
			fmt.Fprintf(os.Stderr, "warning: skipping mapping entry that escapes api directory: %s\n", relPath)
			continue
		}

		structMap := mapping[relPath]

		// Track which struct.field pairs are matched in this file.
		matched := make(map[string]map[string]bool)

		updated, added, unchanged, err := processFile(cleanPath, structMap, *dryRun, matched)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", cleanPath, err)
			os.Exit(1)
		}
		if updated > 0 || added > 0 {
			totalFiles++
		}
		totalUpdated += updated
		totalAdded += added
		totalUnchanged += unchanged

		// Report unmatched struct/field entries.
		for structName, fieldMap := range structMap {
			for fieldName := range fieldMap {
				if matched[structName] == nil || !matched[structName][fieldName] {
					fmt.Fprintf(os.Stderr, "warning: mapping entry %s.%s did not match any struct field in %s\n",
						structName, fieldName, relPath)
				}
			}
		}
	}

	fmt.Printf("Done. Files modified: %d, fields updated: %d, fields added: %d, fields unchanged: %d\n",
		totalFiles, totalUpdated, totalAdded, totalUnchanged)
}

// processFile applies writer annotations to a single Go source file.
// It returns counts of updated, added, and unchanged fields.
// The matched map is populated with struct→field entries that were found in the AST.
func processFile(filePath string, structMap map[string]map[string][]string, dryRun bool, matched map[string]map[string]bool) (updated, added, unchanged int, err error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("reading file: %w", err)
	}

	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parsing file: %w", err)
	}

	lines := strings.Split(string(src), "\n")

	// Collect all modifications needed.
	mods := collectModifications(fset, astFile, structMap, lines, matched)

	if len(mods) == 0 {
		return 0, 0, 0, nil
	}

	// Sort by fieldLine descending so we can process bottom-to-top
	// without invalidating line indices.
	sort.Slice(mods, func(i, j int) bool {
		return mods[i].fieldLine > mods[j].fieldLine
	})

	// Apply modifications.
	fileModified := false
	for _, mod := range mods {
		if mod.writtenByLine >= 0 {
			// Replace existing line.
			if lines[mod.writtenByLine] == mod.newContent {
				unchanged++
				continue
			}
			lines[mod.writtenByLine] = mod.newContent
			updated++
			fileModified = true
		} else {
			// Insert new line before insertBefore index.
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:mod.insertBefore]...)
			newLines = append(newLines, mod.newContent)
			newLines = append(newLines, lines[mod.insertBefore:]...)
			lines = newLines
			added++
			fileModified = true
		}
	}

	if !fileModified {
		return updated, added, unchanged, nil
	}

	// Format the result to ensure valid Go formatting.
	result := strings.Join(lines, "\n")
	formatted, err := format.Source([]byte(result))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("formatting result: %w", err)
	}

	if dryRun {
		fmt.Printf("Would modify: %s (updated: %d, added: %d)\n", filePath, updated, added)
		return updated, added, unchanged, nil
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("stat file: %w", err)
	}

	if err := os.WriteFile(filePath, formatted, info.Mode()); err != nil {
		return 0, 0, 0, fmt.Errorf("writing file: %w", err)
	}

	return updated, added, unchanged, nil
}

// collectModifications walks the AST to find struct fields that need annotation
// changes and returns the list of modifications.
// The matched map is populated with struct→field entries that were found in the AST.
func collectModifications(fset *token.FileSet, astFile *ast.File, structMap map[string]map[string][]string, lines []string, matched map[string]map[string]bool) []fieldMod {
	var mods []fieldMod

	ast.Inspect(astFile, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}

		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}

		structName := ts.Name.Name
		fieldMap, ok := structMap[structName]
		if !ok {
			return true
		}

		for _, field := range st.Fields.List {
			fieldName := fieldNameFor(field)
			if fieldName == "" {
				continue
			}
			writers, ok := fieldMap[fieldName]
			if !ok {
				continue
			}

			// Record this struct.field as matched.
			if matched[structName] == nil {
				matched[structName] = make(map[string]bool)
			}
			matched[structName][fieldName] = true

			mod := buildModification(fset, field, writers, lines)
			mods = append(mods, mod)
		}

		return true
	})

	return mods
}

// buildModification determines how to update a single field's Written by annotation.
func buildModification(fset *token.FileSet, field *ast.Field, writers []string, lines []string) fieldMod {
	// Sort writers alphabetically for deterministic output.
	sortedWriters := make([]string, len(writers))
	copy(sortedWriters, writers)
	sort.Strings(sortedWriters)

	// Get field declaration line (0-indexed).
	fieldPos := fset.Position(field.Pos())
	fieldLine := fieldPos.Line - 1

	// Get indentation from the field declaration line.
	indent := extractIndent(lines[fieldLine])

	// Build the new Written by line.
	newContent := fmt.Sprintf("%s// Written by: %s", indent, strings.Join(sortedWriters, ", "))

	// Find existing Written by line in doc comment.
	writtenByLine := -1
	insertBefore := fieldLine // default: insert right before the field declaration

	if field.Doc != nil {
		for _, comment := range field.Doc.List {
			commentLine := fset.Position(comment.Pos()).Line - 1
			trimmed := strings.TrimSpace(comment.Text)
			if strings.HasPrefix(trimmed, "// Written by:") {
				writtenByLine = commentLine
				break
			}
		}

		if writtenByLine == -1 {
			// No existing Written by — find insertion point.
			// Written by goes after the last description comment line
			// and before any kubebuilder marker lines (// +...).
			insertBefore = findInsertionPoint(fset, field, fieldLine)
		}
	}

	return fieldMod{
		fieldLine:     fieldLine,
		writtenByLine: writtenByLine,
		insertBefore:  insertBefore,
		newContent:    newContent,
	}
}

// findInsertionPoint determines where to insert a new Written by line
// within a field's doc comment block. It places the annotation after
// description lines but before kubebuilder marker lines (// +...).
func findInsertionPoint(fset *token.FileSet, field *ast.Field, fieldLine int) int {
	if field.Doc == nil {
		return fieldLine
	}

	// Find the first kubebuilder marker in the doc comment.
	firstMarker := -1
	lastNonMarker := -1
	for _, comment := range field.Doc.List {
		commentLine := fset.Position(comment.Pos()).Line - 1
		trimmed := strings.TrimSpace(comment.Text)
		if strings.HasPrefix(trimmed, "// +") {
			if firstMarker == -1 {
				firstMarker = commentLine
			}
		} else {
			lastNonMarker = commentLine
		}
	}

	if firstMarker != -1 {
		// Insert before the first marker line.
		return firstMarker
	}
	if lastNonMarker != -1 {
		// Insert after the last non-marker comment line.
		return lastNonMarker + 1
	}
	// Fallback: insert right before the field.
	return fieldLine
}

// extractIndent returns the leading whitespace of a line.
func extractIndent(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	return line[:len(line)-len(trimmed)]
}

// fieldNameFor returns the name of a struct field. For named fields it returns
// the first name; for embedded fields it returns the type name (without package
// qualifier). Returns empty string if the name cannot be determined.
func fieldNameFor(field *ast.Field) string {
	if len(field.Names) > 0 {
		return field.Names[0].Name
	}
	return embeddedTypeName(field.Type)
}

// embeddedTypeName extracts the unqualified type name from an embedded field's
// type expression.
func embeddedTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.StarExpr:
		return embeddedTypeName(e.X)
	}
	return ""
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
