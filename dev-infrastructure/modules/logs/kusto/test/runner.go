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
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/kustoctl/pkg/kqlfunc"
)

const (
	envKustoEndpoint = "KUSTO_ENDPOINT"
	envKustoKQLRoot  = "KUSTO_KQL_ROOT"
	envKustoDebug    = "KUSTO_DEBUG"
)

func debugf(t *testing.T, format string, args ...any) {
	t.Helper()
	if len(os.Getenv(envKustoDebug)) != 0 {
		t.Logf(format, args...)
	}
}

// RunFunctionTests discovers and runs all artifact-driven Kusto function test
// cases for a suite. Each subdirectory under the suite is a function name, and
// each subdirectory under that is a test case.
//
// Database/table/function topology is computed in-process on every run,
// directly from the ".kql" files and fixtures on disk (see discoverFunctions,
// discoverTestTables). Databases, tables, and functions are each created in
// one pass across all databases, which keeps creation order independent of
// any particular database, including for cross-database functions.
func RunFunctionTests(t *testing.T, suiteDir fs.FS) {
	t.Helper()

	endpoint := os.Getenv(envKustoEndpoint)
	if len(endpoint) == 0 {
		t.Skipf("skipping: %s not set", envKustoEndpoint)
	}
	kqlRoot := os.Getenv(envKustoKQLRoot)
	if len(kqlRoot) == 0 {
		t.Skipf("skipping: %s not set", envKustoKQLRoot)
	}

	ctx := context.Background()

	rootDir, err := fs.Sub(artifacts, "artifacts")
	require.NoError(t, err, "failed to sub into artifacts root")

	functionsRoot := filepath.Join(kqlRoot, "functions")
	functionsByDatabase := discoverFunctions(t, functionsRoot)
	tablesByDatabase := discoverTestTables(t, rootDir)

	databases := make(map[string]bool, len(functionsByDatabase)+len(tablesByDatabase))
	for db := range functionsByDatabase {
		databases[db] = true
	}
	for db := range tablesByDatabase {
		databases[db] = true
	}
	dbNames := make([]string, 0, len(databases))
	for db := range databases {
		dbNames = append(dbNames, db)
	}
	sort.Strings(dbNames)
	require.NotEmpty(t, dbNames, "discovered no databases from function files or ingestRows fixtures")

	clients := make(map[string]*emulatorClient, len(dbNames))

	defer func() {
		for _, c := range clients {
			assert.NoError(t, c.close(), "failed to close Kusto client for %q", c.database)
		}
	}()

	resetAllDatabases(ctx, t, endpoint)

	for _, dbName := range dbNames {
		client := newEmulatorClient(t, endpoint, dbName)
		clients[dbName] = client
		debugf(t, "Creating database %q", dbName)
		client.createDatabase(ctx, t)
	}

	for _, dbName := range dbNames {
		client := clients[dbName]
		tables := tablesByDatabase[dbName]
		debugf(t, "[%s] Loading tables: %v", dbName, tables)
		for _, table := range tables {
			client.loadKQLFile(ctx, t, filepath.Join(kqlRoot, "tables", table+".kql"))
		}
	}

	for _, dbName := range dbNames {
		client := clients[dbName]
		funcs := functionsByDatabase[dbName]
		names := make([]string, len(funcs))
		for i, f := range funcs {
			names[i] = f.Name
		}
		debugf(t, "[%s] Loading functions: %v", dbName, names)
		for _, f := range funcs {
			client.loadKQLFile(ctx, t, f.Path)
		}
	}

	functionDirs, err := fs.ReadDir(suiteDir, ".")
	require.NoError(t, err, "failed to read suite directory")

	for _, funcDir := range functionDirs {
		if !funcDir.IsDir() {
			continue
		}
		funcFS, err := fs.Sub(suiteDir, funcDir.Name())
		require.NoError(t, err)

		t.Run(funcDir.Name(), func(t *testing.T) {
			testCaseDirs, err := fs.ReadDir(funcFS, ".")
			require.NoError(t, err)

			for _, tcDir := range testCaseDirs {
				if !tcDir.IsDir() {
					continue
				}
				tcFS, err := fs.Sub(funcFS, tcDir.Name())
				require.NoError(t, err)

				t.Run(tcDir.Name(), func(t *testing.T) {
					for _, c := range clients {
						c.clearAllTables(ctx, t)
					}

					steps, err := readSteps(tcFS)
					require.NoError(t, err, "failed to read steps")

					for _, step := range steps {
						step.Run(ctx, t, clients)
					}
				})
			}
		})
	}
}

// discoverFunctions groups every function file by database in dependency
// order, via the same kqlfunc package "kustoctl generate kql-bicep" uses.
// Every function file found is included: main.bicep always deploys the full
// set, so loading the full set here is always a valid superset for testing.
func discoverFunctions(t *testing.T, functionsRoot string) map[string][]kqlfunc.File {
	t.Helper()
	paths, err := kqlfunc.FindFiles(functionsRoot)
	require.NoError(t, err, "failed to find function files under %q", functionsRoot)
	require.NotEmpty(t, paths, "no .kql function files found under %q", functionsRoot)

	files, err := kqlfunc.ParseFiles(paths)
	require.NoError(t, err, "failed to parse function files")
	grouped, err := kqlfunc.SortByDependency(files)
	require.NoError(t, err, "failed to order function files by dependency")
	return grouped
}

// discoverTestTables scans every "ingestRows" step's 00-key.json for its
// declared database/table pair. Filter and enrichment functions operate on a
// generic piped "T" parameter; ingestRows steps are where a concrete table
// name is declared. Scans the whole artifact tree so every suite shares one
// table set.
func discoverTestTables(t *testing.T, root fs.FS) map[string][]string {
	t.Helper()
	tables := make(map[string]map[string]bool)
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		_, stepType, _, ok := parseStepDirName(d.Name())
		if !ok || stepType != "ingestRows" {
			return nil
		}
		keyContent, err := fs.ReadFile(root, filepath.ToSlash(filepath.Join(path, "00-key.json")))
		if err != nil {
			return fmt.Errorf("%s: missing 00-key.json: %w", path, err)
		}
		var key ingestRowsKey
		if err := json.Unmarshal(keyContent, &key); err != nil {
			return fmt.Errorf("%s: failed to parse 00-key.json: %w", path, err)
		}
		if tables[key.Database] == nil {
			tables[key.Database] = make(map[string]bool)
		}
		tables[key.Database][key.Table] = true
		return nil
	})
	require.NoError(t, err, "failed to scan artifact tree for ingestRows steps")

	result := make(map[string][]string, len(tables))
	for db, set := range tables {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		result[db] = names
	}
	return result
}

func resetAllDatabases(ctx context.Context, t *testing.T, endpoint string) {
	t.Helper()
	admin := newEmulatorClient(t, endpoint, "")
	defer func() { assert.NoError(t, admin.close()) }()

	ds, err := admin.inner.Mgmt(ctx, "", unsafeStmt(".show databases | project DatabaseName"))
	require.NoError(t, err, "failed to list databases for reset")

	for _, tbl := range ds.Tables() {
		for _, row := range tbl.Rows() {
			name, err := row.StringByName("DatabaseName")
			require.NoError(t, err)
			dbClient := newEmulatorClient(t, endpoint, name)
			dbClient.resetSchema(ctx, t)
			assert.NoError(t, dbClient.close())
		}
	}
}
