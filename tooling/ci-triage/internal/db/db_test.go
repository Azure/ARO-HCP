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

package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAndMigrate(t *testing.T) {
	database, err := OpenAndMigrate(":memory:")
	require.NoError(t, err)
	defer database.Close()

	// Verify schema_version table exists and has version 1
	var version int
	err = database.QueryRow("SELECT version FROM schema_version").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, 1, version)

	// Verify tables exist
	tables := []string{"jobs", "test_results", "build_log_cache"}
	for _, table := range tables {
		var name string
		err := database.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		require.NoError(t, err, "table %s should exist", table)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	database, err := OpenAndMigrate(":memory:")
	require.NoError(t, err)
	defer database.Close()

	// Running migrate again should not error
	err = Migrate(database)
	assert.NoError(t, err)
}
