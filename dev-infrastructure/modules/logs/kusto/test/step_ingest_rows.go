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
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ingestRowsStep inserts seed rows into a Kusto table.
// The target database and table are declared in the step's 00-key.json.
// The step directory contains JSON files, each an array of row objects.
// Keys in the row objects must match column names in the target table.
// Values are serialized into a KQL datatable expression for ingestion.
type ingestRowsStep struct {
	id       stepID
	database string
	table    string
	rows     []map[string]any
}

type ingestRowsKey struct {
	Database string `json:"database"`
	Table    string `json:"table"`
}

func newIngestRowsStep(id stepID, stepDir fs.FS) (*ingestRowsStep, error) {
	keyContent, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("missing 00-key.json: %w", err)
	}
	var key ingestRowsKey
	if err := json.Unmarshal(keyContent, &key); err != nil {
		return nil, fmt.Errorf("failed to parse 00-key.json: %w", err)
	}
	if len(key.Database) == 0 {
		return nil, fmt.Errorf("00-key.json must specify 'database'")
	}
	if len(key.Table) == 0 {
		return nil, fmt.Errorf("00-key.json must specify 'table'")
	}

	entries, err := fs.ReadDir(stepDir, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to read step directory: %w", err)
	}

	var allRows []map[string]any
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "00-key.json" {
			continue
		}
		content, err := fs.ReadFile(stepDir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", entry.Name(), err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(content, &rows); err != nil {
			return nil, fmt.Errorf("failed to parse %s as JSON array: %w", entry.Name(), err)
		}
		allRows = append(allRows, rows...)
	}

	return &ingestRowsStep{
		id:       id,
		database: key.Database,
		table:    key.Table,
		rows:     allRows,
	}, nil
}

func (s *ingestRowsStep) StepID() stepID { return s.id }

func (s *ingestRowsStep) Run(ctx context.Context, t *testing.T, clients map[string]*emulatorClient) {
	t.Helper()
	client, ok := clients[s.database]
	require.True(t, ok, "%s: database %q not found in client map", s.id, s.database)

	if len(s.rows) == 0 {
		debugf(t, "%s: no rows to ingest", s.id)
		return
	}

	schema, err := getTableSchema(ctx, client, s.table)
	require.NoError(t, err, "failed to get schema for table %q in database %q", s.table, s.database)
	require.NotEmpty(t, schema, "table %q in database %q has no columns", s.table, s.database)

	cmd := buildIngestCommand(t, s.table, schema, s.rows)
	_, err = client.mgmt(ctx, cmd)
	require.NoError(t, err, "failed to ingest rows into %q.%q", s.database, s.table)

	debugf(t, "%s: ingested %d rows into %s.%s", s.id, len(s.rows), s.database, s.table)
}

type columnSchema struct {
	Name string
	Type string
}

func getTableSchema(ctx context.Context, client *emulatorClient, table string) ([]columnSchema, error) {
	ds, err := client.mgmt(ctx, fmt.Sprintf(".show table %s cslschema", table))
	if err != nil {
		return nil, fmt.Errorf("failed to query cslschema for table %q: %w", table, err)
	}

	// The cslschema result has a Schema column with "col1:type1,col2:type2,..." format
	for _, tbl := range ds.Tables() {
		for _, row := range tbl.Rows() {
			schemaStr, err := row.StringByName("Schema")
			if err != nil {
				continue
			}
			return parseCslSchema(schemaStr)
		}
	}
	return nil, fmt.Errorf("no schema found for table %q", table)
}

func parseCslSchema(schema string) ([]columnSchema, error) {
	if len(schema) == 0 {
		return nil, fmt.Errorf("empty schema string")
	}
	parts := strings.Split(schema, ",")
	var columns []columnSchema
	for _, part := range parts {
		colParts := strings.SplitN(part, ":", 2)
		if len(colParts) != 2 {
			return nil, fmt.Errorf("invalid schema part: %q", part)
		}
		columns = append(columns, columnSchema{
			Name: colParts[0],
			Type: colParts[1],
		})
	}
	return columns, nil
}

func buildIngestCommand(t *testing.T, table string, schema []columnSchema, rows []map[string]any) string {
	t.Helper()

	schemaNames := make(map[string]bool, len(schema))
	for _, col := range schema {
		schemaNames[col.Name] = true
	}
	for i, row := range rows {
		for key := range row {
			require.True(t, schemaNames[key],
				"fixture row %d has key %q not found in table %q schema", i, key, table)
		}
	}

	// Build datatable column declaration
	var colDecl []string
	for _, col := range schema {
		colDecl = append(colDecl, fmt.Sprintf("%s:%s", col.Name, col.Type))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(".set-or-append %s <| datatable(%s) [\n", table, strings.Join(colDecl, ", ")))

	for i, row := range rows {
		if i > 0 {
			sb.WriteString(",\n")
		}
		var vals []string
		for _, col := range schema {
			val, ok := row[col.Name]
			if !ok {
				vals = append(vals, kqlDefault(col.Type))
				continue
			}
			lit, err := kqlLiteral(col.Type, val)
			require.NoError(t, err, "failed to format value for column %q", col.Name)
			vals = append(vals, lit)
		}
		sb.WriteString("  ")
		sb.WriteString(strings.Join(vals, ", "))
	}

	sb.WriteString("\n]")
	return sb.String()
}

func kqlDefault(colType string) string {
	switch colType {
	case "string":
		return `""`
	case "datetime":
		return "datetime(null)"
	case "timespan":
		return "timespan(null)"
	case "int":
		return "int(null)"
	case "long":
		return "long(null)"
	case "real":
		return "real(null)"
	case "decimal":
		return "decimal(null)"
	case "bool":
		return "bool(null)"
	case "dynamic":
		return "dynamic(null)"
	case "guid":
		return "guid(null)"
	default:
		return `""`
	}
}

func kqlLiteral(colType string, val any) (string, error) {
	switch colType {
	case "string":
		s := fmt.Sprintf("%v", val)
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return fmt.Sprintf("\"%s\"", s), nil
	case "datetime":
		return fmt.Sprintf("datetime(%v)", val), nil
	case "timespan":
		return fmt.Sprintf("timespan(%v)", val), nil
	case "int", "long":
		f, ok := val.(float64)
		if !ok {
			return "", fmt.Errorf("expected float64 for %s column, got %T", colType, val)
		}
		return fmt.Sprintf("%d", int64(f)), nil
	case "real":
		return fmt.Sprintf("real(%v)", val), nil
	case "decimal":
		return fmt.Sprintf("decimal(%v)", val), nil
	case "bool":
		return fmt.Sprintf("%v", val), nil
	case "dynamic":
		jsonBytes, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("failed to marshal dynamic value: %w", err)
		}
		return fmt.Sprintf("dynamic(%s)", string(jsonBytes)), nil
	case "guid":
		return fmt.Sprintf("guid(%v)", val), nil
	default:
		s := fmt.Sprintf("%v", val)
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return fmt.Sprintf("\"%s\"", s), nil
	}
}
