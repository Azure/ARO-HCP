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

// verify-schema-additional-properties checks that all object definitions in a
// JSON schema file have an "additionalProperties" field set. Without it, the
// schema silently allows unexpected properties, defeating typo detection.
//
// Exit code is 1 if any object definitions are missing the field.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type schemaNode struct {
	Type                 json.RawMessage       `json:"type"`
	AdditionalProperties *json.RawMessage      `json:"additionalProperties"`
	Properties           map[string]schemaNode `json:"properties"`
	Definitions          map[string]schemaNode `json:"definitions"`
	PatternProperties    map[string]schemaNode `json:"patternProperties"`
	Items                *schemaNode           `json:"items"`
	AllOf                []schemaNode          `json:"allOf"`
	OneOf                []schemaNode          `json:"oneOf"`
	AnyOf                []schemaNode          `json:"anyOf"`
	Not                  *schemaNode           `json:"not"`
}

func (n schemaNode) isObject() bool {
	if len(n.Properties) > 0 || len(n.PatternProperties) > 0 || n.AdditionalProperties != nil {
		return true
	}
	if n.Type == nil {
		return false
	}
	var s string
	if json.Unmarshal(n.Type, &s) == nil {
		return s == "object"
	}
	var arr []string
	if json.Unmarshal(n.Type, &arr) == nil {
		for _, t := range arr {
			if t == "object" {
				return true
			}
		}
	}
	return false
}

func walkSchema(node schemaNode, path string, missing *[]string) {
	if node.isObject() && node.AdditionalProperties == nil {
		if path == "" {
			path = "(root)"
		}
		*missing = append(*missing, path)
	}

	for name, child := range node.Definitions {
		walkSchema(child, joinPath(path, name), missing)
	}
	for name, child := range node.Properties {
		walkSchema(child, joinPath(path, name), missing)
	}
	for _, child := range node.PatternProperties {
		walkSchema(child, joinPath(path, "(patternProperty)"), missing)
	}
	if node.Items != nil {
		walkSchema(*node.Items, joinPath(path, "(items)"), missing)
	}
	for _, child := range node.AllOf {
		walkSchema(child, path, missing)
	}
	for _, child := range node.OneOf {
		walkSchema(child, path, missing)
	}
	for _, child := range node.AnyOf {
		walkSchema(child, path, missing)
	}
	if node.Not != nil {
		walkSchema(*node.Not, joinPath(path, "(not)"), missing)
	}
}

func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

func check(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var schema schemaNode
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	var missing []string
	walkSchema(schema, "", &missing)
	sort.Strings(missing)
	return missing, nil
}

func main() {
	paths := os.Args[1:]
	if len(paths) == 0 {
		paths = []string{"config/config.schema.json"}
	}

	exitCode := 0
	for _, path := range paths {
		missing, err := check(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			exitCode = 1
			continue
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "ERROR: %d object definition(s) missing additionalProperties in %s:\n", len(missing), path)
			for _, m := range missing {
				fmt.Fprintf(os.Stderr, "  - %s\n", m)
			}
			fmt.Fprintf(os.Stderr, "\nAdd \"additionalProperties\": false (or true) to each definition.\n")
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}
