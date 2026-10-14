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

// Package editor performs in-place edits of scalar values in a YAML file while
// preserving all comments, formatting, key ordering, and trailing newline
// behavior. It parses the document with gopkg.in/yaml.v3 solely to resolve the
// line number of the target scalar, then performs a line-based splice so that
// nothing else in the file is disturbed.
//
// This mirrors the approach used by tooling/image-updater, generalized to
// replace arbitrary scalar values (e.g. resource requests) rather than image
// digests.
package editor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Update is a single scalar replacement, addressed by a dotted YAML path.
type Update struct {
	// Path is a dotted path into the document, e.g.
	// "defaults.backend.k8s.resources.requests.cpu".
	Path string
	// NewValue is the replacement scalar value, e.g. "150m" or "512Mi".
	NewValue string
	// line is resolved internally from the parsed document.
	line int
}

// Editor edits a single YAML file in place.
type Editor struct {
	filePath string
	root     *yaml.Node
}

// New reads and parses the YAML file at filePath.
func New(filePath string) (*Editor, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %s: %w", filePath, err)
	}

	return &Editor{filePath: filePath, root: &root}, nil
}

// Get returns the current scalar value and its 1-based line number for the
// given dotted path.
func (e *Editor) Get(path string) (value string, line int, err error) {
	parts := strings.Split(path, ".")
	node := e.root
	for _, part := range parts {
		node = findChild(node, part)
		if node == nil {
			return "", 0, fmt.Errorf("path %s not found", path)
		}
	}
	if node.Kind != yaml.ScalarNode {
		return "", 0, fmt.Errorf("path %s does not point to a scalar value", path)
	}
	return node.Value, node.Line, nil
}

