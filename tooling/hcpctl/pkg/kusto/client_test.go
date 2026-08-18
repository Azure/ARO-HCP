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

package kusto

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
)

// TestPrimaryResultTable_EmptyChannel guards against the SIGSEGV regression:
// when the Tables() channel closes without yielding a table (e.g. the query
// context is cancelled or times out), the receive returns a nil TableResult.
// Calling .Err() on it used to panic with a nil pointer dereference; it must
// now return a clear error instead.
func TestPrimaryResultTable_EmptyChannel(t *testing.T) {
	ch := make(chan azkquery.TableResult)
	close(ch)

	table, err := PrimaryResultTable(ch)

	require.Error(t, err, "closed empty channel must return an error, not panic")
	require.Nil(t, table, "no table expected when the channel closes empty")
	require.Contains(t, err.Error(), "no primary result table", "error should identify the missing primary table")
}

// TestPrimaryResultTable_ResultError confirms the normal error path (a
// TableResult carrying an error) is still surfaced.
func TestPrimaryResultTable_ResultError(t *testing.T) {
	ch := make(chan azkquery.TableResult, 1)
	ch <- azkquery.TableResultError(errors.New("boom"))
	close(ch)

	table, err := PrimaryResultTable(ch)

	require.Error(t, err, "a TableResult error must be surfaced")
	require.Nil(t, table, "no table expected on primary result error")
	require.Contains(t, err.Error(), "failed to get primary result", "error should wrap the primary result failure")
}
