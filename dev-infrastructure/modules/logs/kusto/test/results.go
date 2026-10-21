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

package kustotest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-kusto-go/azkustodata/query"
	v1 "github.com/Azure/azure-kusto-go/azkustodata/query/v1"
	"github.com/Azure/azure-kusto-go/azkustodata/types"
)

func datasetToRows(t *testing.T, ds query.Dataset) []map[string]any {
	t.Helper()

	var rows []map[string]any
	for _, table := range ds.Tables() {
		if !table.IsPrimaryResult() {
			continue
		}
		for _, row := range table.Rows() {
			rowMap := make(map[string]any)
			for _, col := range row.Columns() {
				val, err := row.ValueByName(col.Name())
				if err != nil {
					continue
				}
				rowMap[col.Name()] = normalizeColumnValue(col.Type(), val)
			}
			rows = append(rows, rowMap)
		}
	}
	return rows
}

// mgmtDatasetToRows converts a v1.Dataset from a management command into row
// maps. Management commands do not mark tables as primary results, so this
// iterates all tables.
func mgmtDatasetToRows(t *testing.T, ds v1.Dataset) []map[string]any {
	t.Helper()

	var rows []map[string]any
	for _, table := range ds.Tables() {
		for _, row := range table.Rows() {
			rowMap := make(map[string]any)
			for _, col := range row.Columns() {
				val, err := row.ValueByName(col.Name())
				if err != nil {
					continue
				}
				rowMap[col.Name()] = normalizeColumnValue(col.Type(), val)
			}
			rows = append(rows, rowMap)
		}
	}
	return rows
}

// normalizeColumnValue unmarshals dynamic columns, which the SDK returns as
// JSON-encoded strings, so they structurally match expected-result.json's
// own parsed JSON. Every other column type stays a plain %v string.
func normalizeColumnValue(colType types.Column, val any) any {
	if colType != types.Dynamic {
		return fmt.Sprintf("%v", val)
	}
	raw := fmt.Sprintf("%v", val)
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw
	}
	return parsed
}

func compareRows(t *testing.T, label string, expectedRows, actualRows []map[string]any) {
	t.Helper()
	diff := diffRows(expectedRows, actualRows)
	if len(diff) > 0 {
		t.Errorf("%s: result mismatch (expected %d rows, got %d)\n%s",
			label, len(expectedRows), len(actualRows), diff)
		t.FailNow()
	}
}

func diffRows(expected, actual []map[string]any) string {
	maxLen := len(expected)
	if len(actual) > maxLen {
		maxLen = len(actual)
	}
	var b strings.Builder
	for i := 0; i < maxLen; i++ {
		hasExpected := i < len(expected)
		hasActual := i < len(actual)
		switch {
		case hasExpected && !hasActual:
			fmt.Fprintf(&b, "  - [%d] %s\n", i, formatRow(expected[i]))
		case !hasExpected && hasActual:
			fmt.Fprintf(&b, "  + [%d] %s\n", i, formatRow(actual[i]))
		default:
			colDiff := diffColumns(expected[i], actual[i])
			if len(colDiff) > 0 {
				fmt.Fprintf(&b, "  ~ [%d]\n%s", i, colDiff)
			}
		}
	}
	return b.String()
}

func diffColumns(expected, actual map[string]any) string {
	var b strings.Builder
	for col, expectedVal := range expected {
		actualVal, ok := actual[col]
		if !ok {
			fmt.Fprintf(&b, "      %s:\n        - %s\n        + (missing)\n", col, displayValue(expectedVal))
			continue
		}
		if !valuesEqual(expectedVal, actualVal) {
			fmt.Fprintf(&b, "      %s:\n        - %s\n        + %s\n", col, displayValue(expectedVal), displayValue(actualVal))
		}
	}
	return b.String()
}

// Non-string values are round-tripped through JSON before DeepEqual, since
// expectedVal and actualVal can independently end up as different Go numeric
// types (e.g. int vs. float64) for the same JSON number.
func valuesEqual(expectedVal, actualVal any) bool {
	expectedStr, expectedIsString := expectedVal.(string)
	actualStr, actualIsString := actualVal.(string)
	if expectedIsString || actualIsString {
		if expectedIsString && actualIsString {
			return expectedStr == actualStr
		}
		return fmt.Sprintf("%v", expectedVal) == fmt.Sprintf("%v", actualVal)
	}

	expectedJSON, err := json.Marshal(expectedVal)
	if err != nil {
		return false
	}
	var normalizedExpected any
	if err := json.Unmarshal(expectedJSON, &normalizedExpected); err != nil {
		return false
	}
	return reflect.DeepEqual(normalizedExpected, actualVal)
}

// displayValue renders a value for the human-readable diff output: a
// string as-is, anything else as canonical JSON.
func displayValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func formatRow(row map[string]any) string {
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Sprintf("%v", row)
	}
	return string(data)
}
