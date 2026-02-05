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

package yaml

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Update represents a single line update in a YAML file
type Update struct {
	Name      string // Image/component name
	OldDigest string // Current digest value
	NewDigest string // New digest value
	Tag       string // Image tag (e.g., "v1.2.3")
	Version   string // Human-friendly version from container label (if configured and available)
	Date      string // Image creation date (e.g., "2025-11-24 14:30")
	FilePath  string // Path to the YAML file
	JsonPath  string // JSON path to the value in the YAML
	Line      int    // Line number in the file
	ValueType string
}

type EditorInterface interface {
	GetUpdate(path string) (int, string, error)
	GetLineWithComment(path string) (int, string, string, error)
	ApplyUpdates(updates []Update) error
}

// Editor provides functionality to edit YAML files while preserving structure
type Editor struct {
	filePath string
	root     *yaml.Node
}

// NewEditor creates a new YAML editor for the specified file
func NewEditor(filePath string) (*Editor, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %s: %w", filePath, err)
	}

	return &Editor{
		filePath: filePath,
		root:     &root,
	}, nil
}

// GetUpdate retrieves the update(line + text) required at the specified path
func (e *Editor) GetUpdate(path string) (int, string, error) {
	parts := strings.Split(path, ".")
	node := e.root

	for _, part := range parts {
		node = e.findChild(node, part)
		if node == nil {
			return 0, "", fmt.Errorf("path %s not found", path)
		}
	}

	if node.Kind != yaml.ScalarNode {
		return 0, "", fmt.Errorf("path %s does not point to a scalar value", path)
	}

	line := node.Line
	return line, node.Value, nil
}

// GetLineWithComment retrieves the line number, value, and the full line content (including comment)
func (e *Editor) GetLineWithComment(path string) (int, string, string, error) {
	line, value, err := e.GetUpdate(path)
	if err != nil {
		return 0, "", "", err
	}

	// Read the line from the file
	file, err := os.Open(e.filePath)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	currentLine := 1
	for scanner.Scan() {
		if currentLine == line {
			return line, value, scanner.Text(), nil
		}
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return 0, "", "", fmt.Errorf("error reading file: %w", err)
	}

	return line, value, "", nil
}

// ParseVersionComment extracts tag and date from a YAML line comment
// Expected format: "digest: sha256:abc... # v1.2.3 (2025-01-15 10:30)"
// Returns (tag, date) where either or both may be empty if not found
func ParseVersionComment(lineContent string) (string, string) {
	// Find the comment marker
	commentIdx := strings.Index(lineContent, "#")
	if commentIdx == -1 {
		return "", ""
	}

	// Extract the comment part
	comment := strings.TrimSpace(lineContent[commentIdx+1:])
	if comment == "" {
		return "", ""
	}

	// Look for date in parentheses at the end
	var tag, date string
	if lastParen := strings.LastIndex(comment, "("); lastParen != -1 {
		if closeParen := strings.Index(comment[lastParen:], ")"); closeParen != -1 {
			date = strings.TrimSpace(comment[lastParen+1 : lastParen+closeParen])
			tag = strings.TrimSpace(comment[:lastParen])
		} else {
			// No closing paren, treat entire comment as tag
			tag = comment
		}
	} else {
		// No parentheses, treat entire comment as tag
		tag = comment
	}

	return tag, date
}

// findChild finds a child node with the specified key
func (e *Editor) findChild(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind == yaml.DocumentNode && len(parent.Content) > 0 {
		parent = parent.Content[0]
	}

	if parent.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(parent.Content); i += 2 {
		keyNode := parent.Content[i]
		valueNode := parent.Content[i+1]

		if keyNode.Value == key {
			return valueNode
		}
	}

	return nil
}

