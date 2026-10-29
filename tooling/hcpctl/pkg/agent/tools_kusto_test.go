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

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/tabular"
)

type kustoQueryFunc func(context.Context, string) (*tabular.Table, error)

func (f kustoQueryFunc) Query(ctx context.Context, query string) (*tabular.Table, error) {
	return f(ctx, query)
}

func TestCachingKustoClientCachesSuccessfulQueries(t *testing.T) {
	results := map[string]*tabular.Table{
		"print value=1": {Columns: []string{"value"}, Rows: [][]string{{"1"}}},
		"print value=2": {Columns: []string{"value"}, Rows: [][]string{{"2"}}},
	}
	calls := 0
	client := NewCachingKustoClient(kustoQueryFunc(func(_ context.Context, query string) (*tabular.Table, error) {
		calls++
		return results[query], nil
	}))

	for range 2 {
		for query, want := range results {
			got, err := client.Query(t.Context(), query)
			if err != nil {
				t.Fatalf("Query(%q): %v", query, err)
			}
			if got != want {
				t.Errorf("Query(%q) = %p, want original table %p", query, got, want)
			}
		}
	}
	if calls != len(results) {
		t.Errorf("delegate calls = %d, want %d", calls, len(results))
	}
}

func TestCachingKustoClientRetriesErrors(t *testing.T) {
	queryErr := errors.New("query failed")
	table := &tabular.Table{Columns: []string{"value"}, Rows: [][]string{{"1"}}}
	calls := 0
	client := NewCachingKustoClient(kustoQueryFunc(func(context.Context, string) (*tabular.Table, error) {
		calls++
		if calls == 1 {
			return table, queryErr
		}
		return table, nil
	}))

	got, err := client.Query(t.Context(), "print value=1")
	if !errors.Is(err, queryErr) || got != nil {
		t.Fatalf("failed Query = (%v, %v), want (nil, %v)", got, err, queryErr)
	}
	for range 2 {
		got, err = client.Query(t.Context(), "print value=1")
		if err != nil || got != table {
			t.Fatalf("retried Query = (%v, %v), want (%v, nil)", got, err, table)
		}
	}
	if calls != 2 {
		t.Errorf("delegate calls = %d, want 2", calls)
	}
}

func TestCachingKustoClientConcurrentQueries(t *testing.T) {
	for _, keys := range []int{1, 8} {
		t.Run(fmt.Sprintf("%d_keys", keys), func(t *testing.T) {
			const workers = 32
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			started := make(chan struct{}, workers)
			release := make(chan struct{})
			client := NewCachingKustoClient(kustoQueryFunc(func(ctx context.Context, query string) (*tabular.Table, error) {
				started <- struct{}{}
				select {
				case <-release:
					return &tabular.Table{Columns: []string{"query"}, Rows: [][]string{{query}}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}))

			var wg sync.WaitGroup
			defer func() {
				cancel()
				wg.Wait()
			}()
			for i := range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					query := fmt.Sprintf("print value=%d", i%keys)
					for range 20 {
						table, err := client.Query(ctx, query)
						if err != nil {
							t.Errorf("Query(%q): %v", query, err)
							return
						}
						if table == nil || len(table.Rows) != 1 || len(table.Rows[0]) != 1 || table.Rows[0][0] != query {
							t.Errorf("Query(%q) returned wrong result: %+v", query, table)
							return
						}
					}
				}()
			}

			// All cache misses must reach the delegate before any query finishes.
			for range workers {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatalf("queries did not reach the delegate concurrently: %v", ctx.Err())
				}
			}
			close(release)
			wg.Wait()
		})
	}
}
