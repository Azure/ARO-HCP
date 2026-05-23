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

// Package tabular provides a structured representation of tabular data
// with preserved column ordering. It is used to store Kusto query results
// and other table-shaped data in a way that survives JSON round-tripping
// without losing column order.
package tabular

// Table represents ordered tabular data with named columns and string cell values.
type Table struct {
	// Columns lists the column names in display order.
	Columns []string `json:"columns"`

	// Rows contains the data rows. Each row is a slice of cell values
	// positionally corresponding to Columns.
	Rows [][]string `json:"rows"`
}
