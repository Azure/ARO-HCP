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

package verifiers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-kusto-go/azkustodata/kql"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/kusto-table-creation/schema"
)

// kustoTableSchema represents a Kusto table definition.
type kustoTableSchema struct {
	Name     string
	Database string
	Columns  []string
}

// findRepoRoot locates the repository root by walking up from the current
// working directory until it finds go.work.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repo root (go.work not found)")
		}
		dir = parent
	}
}

// loadKustoTableSchemas builds the expected table schemas by reading
// tables.yaml (via NewConfigFromFile). Each table's database assignments
// and column definitions come directly from the YAML config.
func loadKustoTableSchemas() ([]kustoTableSchema, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, fmt.Errorf("finding repo root: %w", err)
	}

	tablesYAMLPath := filepath.Join(repoRoot, "tooling", "kusto-table-creation", "tables.yaml")

	cfg, err := schema.NewConfigFromFile(tablesYAMLPath)
	if err != nil {
		return nil, fmt.Errorf("loading tables.yaml: %w", err)
	}

	defMap := cfg.DefinitionMap()
	var schemas []kustoTableSchema

	for _, table := range cfg.Tables {
		columns := schema.ResolveColumns(table, defMap)
		colNames := make([]string, len(columns))
		for i, c := range columns {
			colNames[i] = c.Name
		}

		for _, db := range table.Databases {
			schemas = append(schemas, kustoTableSchema{
				Name:     table.Name,
				Database: db,
				Columns:  colNames,
			})
		}
	}

	return schemas, nil
}

// rawQuery implements kusto.Query for executing raw KQL queries
type rawQuery struct {
	name     string
	database string
	query    *kql.Builder
}

func (q *rawQuery) GetName() string               { return q.name }
func (q *rawQuery) GetQueryType() kusto.QueryType { return kusto.QueryTypeInternal }
func (q *rawQuery) GetDatabase() string           { return q.database }
func (q *rawQuery) GetQuery() *kql.Builder        { return q.query }
func (q *rawQuery) IsUnlimited() bool             { return false }

func hasColumn(table kustoTableSchema, name string) bool {
	for _, c := range table.Columns {
		if c == name {
			return true
		}
	}
	return false
}

// buildColumnCheckQuery creates a KQL query that counts non-empty values for each column.
// When clusterNames is non-empty and the table has a "cluster" column, the query
// is restricted to rows matching those cluster names.
func buildColumnCheckQuery(table kustoTableSchema, clusterNames []string) kusto.Query {
	var summarizeParts []string
	summarizeParts = append(summarizeParts, "Total = count()")
	for _, col := range table.Columns {
		summarizeParts = append(summarizeParts,
			fmt.Sprintf("['%s'] = countif(isnotempty(['%s']))", col, col))
	}

	var filters []string

	if len(clusterNames) > 0 && hasColumn(table, "cluster") {
		quoted := make([]string, len(clusterNames))
		for i, name := range clusterNames {
			quoted[i] = "'" + name + "'"
		}
		filters = append(filters, fmt.Sprintf("| where ['cluster'] in (%s)", strings.Join(quoted, ", ")))
	}

	queryStr := fmt.Sprintf("%s\n%s\n| summarize %s",
		table.Name,
		strings.Join(filters, "\n"),
		strings.Join(summarizeParts, ", "))

	builder := kql.New("")
	builder.AddUnsafe(queryStr)

	return &rawQuery{
		name:     fmt.Sprintf("column-check-%s-%s", table.Database, table.Name),
		database: table.Database,
		query:    builder,
	}
}

// verifyKustoTableColumnsImpl verifies all columns in Kusto tables have data
type verifyKustoTableColumnsImpl struct {
	kustoCluster string
	kustoRegion  string
	queryTimeout time.Duration
	tables       []kustoTableSchema
	clusterNames []string
}

func (v verifyKustoTableColumnsImpl) Name() string {
	return "VerifyKustoTableColumns"
}

