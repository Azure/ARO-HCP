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

package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	v1 "github.com/Azure/azure-kusto-go/azkustodata/query/v1"
)

func newKQLCommand() *cobra.Command {
	var endpoint, database, kqlDir string

	cmd := &cobra.Command{
		Use:   "kql",
		Short: "Validate KQL table definitions against a Kusto emulator",
		Long: `Validate .kql files by executing them against a running Kusto emulator.

Each .kql file is executed via ".execute database script" and the resulting
tables are verified. This mirrors how the files are deployed in production
via script.bicep.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logr.FromContextOrDiscard(cmd.Context())

			kcsb := azkustodata.NewConnectionStringBuilder(endpoint)
			client, err := azkustodata.New(kcsb)
			if err != nil {
				return fmt.Errorf("creating Kusto client: %w", err)
			}
			defer client.Close()

			return runValidateKQL(cmd, logger, client, database, kqlDir)
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Kusto emulator HTTP endpoint (e.g. http://localhost:8080)")
	cmd.Flags().StringVar(&database, "database", "validationdb", "Database name to create for validation")
	cmd.Flags().StringVar(&kqlDir, "kql-dir", "", "Directory containing .kql files to validate")

	_ = cmd.MarkFlagRequired("endpoint")
	_ = cmd.MarkFlagRequired("kql-dir")

	return cmd
}

func runValidateKQL(cmd *cobra.Command, logger logr.Logger, client *azkustodata.Client, database, kqlDir string) error {
	ctx := cmd.Context()

	logger.Info("Creating database", "database", database)
	createCmd := fmt.Sprintf(
		`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`,
		database, database, database,
	)
	ds, err := client.Mgmt(ctx, "", unsafeStmt(createCmd))
	if err != nil {
		return fmt.Errorf("creating database: %w", err)
	}
	logQuery(logger, "", createCmd)
	logDataset(logger, ds)

	files, err := filepath.Glob(filepath.Join(kqlDir, "*.kql"))
	if err != nil {
		return fmt.Errorf("finding .kql files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no .kql files found in %s", kqlDir)
	}

	logger.Info("Found .kql files", "count", len(files))

	var failed []string
	for _, file := range files {
		name := filepath.Base(file)
		if err := validateFile(ctx, logger, client, database, file); err != nil {
			logger.Error(err, "FAIL", "file", name)
			failed = append(failed, name)
		} else {
			logger.Info("OK", "file", name)
		}
	}

	tables, err := showTables(ctx, logger, client, database)
	if err != nil {
		return fmt.Errorf("listing tables: %w", err)
	}
	logger.Info("Tables in database", "tables", tables)

	if len(failed) > 0 {
		return fmt.Errorf("validation failed for: %s", strings.Join(failed, ", "))
	}

	logger.Info("All .kql files validated successfully", "count", len(files))
	return nil
}

func validateFile(ctx context.Context, logger logr.Logger, client *azkustodata.Client, database, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	cmd := fmt.Sprintf(".execute database script <|\n%s", string(content))
	logQuery(logger, database, cmd)
	dataset, err := client.Mgmt(ctx, database, unsafeStmt(cmd))
	if err != nil {
		if strings.Contains(err.Error(), "not supported") ||
			strings.Contains(err.Error(), "Unknown") ||
			strings.Contains(err.Error(), "unrecognized") {
			logger.Info("  .execute database script not supported, falling back to individual commands")
			return validateFileIndividual(ctx, logger, client, database, string(content))
		}
		return err
	}
	logDataset(logger, dataset)

	return checkScriptResults(dataset)
}

func checkScriptResults(dataset v1.Dataset) error {
	for _, table := range dataset.Tables() {
		hasResultCol := table.ColumnByName("Result").Name() == "Result"
		if !hasResultCol {
			continue
		}
		for _, row := range table.Rows() {
			result, err := row.StringByName("Result")
			if err != nil {
				continue
			}
			if result == "Failed" {
				reason, _ := row.StringByName("Reason")
				if reason != "" {
					return fmt.Errorf("command failed: %s", reason)
				}
				return fmt.Errorf("command failed (no reason provided)")
			}
		}
	}
	return nil
}

func validateFileIndividual(ctx context.Context, logger logr.Logger, client *azkustodata.Client, database, content string) error {
	commands := splitCommands(content)
	for i, cmd := range commands {
		logQuery(logger, database, cmd)
		ds, err := client.Mgmt(ctx, database, unsafeStmt(cmd))
		if err != nil {
			return fmt.Errorf("command %d: %w", i+1, err)
		}
		logDataset(logger, ds)
	}
	return nil
}

// splitCommands splits a KQL file into individual management commands.
// Each command starts with '.' at column 0.
func splitCommands(content string) []string {
	lines := strings.Split(content, "\n")
	var commands []string
	var current strings.Builder

	for _, line := range lines {
		if len(line) > 0 && line[0] == '.' && current.Len() > 0 {
			cmd := strings.TrimSpace(current.String())
			if cmd != "" {
				commands = append(commands, cmd)
			}
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		cmd := strings.TrimSpace(current.String())
		if cmd != "" {
			commands = append(commands, cmd)
		}
	}

	return commands
}

func showTables(ctx context.Context, logger logr.Logger, client *azkustodata.Client, database string) ([]string, error) {
	logQuery(logger, database, ".show tables")
	dataset, err := client.Mgmt(ctx, database, kql.New(".show tables"))
	if err != nil {
		return nil, err
	}
	logDataset(logger, dataset)

	var tables []string
	for _, table := range dataset.Tables() {
		for _, row := range table.Rows() {
			name, err := row.StringByName("TableName")
			if err != nil {
				continue
			}
			tables = append(tables, name)
		}
	}

	return tables, nil
}

func unsafeStmt(cmd string) *kql.Builder {
	return (&kql.Builder{}).AddUnsafe(cmd)
}

func logQuery(logger logr.Logger, db, cmd string) {
	logger.V(1).Info("Executing command", "db", db, "command", cmd)
}

func logDataset(logger logr.Logger, dataset v1.Dataset) {
	for _, table := range dataset.Tables() {
		logger.V(1).Info("Response", "table", table.Name(), "rows", len(table.Rows()))
		for _, row := range table.Rows() {
			logger.V(2).Info("  Row", "data", row.String())
		}
	}
}
