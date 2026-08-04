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

package cosmosstorageutils

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIteratorEntry holds one item yielded by a fakeIterator.
type fakeIteratorEntry[T any] struct {
	id   string
	item *T
}

// fakeIterator is a test implementation of DBClientIterator[T] that yields a
// fixed set of items, optionally returning a continuation token and/or an error.
type fakeIterator[T any] struct {
	entries []fakeIteratorEntry[T]
	token   string
	err     error
}

func (f *fakeIterator[T]) Items(_ context.Context) DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
		for _, e := range f.entries {
			if !yield(e.id, e.item) {
				return
			}
		}
	}
}

func (f *fakeIterator[T]) GetContinuationToken() string { return f.token }
func (f *fakeIterator[T]) GetError() error              { return f.err }

func TestListAll(t *testing.T) {
	// testListItem is a minimal type used as the ListAll type parameter in tests.
	type testListItem struct{ value string }

	type listFn = func(context.Context, *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error)

	type testCase struct {
		name      string
		pageSize  int32
		listFn    listFn
		wantItems []string         // nil means expect empty/nil result
		wantErr   string           // non-empty means expect an error containing this substring
		verify    func(*testing.T) // optional: extra assertions beyond result/error
	}

	// --- Stateful setup: state variables defined here are captured by the closures below. ---

	// multi-page: verify the continuation token is forwarded on the second call
	var mpCallCount int
	var mpReceivedTokens []*string
	mpListFn := func(_ context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
		mpCallCount++
		mpReceivedTokens = append(mpReceivedTokens, opts.ContinuationToken)
		if mpCallCount == 1 {
			return &fakeIterator[testListItem]{
				entries: []fakeIteratorEntry[testListItem]{
					{id: "p1-0", item: &testListItem{value: "p1-a"}},
					{id: "p1-1", item: &testListItem{value: "p1-b"}},
				},
				token: "tok-1",
			}, nil
		}
		return &fakeIterator[testListItem]{
			entries: []fakeIteratorEntry[testListItem]{
				{id: "p2-0", item: &testListItem{value: "p2-a"}},
			},
		}, nil
	}

	// three-page: verify listFn is called exactly three times
	var threeCallCount int
	threeListFn := func(_ context.Context, _ *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
		threeCallCount++
		switch threeCallCount {
		case 1:
			return &fakeIterator[testListItem]{
				entries: []fakeIteratorEntry[testListItem]{{id: "0", item: &testListItem{value: "first"}}},
				token:   "tok-a",
			}, nil
		case 2:
			return &fakeIterator[testListItem]{
				entries: []fakeIteratorEntry[testListItem]{{id: "1", item: &testListItem{value: "second"}}},
				token:   "tok-b",
			}, nil
		default:
			return &fakeIterator[testListItem]{
				entries: []fakeIteratorEntry[testListItem]{{id: "2", item: &testListItem{value: "third"}}},
			}, nil
		}
	}

	// error on second page: verify listFn is called exactly twice
	var secondPageCallCount int
	secondPageListFn := func(_ context.Context, _ *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
		secondPageCallCount++
		if secondPageCallCount == 1 {
			return &fakeIterator[testListItem]{
				entries: []fakeIteratorEntry[testListItem]{{id: "id-0", item: &testListItem{value: "first"}}},
				token:   "tok",
			}, nil
		}
		return nil, fmt.Errorf("second page error")
	}

	// page-size hint: capture the PageSizeHint seen by listFn
	var capturedPageSize *int32
	pageSizeListFn := func(_ context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
		capturedPageSize = opts.PageSizeHint
		return &fakeIterator[testListItem]{}, nil
	}

	// --- Test table ---

	tests := []testCase{
		{
			name:     "empty result",
			pageSize: 10,
			listFn: func(_ context.Context, _ *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
				return &fakeIterator[testListItem]{}, nil
			},
		},
		{
			name:      "single page returns all items",
			pageSize:  10,
			wantItems: []string{"a", "b", "c"},
			listFn: func(_ context.Context, _ *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
				return &fakeIterator[testListItem]{
					entries: []fakeIteratorEntry[testListItem]{
						{id: "id-0", item: &testListItem{value: "a"}},
						{id: "id-1", item: &testListItem{value: "b"}},
						{id: "id-2", item: &testListItem{value: "c"}},
					},
				}, nil
			},
		},
		{
			name:      "multi-page follows continuation token",
			pageSize:  5,
			listFn:    mpListFn,
			wantItems: []string{"p1-a", "p1-b", "p2-a"},
			verify: func(t *testing.T) {
				assert.Equal(t, 2, mpCallCount, "expected exactly two listFn calls")
				assert.Nil(t, mpReceivedTokens[0], "first call should have no continuation token")
				require.NotNil(t, mpReceivedTokens[1], "second call should carry the continuation token from page 1")
				assert.Equal(t, "tok-1", *mpReceivedTokens[1])
			},
		},
		{
			name:      "three pages accumulates all items",
			pageSize:  1,
			listFn:    threeListFn,
			wantItems: []string{"first", "second", "third"},
			verify: func(t *testing.T) {
				assert.Equal(t, 3, threeCallCount)
			},
		},
		{
			name:     "pageSize is forwarded as PageSizeHint",
			pageSize: 42,
			listFn:   pageSizeListFn,
			verify: func(t *testing.T) {
				require.NotNil(t, capturedPageSize)
				assert.Equal(t, int32(42), *capturedPageSize)
			},
		},
		{
			name:     "listFn error on first call",
			pageSize: 10,
			listFn: func(_ context.Context, _ *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
				return nil, fmt.Errorf("connection refused")
			},
			wantErr: "failed to list: connection refused",
		},
		{
			name:     "listFn error on second page",
			pageSize: 10,
			listFn:   secondPageListFn,
			wantErr:  "failed to list: second page error",
			verify: func(t *testing.T) {
				assert.Equal(t, 2, secondPageCallCount)
			},
		},
		{
			name:     "iterator error",
			pageSize: 10,
			listFn: func(_ context.Context, _ *DBClientListResourceDocsOptions) (DBClientIterator[testListItem], error) {
				return &fakeIterator[testListItem]{err: fmt.Errorf("unmarshal error")}, nil
			},
			wantErr: "failed iterating: unmarshal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ListAll(context.Background(), tt.pageSize, tt.listFn)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				var gotValues []string
				for _, item := range result {
					gotValues = append(gotValues, item.value)
				}
				assert.Equal(t, tt.wantItems, gotValues)
			}

			if tt.verify != nil {
				tt.verify(t)
			}
		})
	}
}
