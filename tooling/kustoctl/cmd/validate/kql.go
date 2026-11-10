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

package validate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	v1 "github.com/Azure/azure-kusto-go/azkustodata/query/v1"
)

// databaseNameRegex matches valid Kusto database names.
var databaseNameRegex = regexp.MustCompile("^[a-zA-Z0-9_-]{1,63}$")

// Settings is the meta configuration (kql-verify-settings.yaml) describing the
// databases to validate and where their .kql scripts live.
type Settings struct {
	Databases []DatabaseSettings `json:"databases"`
}

// DatabaseSettings maps a single Kusto database to the ordered list of
// directories whose .kql files define it.
type DatabaseSettings struct {
	// Name is the Kusto database name to create. It must match any names used
	// in cross-database references (e.g. database("AllAroHcpLogs")) so those
	// references resolve during validation.
	Name string `json:"name"`
	// KQLDir is the ordered list of directories whose *.kql files define the
	// database. Directories are processed from 0 to n, so a directory must
	// appear after anything its scripts depend on (e.g. the routing directory
	// that creates the tab_* tables before the views directory that unions
	// them). This mirrors the deployment order in script.bicep. Within each
	// directory, files execute in sorted name order (so numeric-prefixed
	// foundation scripts like 00_/01_ run first); directories are not walked
	// recursively. Each path is resolved relative to the settings file's
	// directory unless absolute.
	KQLDir []string `json:"kqlDir"`
}

