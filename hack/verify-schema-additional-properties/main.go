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

type schemaDefinition struct {
	Type                 string                      `json:"type"`
	AdditionalProperties *json.RawMessage            `json:"additionalProperties"`
	Definitions          map[string]schemaDefinition `json:"definitions"`
}

func check(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var schema schemaDefinition
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	var missing []string
	if schema.Type == "object" && schema.AdditionalProperties == nil {
		missing = append(missing, "(root)")
	}
	for name, def := range schema.Definitions {
		if def.Type == "object" && def.AdditionalProperties == nil {
			missing = append(missing, name)
		}
	}
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