func findChild(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind == yaml.DocumentNode && len(parent.Content) > 0 {
		parent = parent.Content[0]
	}
	if parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// ApplyUpdates rewrites the file, replacing only the targeted scalar values.
// Any inline trailing comment (e.g. "# note") on an edited line is preserved.
func (e *Editor) ApplyUpdates(updates []Update) error {
	if len(updates) == 0 {
		return nil
	}

	// Resolve line numbers for every update.
	for i := range updates {
		_, line, err := e.Get(updates[i].Path)
		if err != nil {
			return err
		}
		updates[i].line = line
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].line < updates[j].line })

	originalContent, err := os.ReadFile(e.filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", e.filePath, err)
	}
	endsWithNewline := len(originalContent) > 0 && originalContent[len(originalContent)-1] == '\n'

	file, err := os.Open(e.filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", e.filePath, err)
	}
	defer file.Close()

	targetDir := filepath.Dir(e.filePath)
	targetName := filepath.Base(e.filePath)
	tempFile, err := os.CreateTemp(targetDir, targetName+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", e.filePath, err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	writer := bufio.NewWriter(tempFile)

	lineNum := 1
	updateIndex := 0
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if updateIndex < len(updates) && updates[updateIndex].line == lineNum {
			newLine, err := spliceValue(line, updates[updateIndex].NewValue)
			if err != nil {
				return fmt.Errorf("line %d (%s): %w", lineNum, updates[updateIndex].Path, err)
			}
			line = newLine
			updateIndex++
		}
		lines = append(lines, line)
		lineNum++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file %s: %w", e.filePath, err)
	}

	for i, line := range lines {
		if i < len(lines)-1 || endsWithNewline {
			if _, err := writer.WriteString(line + "\n"); err != nil {
				return fmt.Errorf("failed to write temp file: %w", err)
			}
		} else {
			if _, err := writer.WriteString(line); err != nil {
				return fmt.Errorf("failed to write temp file: %w", err)
			}
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	tempFile.Close()
	file.Close()

	if err := os.Rename(tempFile.Name(), e.filePath); err != nil {
		return fmt.Errorf("failed to replace %s: %w", e.filePath, err)
	}
	return nil
}

// spliceValue replaces the value portion of a "key: value" line, preserving the
// key, indentation, and any inline trailing comment.
func spliceValue(line, newValue string) (string, error) {
	colonIdx := strings.Index(line, ":")
	if colonIdx == -1 {
		return "", fmt.Errorf("no key: value structure found")
	}
	prefix := line[:colonIdx+1]

	rest := line[colonIdx+1:]
	// Preserve an inline trailing comment if present.
	comment := ""
	if hashIdx := strings.Index(rest, "#"); hashIdx != -1 {
		comment = " " + strings.TrimRight(rest[hashIdx:], " ")
	}
	return prefix + " " + newValue + comment, nil
}

// Upsert sets the scalar at each dotted path, creating any missing intermediate
// mapping nodes. Existing scalars are replaced in place (preserving inline
// comments); missing paths are inserted as a nested block under the deepest
// existing ancestor. All other content, comments, and formatting are preserved.
//
// Updates are applied one at a time, re-reading the file between each, so that
// a parent block created for one update is reused by the next (e.g. inserting
// requests.cpu then requests.memory yields a single requests block).
func (e *Editor) Upsert(updates []Update) error {
	for _, u := range updates {
		if err := e.upsertOne(u.Path, u.NewValue); err != nil {
			return fmt.Errorf("upsert %s: %w", u.Path, err)
		}
	}
	return nil
}

func (e *Editor) upsertOne(path, value string) error {
	data, err := os.ReadFile(e.filePath)
	if err != nil {
		return err
	}
	trailingNewline := strings.HasSuffix(string(data), "\n")
	content := string(data)
	if trailingNewline {
		content = strings.TrimSuffix(content, "\n")
	}
	lines := strings.Split(content, "\n")

	segs := strings.Split(path, ".")
	start, end := 0, len(lines)
	keyIndent := 0

	for depth := 0; depth < len(segs); depth++ {
		seg := segs[depth]
		found := -1
		for i := start; i < end; i++ {
			if isBlank(lines[i]) {
				continue
			}
			ind := indentOf(lines[i])
			if ind < keyIndent {
				break
			}
			if ind == keyIndent && !isComment(lines[i]) && keyOf(lines[i]) == seg {
				found = i
				break
			}
		}

		if found == -1 {
			insertPos := blockEndIndex(lines, start, end, keyIndent)
			block := buildNested(segs[depth:], value, keyIndent)
			newLines := make([]string, 0, len(lines)+len(block))
			newLines = append(newLines, lines[:insertPos]...)
			newLines = append(newLines, block...)
			newLines = append(newLines, lines[insertPos:]...)
			return writeLines(e.filePath, newLines, trailingNewline)
		}

		if depth == len(segs)-1 {
			spliced, err := spliceValue(lines[found], value)
			if err != nil {
				return err
			}
			lines[found] = spliced
			return writeLines(e.filePath, lines, trailingNewline)
		}

		// Descend. Detect the child indent from the first child; fall back to +2.
		childIndent := keyIndent + 2
		for i := found + 1; i < end; i++ {
			if isBlank(lines[i]) {
				continue
			}
			ci := indentOf(lines[i])
			if ci <= keyIndent {
				break
			}
			childIndent = ci
			break
		}
		start = found + 1
		end = blockEndIndex(lines, found+1, end, childIndent)
		keyIndent = childIndent
	}
	return nil
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

func isComment(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "#") }

func keyOf(line string) string {
	t := strings.TrimSpace(line)
	if i := strings.Index(t, ":"); i != -1 {
		return t[:i]
	}
	return ""
}

// blockEndIndex returns the index just after the last line (skipping trailing
// blanks) that belongs to a block whose children are indented at childIndent.
func blockEndIndex(lines []string, from, limit, childIndent int) int {
	end := from
	for i := from; i < limit; i++ {
		if isBlank(lines[i]) {
			continue
		}
		if indentOf(lines[i]) < childIndent {
			break
		}
		end = i + 1
	}
	return end
}

// buildNested renders segs as an indented mapping chain, with value on the last.
func buildNested(segs []string, value string, baseIndent int) []string {
	out := make([]string, 0, len(segs))
	ind := baseIndent
	for i, s := range segs {
		prefix := strings.Repeat(" ", ind)
		if i == len(segs)-1 {
			out = append(out, prefix+s+": "+value)
		} else {
			out = append(out, prefix+s+":")
		}
		ind += 2
	}
	return out
}

func writeLines(path string, lines []string, trailingNewline bool) error {
	content := strings.Join(lines, "\n")
	if trailingNewline {
		content += "\n"
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), path)
}
