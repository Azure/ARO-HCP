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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

type manifest struct {
	Databases     map[string]databaseSpec `json:"databases"`
	databaseOrder []string
}

type databaseSpec struct {
	Tables    []string `json:"tables"`
	Functions []string `json:"functions"`
}

// RunFunctionTests discovers and runs all artifact-driven Kusto function test
// cases for a suite. Each subdirectory under the suite is a function name, and
// each subdirectory under that is a test case.
//
// The runner:
//  1. Connects to the Kusto emulator at KUSTO_ENDPOINT
//  2. Reads manifest.json to discover the database topology
//  3. Creates each database and loads its tables and functions from KUSTO_KQL_ROOT
//  4. Walks the artifact tree; each step's 00-key.json declares which database it targets
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
	m := readManifest(t, rootDir)
	require.NotNil(t, m, "artifacts/manifest.json is required")
	require.NotEmpty(t, m.Databases, "manifest.json must define at least one database")

	functionsRoot := filepath.Join(kqlRoot, "functions")
	clients := make(map[string]*emulatorClient, len(m.Databases))

	defer func() {
		for _, c := range clients {
			assert.NoError(t, c.close(), "failed to close Kusto client for %q", c.database)
		}
	}()

	resetAllDatabases(ctx, t, endpoint)

	for _, dbName := range m.databaseOrder {
		spec := m.Databases[dbName]
		client := newEmulatorClient(t, endpoint, dbName)
		clients[dbName] = client

		debugf(t, "Creating database %q", dbName)
		client.createDatabase(ctx, t)

		debugf(t, "[%s] Loading tables: %v", dbName, spec.Tables)
		for _, table := range spec.Tables {
			client.loadKQLFile(ctx, t, filepath.Join(kqlRoot, "tables", table+".kql"))
		}

		debugf(t, "[%s] Loading functions: %v", dbName, spec.Functions)
		for _, funcName := range spec.Functions {
			path := findKQLFile(t, functionsRoot, funcName)
			client.loadKQLFile(ctx, t, path)
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

func readManifest(t *testing.T, suiteDir fs.FS) *manifest {
	t.Helper()
	content, err := fs.ReadFile(suiteDir, "manifest.json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		require.NoError(t, err, "failed to read manifest.json")
	}
	var m manifest
	require.NoError(t, json.Unmarshal(content, &m), "failed to parse manifest.json")
	m.databaseOrder, err = parseDatabaseOrder(content)
	require.NoError(t, err, "failed to parse database order from manifest.json")
	require.Len(t, m.databaseOrder, len(m.Databases),
		"parseDatabaseOrder returned %d keys but manifest has %d databases; manifest.json may be malformed",
		len(m.databaseOrder), len(m.Databases))
	return &m
}

// parseDatabaseOrder extracts database key names from the raw manifest JSON
// in their declared order. This gives manifest authors control over database
// setup ordering, which matters when cross-database functions reference
// tables in other databases.
func parseDatabaseOrder(data []byte) ([]string, error) {
	var raw struct {
		Databases json.RawMessage `json:"databases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshalling manifest: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw.Databases))
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("reading opening token of databases object: %w", err)
	}
	var order []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading database key: %w", err)
		}
		if key, ok := tok.(string); ok {
			order = append(order, key)
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, fmt.Errorf("skipping value for database %q: %w", key, err)
			}
		}
	}
	return order, nil
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

func findKQLFile(t *testing.T, root, name string) string {
	t.Helper()
	target := name + ".kql"
	var matches []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == target {
			matches = append(matches, path)
		}
		return nil
	})
	require.NoError(t, err, "failed to search for %q under %q", target, root)
	require.NotEmpty(t, matches, "function %q not found under %q", name, root)
	require.Len(t, matches, 1,
		"function %q matched multiple files under %q: %v", name, root, matches)
	return matches[0]
}
