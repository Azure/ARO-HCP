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
	"strings"
	"testing"

	"github.com/Azure/azure-kusto-go/azkustodata/query"
	v1 "github.com/Azure/azure-kusto-go/azkustodata/query/v1"
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
				rowMap[col.Name()] = fmt.Sprintf("%v", val)
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
				rowMap[col.Name()] = fmt.Sprintf("%v", val)
			}
			rows = append(rows, rowMap)
		}
	}
	return rows
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
		ev := fmt.Sprintf("%v", expectedVal)
		actualVal, ok := actual[col]
		if !ok {
			fmt.Fprintf(&b, "      %s:\n        - %s\n        + (missing)\n", col, ev)
			continue
		}
		av := fmt.Sprintf("%v", actualVal)
		if ev != av {
			fmt.Fprintf(&b, "      %s:\n        - %s\n        + %s\n", col, ev, av)
		}
	}
	return b.String()
}

func formatRow(row map[string]any) string {
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Sprintf("%v", row)
	}
	return string(data)
}
