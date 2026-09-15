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

package cosmosstorageutils

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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
//
// In addition to the whole-batch transaction metrics, it records a per-step
// breakdown (database_transaction_step_total / _duration_seconds) labelled by the
// step's verb and resource type. The step details are captured as AddStep queues
// them and emitted when Execute commits the batch.
type instrumentedTransaction struct {
	inner           DBTransaction
	transactionType string
	metrics         *databaseMetrics

	// steps accumulates the details of every step queued via AddStep so that
	// Execute can emit a per-step metric sample for each one. A transaction is
	// built and executed by a single goroutine, so no locking is required.
	steps []CosmosDBTransactionStepDetails
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
// nil -> 200, an azcore.ResponseError or a TransactionStepError (possibly
// wrapped) -> its HTTP status, and any other error -> 500. A failing batch step
// therefore surfaces its real status (e.g. 412 for a precondition failure)
// rather than a generic 500.
func (t *instrumentedTransaction) observe(start time.Time, err error) {
	code := codeForError(err)
	duration := time.Since(start).Seconds()

	t.metrics.transactionTotal.WithLabelValues(t.transactionType, code).Inc()
	t.metrics.transactionDuration.WithLabelValues(t.transactionType, code).Observe(duration)

	// Emit a per-step sample for every step this transaction committed. The whole
	// batch commits atomically, so all steps share the batch's outcome code and
	// are attributed the batch's duration (see the transactionStep* field docs).
	for _, step := range t.steps {
		verb := stepVerb(step.ActionType)
		resourceType := stepResourceType(step)
		t.metrics.transactionStepTotal.WithLabelValues(t.transactionType, verb, resourceType, code).Inc()
		t.metrics.transactionStepDuration.WithLabelValues(t.transactionType, verb, resourceType, code).Observe(duration)
	}
}

// stepVerb maps a transaction step's ActionType (e.g. "Create", "Replace",
// "Delete") to the lower-cased verb used as the step_verb label, matching the
// style of the CRUD verb constants ("create", "replace", "delete"). An empty
// ActionType is reported as "unknown" so the label is never blank.
func stepVerb(actionType string) string {
	if actionType == "" {
		return "unknown"
	}
	return strings.ToLower(actionType)
}

// stepResourceType derives the step_resource_type label from a step's details.
// It parses the step's ARM ResourceID and sanitizes the resulting ResourceType
// exactly like the CRUD resource_type label, so both labels share one vocabulary
// (e.g. "Microsoft_RedHatOpenShift_hcpOpenShiftClusters"). If the ResourceID
// cannot be parsed it falls back to the sanitized Go type, and finally to
// "unknown", keeping the label a stable, low-cardinality value.
func stepResourceType(details CosmosDBTransactionStepDetails) string {
	if details.ResourceID != "" {
		if rid, err := azcorearm.ParseResourceID(details.ResourceID); err == nil {
			return sanitizeResourceType(rid.ResourceType)
		}
	}
	if details.GoType != "" {
		return resourceTypeLabelSanitizer.ReplaceAllString(details.GoType, "_")
	}
	return "unknown"
}

// Execute instruments the underlying transaction execution — the single Cosmos
// TransactionalBatch round-trip that commits all queued steps atomically — and
// records one sample regardless of whether the batch succeeds or fails.
func (t *instrumentedTransaction) Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (_ DBTransactionResult, err error) {
	start := time.Now()
	defer func() { t.observe(start, err) }()
	return t.inner.Execute(ctx, o)
}

// AddStep records the step's details for the per-step metrics emitted by Execute,
// then delegates to the inner transaction. It performs no Cosmos I/O itself — the
// queued work is measured when Execute commits it — so it is not timed; it only
// captures the verb/resource-type breakdown that Execute later reports.
func (t *instrumentedTransaction) AddStep(details CosmosDBTransactionStepDetails, step CosmosDBTransactionStep) {
	t.steps = append(t.steps, details)
	t.inner.AddStep(details, step)
}

// OnSuccess and GetPartitionKey are pure delegations: they only mutate or read
// the in-memory transaction and perform no Cosmos I/O, so they are not
// instrumented.

func (t *instrumentedTransaction) OnSuccess(callback DBTransactionCallback) {
	t.inner.OnSuccess(callback)
}

func (t *instrumentedTransaction) GetPartitionKey() string {
	return t.inner.GetPartitionKey()
}
