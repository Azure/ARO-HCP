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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
)

// fakeQuery is a minimal Query implementation used to exercise
// processPrimaryResult without rendering a real KQL template.
type fakeQuery struct {
	name string
}

func (f fakeQuery) GetName() string         { return f.name }
func (f fakeQuery) GetQueryType() QueryType { return QueryTypeServices }
func (f fakeQuery) GetDatabase() string     { return "test-db" }
func (f fakeQuery) GetQuery() *kql.Builder  { return nil }
func (f fakeQuery) IsUnlimited() bool       { return false }

// TestProcessPrimaryResultClosedChannel is a regression test for the
// nil-pointer panic that crashed hcpctl when the Kusto result stream was empty
// or already closed (no primary table emitted). Receiving from a closed channel
// of the azkquery.TableResult interface type yields a nil interface value; the
// previous single-value receive then panicked on primaryResult.Err(). Because
// the query runs inside an errgroup goroutine (mustgather.ConcurrentQueries),
// that panic took down the whole process. processPrimaryResult must instead
// return a non-nil error without panicking.
func TestProcessPrimaryResultClosedChannel(t *testing.T) {
	tables := make(chan azkquery.TableResult)
	close(tables)

	out := make(chan TaggedRow, 1)

	var (
		res *QueryResult
		err error
	)
	require.NotPanics(t, func() {
		res, err = processPrimaryResult(context.Background(), tables, fakeQuery{name: "test-query"}, out)
	}, "processPrimaryResult must not panic on a closed result stream")

	require.Error(t, err, "expected an error for a closed/empty result stream")
	assert.Nil(t, res, "no QueryResult should be returned on error")
	assert.Contains(t, err.Error(), "no primary result", "error should describe the empty/closed stream")
	assert.Len(t, out, 0, "no rows should be emitted for an empty result stream")
}

// TestProcessPrimaryResultNilResult covers the companion guard: a nil
// TableResult delivered on an otherwise open channel must also be rejected with
// an error rather than dereferenced.
func TestProcessPrimaryResultNilResult(t *testing.T) {
	tables := make(chan azkquery.TableResult, 1)
	tables <- nil
	close(tables)

	out := make(chan TaggedRow, 1)

	var err error
	require.NotPanics(t, func() {
		_, err = processPrimaryResult(context.Background(), tables, fakeQuery{name: "nil-query"}, out)
	}, "processPrimaryResult must not panic on a nil TableResult")

	require.Error(t, err, "expected an error for a nil primary result")
	assert.Contains(t, err.Error(), "no primary result", "error should describe the missing primary result")
}

// TestProcessPrimaryResultError verifies that an error carried by the primary
// TableResult is wrapped and returned (not swallowed or panicked on).
func TestProcessPrimaryResultError(t *testing.T) {
	sentinel := errors.New("boom")
	tables := make(chan azkquery.TableResult, 1)
	tables <- azkquery.TableResultError(sentinel)
	close(tables)

	out := make(chan TaggedRow, 1)

	_, err := processPrimaryResult(context.Background(), tables, fakeQuery{name: "err-query"}, out)
	require.Error(t, err, "expected the primary result error to be returned")
	assert.ErrorIs(t, err, sentinel, "primary result error should be wrapped with %%w")
	assert.Contains(t, err.Error(), "failed to get primary result", "error should preserve the existing message")
}