func (v verifyKustoTableColumnsImpl) Verify(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	if len(v.clusterNames) == 0 {
		return fmt.Errorf("no cluster names retrieved from BUILD_ID environment variable")
	}

	endpoint, err := kusto.KustoEndpoint(v.kustoCluster, v.kustoRegion)
	if err != nil {
		return fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	kustoClient, err := kusto.NewClient(endpoint, v.queryTimeout)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if closeErr := kustoClient.Close(); closeErr != nil {
			logger.Error(closeErr, "Failed to close Kusto client")
		}
	}()

	var missingColumns []string

	for _, table := range v.tables {
		query := buildColumnCheckQuery(table, v.clusterNames)

		outputChannel := make(chan kusto.TaggedRow)
		var resultRow *kusto.TaggedRow

		group := new(errgroup.Group)
		group.Go(func() error {
			for row := range outputChannel {
				r := row
				resultRow = &r
			}
			return nil
		})

		_, queryErr := kustoClient.ExecutePreconfiguredQuery(ctx, query, outputChannel)
		close(outputChannel)

		if err := group.Wait(); err != nil {
			return fmt.Errorf("failed to process column check result for %s.%s: %w", table.Database, table.Name, err)
		}

		if queryErr != nil {
			return fmt.Errorf("failed to execute column check for %s.%s: %w", table.Database, table.Name, queryErr)
		}

		if resultRow == nil {
			missingColumns = append(missingColumns, fmt.Sprintf("%s.%s (no result returned)", table.Database, table.Name))
			continue
		}

		columns := resultRow.Row.Columns()
		values := resultRow.Row.Values()

		for i, col := range columns {
			if col.Name() == "Total" {
				continue
			}
			countStr := values[i].String()
			count, parseErr := strconv.ParseInt(countStr, 10, 64)
			if parseErr != nil {
				return fmt.Errorf("failed to parse count for %s.%s.%s (value: %q): %w",
					table.Database, table.Name, col.Name(), countStr, parseErr)
			}
			if count == 0 {
				missingColumns = append(missingColumns,
					fmt.Sprintf("%s.%s.%s", table.Database, table.Name, col.Name()))
			}
		}

		logger.V(1).Info("Column check completed", "database", table.Database, "table", table.Name)
	}

	if len(missingColumns) > 0 {
		return fmt.Errorf("columns with no data in the last 24 hours: %v", missingColumns)
	}

	return nil
}

// VerifyKustoTableColumns creates a verifier that checks all columns in Kusto
// tables have populated data. Table schemas are loaded from tables.yaml (the
// single source of truth) and the bicep database mapping.
func VerifyKustoTableColumns() (verifyKustoTableColumnsImpl, error) {
	tables, err := loadKustoTableSchemas()
	if err != nil {
		return verifyKustoTableColumnsImpl{}, fmt.Errorf("loading table schemas: %w", err)
	}

	var clusters []string
	svc, mgmt := infraClusterNames()
	if svc != "" {
		clusters = append(clusters, svc, mgmt)
	}

	return verifyKustoTableColumnsImpl{
		kustoCluster: "hcp-dev-us-2",
		kustoRegion:  "eastus2",
		queryTimeout: 5 * time.Minute,
		tables:       tables,
		clusterNames: clusters,
	}, nil
}

// infraClusterNames derives SVC and MGMT cluster names from the BUILD_ID env var.
// Returns empty strings if BUILD_ID is not set.
func infraClusterNames() (svcCluster, mgmtCluster string) {
	buildID := os.Getenv("BUILD_ID")
	if buildID == "" {
		return "", ""
	}

	// regionShort = "j" + last 7 chars of BUILD_ID
	suffix := buildID
	if len(suffix) > 7 {
		suffix = suffix[len(suffix)-7:]
	}
	regionShort := "j" + suffix

	// naming convention from config/config.yaml (svcClusterName / mgmtClusterName templates)
	svcCluster = fmt.Sprintf("prow-%s-svc", regionShort)
	mgmtCluster = fmt.Sprintf("prow-%s-mgmt-1", regionShort)
	return svcCluster, mgmtCluster
}
