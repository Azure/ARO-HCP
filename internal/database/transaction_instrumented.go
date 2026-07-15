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

package database

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// instrumentedTransaction wraps a DBTransaction and records Prometheus
// request-count and request-duration metrics for its Execute call. Unlike the
// instrumentedCRUD verbs — which only measure the in-memory enqueue performed by
// AddCreateToTransaction/AddReplaceToTransaction — this decorator measures the
// single Cosmos TransactionalBatch round-trip that Execute performs to commit
// every queued step atomically. That is where the real database latency (and any
// TransactionStepError) lives, so it complements the CRUD metrics rather than
// duplicating them. It reuses the metric collectors and the codeForError helper
// defined alongside instrumentedCRUD.
type instrumentedTransaction struct {
	inner           DBTransaction
	transactionType string
	metrics         *databaseMetrics
}

var _ DBTransaction = &instrumentedTransaction{}

// InstrumentTransaction returns a DBTransaction that delegates to txn while
// recording database_transaction_total and database_transaction_duration_seconds
// for every Execute, labelling each sample with the caller-supplied
// transaction_type (a stable name identifying the code path, e.g.
// "FrontendClusterCreate"). The collectors are registered on registerer via
// sharedDatabaseMetrics, so the transaction metrics share the memoized
// databaseMetrics instance with the CRUD decorators when both are constructed
// with the same registerer (pass legacyregistry.Registerer() in production).
func InstrumentTransaction(txn DBTransaction, txnType string, registerer prometheus.Registerer) DBTransaction {
	return &instrumentedTransaction{
		inner:           txn,
		transactionType: txnType,
		metrics:         sharedDatabaseMetrics(registerer),
	}
}

// UnwrapTransaction returns the innermost DBTransaction, peeling off any
// instrumentedTransaction decorators added by InstrumentTransaction. Production
// code should treat a DBTransaction opaquely, but test doubles that need the
// concrete underlying transaction (for example to inspect queued steps) must
// call this first: once a transaction is wrapped, a direct type assertion to the
// concrete type would otherwise fail.
func UnwrapTransaction(txn DBTransaction) DBTransaction {
	for {
		wrapped, ok := txn.(*instrumentedTransaction)
		if !ok {
			return txn
		}
		txn = wrapped.inner
	}
}

// observe records one counter increment and one histogram observation for a
// completed transaction. The status code is derived from err by codeForError:
// nil -> 200, an azcore.ResponseError or a transactionStepError (possibly
// wrapped) -> its HTTP status, and any other error -> 500. A failing batch step
// therefore surfaces its real status (e.g. 412 for a precondition failure)
// rather than a generic 500.
func (t *instrumentedTransaction) observe(start time.Time, err error) {
	code := codeForError(err)
	t.metrics.transactionTotal.WithLabelValues(t.transactionType, code).Inc()
	t.metrics.transactionDuration.WithLabelValues(t.transactionType, code).Observe(time.Since(start).Seconds())
}

// Execute instruments the underlying transaction execution — the single Cosmos
// TransactionalBatch round-trip that commits all queued steps atomically — and
// records one sample regardless of whether the batch succeeds or fails.
func (t *instrumentedTransaction) Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (_ DBTransactionResult, err error) {
	start := time.Now()
	defer func() { t.observe(start, err) }()
	return t.inner.Execute(ctx, o)
}

// AddStep, OnSuccess and GetPartitionKey are pure delegations: they only mutate
// or read the in-memory transaction and perform no Cosmos I/O, so they are not
// instrumented. The work they queue is measured when Execute commits it.
func (t *instrumentedTransaction) AddStep(details CosmosDBTransactionStepDetails, step CosmosDBTransactionStep) {
	t.inner.AddStep(details, step)
}

func (t *instrumentedTransaction) OnSuccess(callback DBTransactionCallback) {
	t.inner.OnSuccess(callback)
}

func (t *instrumentedTransaction) GetPartitionKey() string {
	return t.inner.GetPartitionKey()
}
