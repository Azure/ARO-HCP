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
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustodata/query"
	v1 "github.com/Azure/azure-kusto-go/azkustodata/query/v1"
)

type emulatorClient struct {
	inner    *azkustodata.Client
	database string
}

func newEmulatorClient(t *testing.T, endpoint, database string) *emulatorClient {
	t.Helper()

	u, err := url.Parse(endpoint)
	require.NoError(t, err, "failed to parse endpoint URL")
	host := u.Hostname()
	require.True(t, host == "127.0.0.1" || host == "localhost" || host == "::1",
		"CRITICAL ERROR: refusing to connect to non-loopback endpoint %q; integration tests clear all table data and must only run against a local emulator", host)

	kcsb := azkustodata.NewConnectionStringBuilder(endpoint)
	client, err := azkustodata.New(kcsb)
	require.NoError(t, err, "failed to create Kusto emulator client")

	return &emulatorClient{
		inner:    client,
		database: database,
	}
}

func (c *emulatorClient) close() error {
	if c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *emulatorClient) createDatabase(ctx context.Context, t *testing.T) {
	t.Helper()
	createCmd := fmt.Sprintf(
		`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`,
		c.database, c.database, c.database,
	)
	_, err := c.inner.Mgmt(ctx, "", unsafeStmt(createCmd))
	if err != nil && !strings.Contains(err.Error(), "EntityNameAlreadyExistsException") {
		require.NoError(t, err, "failed to create database %q", c.database)
	}
}

func (c *emulatorClient) resetSchema(ctx context.Context, t *testing.T) {
	t.Helper()
	ds, err := c.mgmt(ctx, ".show functions | project Name")
	require.NoError(t, err, "failed to list functions in %q", c.database)
	for _, tbl := range ds.Tables() {
		for _, row := range tbl.Rows() {
			name, err := row.StringByName("Name")
			require.NoError(t, err)
			_, err = c.mgmt(ctx, fmt.Sprintf(".drop function %s", name))
			require.NoError(t, err, "failed to drop function %q in %q", name, c.database)
		}
	}

	ds, err = c.mgmt(ctx, ".show tables | project TableName")
	require.NoError(t, err, "failed to list tables in %q", c.database)
	for _, tbl := range ds.Tables() {
		for _, row := range tbl.Rows() {
			name, err := row.StringByName("TableName")
			require.NoError(t, err)
			_, err = c.mgmt(ctx, fmt.Sprintf(".drop table %s", name))
			require.NoError(t, err, "failed to drop table %q in %q", name, c.database)
		}
	}
}

func (c *emulatorClient) loadKQLFile(ctx context.Context, t *testing.T, file string) {
	t.Helper()
	content, err := os.ReadFile(file)
	require.NoError(t, err, "failed to read %q", file)

	cmd := fmt.Sprintf(".execute database script <|\n%s", string(content))
	dataset, err := c.inner.Mgmt(ctx, c.database, unsafeStmt(cmd))
	require.NoError(t, err, "failed to execute %q", filepath.Base(file))

	checkScriptResults(t, dataset, filepath.Base(file))
}

func (c *emulatorClient) clearAllTables(ctx context.Context, t *testing.T) {
	t.Helper()
	ds, err := c.mgmt(ctx, ".show tables | project TableName")
	require.NoError(t, err, "failed to list tables")

	for _, tbl := range ds.Tables() {
		for _, row := range tbl.Rows() {
			name, err := row.StringByName("TableName")
			require.NoError(t, err, "failed to read table name")
			_, err = c.mgmt(ctx, fmt.Sprintf(".clear table %s data", name))
			require.NoError(t, err, "failed to clear table %q", name)
		}
	}
}

func (c *emulatorClient) mgmt(ctx context.Context, cmd string) (v1.Dataset, error) {
	return c.inner.Mgmt(ctx, c.database, unsafeStmt(cmd))
}

func (c *emulatorClient) query(ctx context.Context, kqlExpr string) (query.Dataset, error) {
	stmt := kql.New("").AddUnsafe(kqlExpr)
	return c.inner.Query(ctx, c.database, stmt)
}

func unsafeStmt(cmd string) *kql.Builder {
	return (&kql.Builder{}).AddUnsafe(cmd)
}

func checkScriptResults(t *testing.T, dataset v1.Dataset, filename string) {
	t.Helper()
	for _, table := range dataset.Tables() {
		if table.ColumnByName("Result") == nil {
			continue
		}
		for _, row := range table.Rows() {
			result, err := row.StringByName("Result")
			if err != nil {
				continue
			}
			if result == "Failed" {
				reason, reasonErr := row.StringByName("Reason")
				if reasonErr != nil {
					t.Fatalf("script %q failed (could not read reason: %v)", filename, reasonErr)
				}
				t.Fatalf("script %q failed: %s", filename, reason)
			}
		}
	}
}
