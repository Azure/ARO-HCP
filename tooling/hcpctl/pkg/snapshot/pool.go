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

package snapshot

import (
	"context"
	"sync"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// workItem represents a single query to be executed in the pool.
type workItem struct {
	// query is the query specification to execute.
	query querySpec
	// data points to the mutable queryData for this scope.
	data *queryData
	// mu protects data from concurrent access.
	mu *sync.Mutex
	// outputDir is the base directory for writing query output.
	// Empty means the query should execute without writing output.
	outputDir string
	// resultRows is populated after execution with the query results.
	// This allows callers to inspect results after the pool completes.
	resultRows []resultRow
	// executed is set to true after the work item has been executed
	// (regardless of success or failure).
	executed bool
	// failed is set to true if the query execution failed.
	failed bool
	// verificationSuite is the suite name for verification reporting.
	verificationSuite string
}

// workItemResult is sent from consumers to the producer when a work item completes.
type workItemResult struct {
	index int
	item  workItem
	rows  []resultRow
	err   error
}

// queryPool runs work items concurrently using a producer/consumer pattern.
// The producer scans pending items and enqueues those whose ready() predicate
// is satisfied. When a consumer finishes an item, the producer re-evaluates
// all pending items, since storeResult may have unlocked new queries.
type queryPool struct {
	gatherer *Gatherer
	input    GatherInput
}

// runPool executes all work items with bounded concurrency. It returns after
// all items that can make progress have completed. Items whose ready()
// predicate is never satisfied are silently skipped.
func (p *queryPool) runPool(ctx context.Context, items []workItem) {
	logger := logr.FromContextOrDiscard(ctx)
	concurrency := p.input.concurrency()

	type itemState int
	const (
		pending itemState = iota
		inflight
		done
	)

	states := make([]itemState, len(items))
	queue := make(chan int, len(items))
	results := make(chan workItemResult, len(items))

	// enqueueReady scans pending items and enqueues those that are ready.
	// Returns the number of newly enqueued items.
	enqueueReady := func() int {
		enqueued := 0
		for i, item := range items {
			if states[i] != pending {
				continue
			}
			if item.query.ready != nil {
				item.mu.Lock()
				ready := item.query.ready(*item.data)
				item.mu.Unlock()
				if !ready {
					continue
				}
			}
			states[i] = inflight
			queue <- i
			enqueued++
		}
		return enqueued
	}

	// Start consumers.
	consumerWg := &sync.WaitGroup{}
	for i := 0; i < concurrency; i++ {
		consumerWg.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer consumerWg.Done()
			for {
				select {
				case idx, open := <-queue:
					if !open {
						return
					}
					item := items[idx]
					// Snapshot the queryData under the lock so we don't race
					// with storeResult calls in the producer.
					item.mu.Lock()
					dataCopy := *item.data
					item.mu.Unlock()
					var rows []resultRow
					var err error
					if item.outputDir != "" {
						rows, err = p.gatherer.executeQuery(ctx, item.query, &dataCopy, item.outputDir, p.input)
					} else {
						rows, err = p.gatherer.executeQueryToDir(ctx, item.query, &dataCopy, "", p.input)
					}
					results <- workItemResult{index: idx, item: item, rows: rows, err: err}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Producer loop.
	inflightCount := 0

	// Bootstrap: enqueue everything that's ready now.
	inflightCount += enqueueReady()

	// If nothing was ready at all, we're done.
	if inflightCount == 0 {
		close(queue)
		consumerWg.Wait()
		return
	}

	for inflightCount > 0 {
		select {
		case res := <-results:
			inflightCount--
			states[res.index] = done
			items[res.index].resultRows = res.rows
			items[res.index].executed = true
			items[res.index].failed = res.err != nil

			if res.err != nil {
				logger.Error(res.err, "Query failed, continuing", "query", res.item.query.key())
			} else if res.item.query.storeResult != nil && len(res.rows) > 0 {
				res.item.mu.Lock()
				if err := res.item.query.storeResult(res.item.data, res.rows); err != nil {
					logger.Error(err, "Ambiguous discovery result, using first row", "query", res.item.query.key())
				}
				res.item.mu.Unlock()
			}

			// Re-evaluate pending items.
			inflightCount += enqueueReady()

		case <-ctx.Done():
			close(queue)
			consumerWg.Wait()
			return
		}
	}

	close(queue)
	consumerWg.Wait()
}