func newKQLCommand() *cobra.Command {
	var endpoint, configPath string

	cmd := &cobra.Command{
		Use:   "kql",
		Short: "Validate KQL definitions against a Kusto emulator",
		Long: `Validate .kql files by executing them against a running Kusto emulator.

The databases to validate and the ordered list of directories that define each
are read from a meta configuration file (kql-verify-settings.yaml). Every
database listed is created first, then each database's directories are processed
in the order they are listed (0..n); within a directory the *.kql files execute
in sorted name order via ".execute database script". Validation does not stop at
the first failure: all scripts run and all failures are reported at the end.

The directory order is explicit and authoritative: a directory must appear after
anything its scripts depend on (e.g. tables before the views that union them).
Creating every database up front lets cross-database references resolve during
validation. This mirrors how the files are deployed in production via
script.bicep.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logr.FromContextOrDiscard(cmd.Context())

			settings, baseDir, err := loadSettings(configPath)
			if err != nil {
				return err
			}

			kcsb := azkustodata.NewConnectionStringBuilder(endpoint)
			client, err := azkustodata.New(kcsb)
			if err != nil {
				return fmt.Errorf("creating Kusto client: %w", err)
			}
			defer client.Close()

			return runValidateKQL(cmd.Context(), logger, client, baseDir, settings)
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Kusto emulator HTTP endpoint (e.g. http://localhost:8080)")
	cmd.Flags().StringVar(&configPath, "config", "", "Path to the kql-verify-settings.yaml meta configuration file")

	_ = cmd.MarkFlagRequired("endpoint")
	_ = cmd.MarkFlagRequired("config")

	return cmd
}

// loadSettings reads and validates the meta configuration file, returning the
// parsed settings and the base directory (the config file's directory) against
// which relative kqlDir values are resolved.
func loadSettings(configPath string) (*Settings, string, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("reading config %q: %w", configPath, err)
	}

	var settings Settings
	if err := yaml.Unmarshal(content, &settings); err != nil {
		return nil, "", fmt.Errorf("parsing config %q: %w", configPath, err)
	}

	if len(settings.Databases) == 0 {
		return nil, "", fmt.Errorf("config %q lists no databases", configPath)
	}

	for i, dbc := range settings.Databases {
		if !databaseNameRegex.MatchString(dbc.Name) {
			return nil, "", fmt.Errorf("databases[%d]: name %q must be 1-63 characters and contain only letters, numbers, hyphens, and underscores", i, dbc.Name)
		}
		if len(dbc.KQLDir) == 0 {
			return nil, "", fmt.Errorf("databases[%d] (%s): kqlDir is required and must list at least one directory", i, dbc.Name)
		}
	}

	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("resolving config path %q: %w", configPath, err)
	}

	return &settings, filepath.Dir(absConfig), nil
}

func runValidateKQL(ctx context.Context, logger logr.Logger, client *azkustodata.Client, baseDir string, settings *Settings) error {
	// Phase 1: create every database up front so cross-database references
	// resolve when the scripts run.
	for _, dbc := range settings.Databases {
		if err := createDatabase(ctx, logger, client, dbc.Name); err != nil {
			return fmt.Errorf("creating database %s: %w", dbc.Name, err)
		}
	}

	// Phase 2: for each database, process its directories in listed order (0..n)
	// and the .kql files within each directory in sorted name order, collecting
	// all failures.
	var failures []string
	totalFiles := 0
	for _, dbc := range settings.Databases {
		logger.Info("Validating database", "database", dbc.Name, "dirs", len(dbc.KQLDir))

		for _, rawDir := range dbc.KQLDir {
			dir := rawDir
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(baseDir, dir)
			}

			files, err := kqlFilesInDir(dir)
			if err != nil {
				failures = append(failures, fmt.Sprintf("[%s] %s", dbc.Name, err))
				continue
			}
			if len(files) == 0 {
				failures = append(failures, fmt.Sprintf("[%s] no .kql files found in %s", dbc.Name, rawDir))
				continue
			}

			logger.Info("Processing directory", "database", dbc.Name, "dir", rawDir, "files", len(files))
			totalFiles += len(files)

			for _, file := range files {
				script := filepath.Join(rawDir, filepath.Base(file))
				if err := validateFile(ctx, logger, client, dbc.Name, file); err != nil {
					logger.Error(err, "FAIL", "database", dbc.Name, "script", script)
					failures = append(failures, fmt.Sprintf("[%s] %s: %v", dbc.Name, script, err))
				} else {
					logger.Info("OK", "database", dbc.Name, "script", script)
				}
			}
		}

		tables, err := tablesWithDocstring(ctx, logger, client, dbc.Name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("[%s] %v", dbc.Name, err))
		}
		logger.Info("Tables in database", "database", dbc.Name, "tables", tables)
	}

	if len(failures) > 0 {
		return fmt.Errorf("validation failed (%d issue(s)):\n  %s", len(failures), strings.Join(failures, "\n  "))
	}

	logger.Info("All .kql files validated successfully", "databases", len(settings.Databases), "files", totalFiles)
	return nil
}

func createDatabase(ctx context.Context, logger logr.Logger, client *azkustodata.Client, database string) error {
	logger.Info("Creating database", "database", database)
	createCmd := fmt.Sprintf(
		`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`,
		database, database, database,
	)
	logQuery(logger, "", createCmd)
	ds, err := client.Mgmt(ctx, "", unsafeStmt(createCmd))
	if err != nil {
		return err
	}
	logDataset(logger, ds)
	return nil
}

// kqlFilesInDir returns the .kql files directly inside dir (non-recursive) in
// sorted name order. filepath.Glob already returns matches in sorted order, so
// numeric-prefixed foundation scripts (00_, 01_) come before the files that
// depend on them.
func kqlFilesInDir(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.kql"))
	if err != nil {
		return nil, fmt.Errorf("finding .kql files in %s: %w", dir, err)
	}
	return files, nil
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
		return fmt.Errorf("executing command: %w", err)
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

func tablesWithDocstring(ctx context.Context, logger logr.Logger, client *azkustodata.Client, database string) ([]string, error) {
	logQuery(logger, database, ".show tables")
	dataset, err := client.Mgmt(ctx, database, kql.New(".show tables"))
	if err != nil {
		return nil, err
	}
	logDataset(logger, dataset)

	allErrors := make([]error, 0)

	var tables []string
	for _, table := range dataset.Tables() {
		for _, row := range table.Rows() {
			name, err := row.StringByName("TableName")
			if err != nil {
				allErrors = append(allErrors, fmt.Errorf("getting table name: %w", err))
				continue
			}
			docstring, err := row.StringByName("DocString")
			if err != nil {
				allErrors = append(allErrors, fmt.Errorf("getting docstring: %w", err))
				continue
			}
			if docstring != "" {
				logger.V(1).Info("DocString", "table", name, "docstring", docstring)
				tables = append(tables, name)
			} else {
				allErrors = append(allErrors, fmt.Errorf("table %s has no DocString", name))
			}
		}
	}

	return tables, errors.Join(allErrors...)
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
