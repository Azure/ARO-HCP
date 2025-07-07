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

package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"sigs.k8s.io/yaml"
)

// OutputFormat represents the supported output formats
type OutputFormat string

const (
	OutputFormatTable OutputFormat = "table"
	OutputFormatYAML  OutputFormat = "yaml"
	OutputFormatJSON  OutputFormat = "json"
)

// ValidateOutputFormat validates and returns the output format
func ValidateOutputFormat(format string) (OutputFormat, error) {
	switch OutputFormat(format) {
	case OutputFormatTable, OutputFormatYAML, OutputFormatJSON:
		return OutputFormat(format), nil
	default:
		return "", fmt.Errorf("unsupported output format: %s (supported: %v)", format, []OutputFormat{OutputFormatTable, OutputFormatYAML, OutputFormatJSON})
	}
}

// TableColumn represents a column in table output
type TableColumn[T any] struct {
	Header string
	Field  func(T) string
}

// TableOptions configures table formatting
type TableOptions[T any] struct {
	Title        string
	Columns      []TableColumn[T]
	EmptyMessage string
}

// OutputWrapper represents the wrapper structure for JSON/YAML output
type OutputWrapper struct {
	Kind  string      `json:"kind" yaml:"kind"`
	Total int         `json:"total" yaml:"total"`
	Items interface{} `json:"items" yaml:"items"`
}

// Formatter provides generic formatting capabilities for different output formats
type Formatter[T any] struct {
	Kind         string
	TableOptions TableOptions[T]
}

// NewFormatter creates a new formatter with the specified kind and table options
func NewFormatter[T any](kind string, tableOptions TableOptions[T]) *Formatter[T] {
	return &Formatter[T]{
		Kind:         kind,
		TableOptions: tableOptions,
	}
}

// FormatTable formats items as a table string
func (f *Formatter[T]) FormatTable(items []T) string {
	if len(items) == 0 {
		if f.TableOptions.EmptyMessage != "" {
			return f.TableOptions.EmptyMessage
		}
		return fmt.Sprintf("No %s found", strings.ToLower(f.Kind))
	}

	var buf bytes.Buffer

	if f.TableOptions.Title != "" {
		if strings.Contains(f.TableOptions.Title, "%d") {
			fmt.Fprintf(&buf, "\n%s:\n\n", fmt.Sprintf(f.TableOptions.Title, len(items)))
		} else {
			fmt.Fprintf(&buf, "\n%s %d %s:\n\n", f.TableOptions.Title, len(items), f.Kind)
		}
	}

	w := tabwriter.NewWriter(&buf, 0, 0, 4, ' ', 0)

	// Print header
	var headers []string
	for _, col := range f.TableOptions.Columns {
		headers = append(headers, col.Header)
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))

	// Print items
	for _, item := range items {
		var values []string
		for _, col := range f.TableOptions.Columns {
			values = append(values, col.Field(item))
		}
		fmt.Fprintf(w, "%s\n", strings.Join(values, "\t"))
	}

	w.Flush()
	return buf.String()
}

// FormatYAML formats items as YAML string with metadata
func (f *Formatter[T]) FormatYAML(items []T) (string, error) {
	output := OutputWrapper{
		Kind:  f.Kind,
		Total: len(items),
		Items: items,
	}
	data, err := yaml.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal %s to YAML: %w", f.Kind, err)
	}
	return string(data), nil
}

// FormatJSON formats items as JSON string with metadata
func (f *Formatter[T]) FormatJSON(items []T) (string, error) {
	output := OutputWrapper{
		Kind:  f.Kind,
		Total: len(items),
		Items: items,
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal %s to JSON: %w", f.Kind, err)
	}
	return string(data), nil
}

// Format formats items in the specified format and returns the string
func (f *Formatter[T]) Format(items []T, format OutputFormat) (string, error) {
	switch format {
	case OutputFormatTable:
		return f.FormatTable(items), nil
	case OutputFormatYAML:
		return f.FormatYAML(items)
	case OutputFormatJSON:
		return f.FormatJSON(items)
	default:
		return "", fmt.Errorf("unsupported output format: %s", format)
	}
}

// Display formats and prints items in the specified format
func (f *Formatter[T]) Display(items []T, format OutputFormat) error {
	output, err := f.Format(items, format)
	if err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}