// ApplyUpdates applies a list of updates to the file
func (e *Editor) ApplyUpdates(updates []Update) error {
	if len(updates) == 0 {
		return nil
	}

	// Sort updates by line number before applying
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].Line < updates[j].Line
	})

	// Read the original file content to check if it ends with a newline
	originalContent, err := os.ReadFile(e.filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %v", e.filePath, err)
	}
	endsWithNewline := len(originalContent) > 0 && originalContent[len(originalContent)-1] == '\n'

	// Read the original file and copy to a temp file, applying updates on the fly
	// Then replace the original file with the temp file
	// This preserves the original formatting instead of rewriting with a yaml library
	file, err := os.Open(e.filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", e.filePath, err)
	}
	defer file.Close()

	targetDir := filepath.Dir(e.filePath)
	targetName := filepath.Base(e.filePath)

	tempFile, err := os.CreateTemp(targetDir, targetName+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %v", e.filePath, err)
	}

	// Ensure temp file is cleaned up if we panic or error out early
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	scanner := bufio.NewScanner(file)
	writer := bufio.NewWriter(tempFile)

	lineNum := 1
	updateIndex := 0
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if updateIndex < len(updates) && updates[updateIndex].Line == lineNum {
			// Find where the YAML value starts (after "key: ")
			// We look for the colon followed by optional whitespace
			colonIdx := strings.Index(line, ":")
			if colonIdx != -1 {
				// Keep everything up to and including the colon and any following spaces
				prefix := line[:colonIdx+1]
				// Add a single space after the colon
				if colonIdx+1 < len(line) && line[colonIdx+1] == ' ' {
					prefix = line[:colonIdx+2]
				} else {
					prefix += " "
				}

				// Build the new value with the digest and optional comment
				newValue := updates[updateIndex].NewDigest

				// If the config requested "tag" mode, use the tag instead of SHA
				if updates[updateIndex].ValueType == "tag" {
					// Use the tag (e.g. "2.10.1")
					// We quote it to ensure YAML treats it as a string (prevents "1.20" -> 1.2 float parsing)
					newValue = fmt.Sprintf("%q", updates[updateIndex].Tag)
				}

				// Prepare the comment (Version/Tag + Date)
				// Use Version if available, otherwise fall back to Tag
				versionInfo := updates[updateIndex].Version
				if versionInfo == "" {
					versionInfo = updates[updateIndex].Tag
				}

				// Construct the comment
				if versionInfo != "" {
					comment := ""

					// Logic to avoid redundancy:
					// If we are writing the Tag as the value, don't repeat it in the comment.
					// Only show the date.
					if updates[updateIndex].ValueType == "tag" {
						if updates[updateIndex].Date != "" {
							comment = "(" + updates[updateIndex].Date + ")"
						}
					} else {
						// Standard Digest Mode: Show "v1.2.3 (Date)"
						comment = versionInfo
						if updates[updateIndex].Date != "" {
							comment = comment + " (" + updates[updateIndex].Date + ")"
						}
					}

					// Append comment if it's not empty
					if comment != "" {
						newValue = newValue + " # " + comment
					}
				}
				line = prefix + newValue
			} else {
				// Fallback: if we can't find a colon, just replace the old digest with new
				// This shouldn't normally happen but provides a safety net
				line = strings.Replace(line, updates[updateIndex].OldDigest, updates[updateIndex].NewDigest, 1)

				// Add the version/tag and date comment
				versionInfo := updates[updateIndex].Version
				if versionInfo == "" {
					versionInfo = updates[updateIndex].Tag
				}
				if versionInfo != "" {
					comment := versionInfo
					if updates[updateIndex].Date != "" {
						comment = comment + " (" + updates[updateIndex].Date + ")"
					}
					line = line + " # " + comment
				}
			}
			updateIndex++
		}
		lines = append(lines, line)
		lineNum++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file %s: %v", e.filePath, err)
	}

	// Write all lines, preserving original newline behavior
	for i, line := range lines {
		if i < len(lines)-1 {
			// Not the last line, always add newline
			_, err := writer.WriteString(line + "\n")
			if err != nil {
				return fmt.Errorf("failed to write to temp file: %v", err)
			}
		} else {
			// Last line: add newline only if original file had one
			if endsWithNewline {
				_, err := writer.WriteString(line + "\n")
				if err != nil {
					return fmt.Errorf("failed to write to temp file: %v", err)
				}
			} else {
				_, err := writer.WriteString(line)
				if err != nil {
					return fmt.Errorf("failed to write to temp file: %v", err)
				}
			}
		}
	}

	// Flush and sync before closing
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush temp file: %v", err)
	}

	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %v", err)
	}

	// Close files before renaming
	tempFile.Close()
	file.Close()

	if err := os.Rename(tempFile.Name(), e.filePath); err != nil {
		return fmt.Errorf("failed to replace original file %s with updated content: %v", e.filePath, err)
	}

	return nil
}
