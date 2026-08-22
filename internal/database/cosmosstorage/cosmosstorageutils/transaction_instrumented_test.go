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
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// mockTransaction is a configurable DBTransaction used to drive the instrumented
// transaction decorator. Execute returns the configured err (and a nil result)
// so tests can exercise both the success and error metric paths, and the
// delegation counters let tests assert the pass-through methods reach the inner
// transaction.
type mockTransaction struct {
	err error
	pk  string

	addStepCalls   int
	onSuccessCalls int
	executeCalls   int
}

var _ DBTransaction = &mockTransaction{}

func (m *mockTransaction) AddStep(details CosmosDBTransactionStepDetails, step CosmosDBTransactionStep) {
	m.addStepCalls++
}

func (m *mockTransaction) GetPartitionKey() string {
	return m.pk
}

func (m *mockTransaction) OnSuccess(callback DBTransactionCallback) {
	m.onSuccessCalls++
}

func (m *mockTransaction) Execute(ctx context.Context, o *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
	m.executeCalls++
	return nil, m.err
}

// transactionCounterValue reads the current value of the
// database_transaction_total series for the given labels. GetMetricWithLabelValues
// creates the series (initialised to zero) if it does not yet exist, so this is
// safe to call before an operation to capture a baseline.
func transactionCounterValue(t *testing.T, m *databaseMetrics, txnType, code string) float64 {
	t.Helper()
	c, err := m.transactionTotal.GetMetricWithLabelValues(txnType, code)
	require.NoError(t, err, "failed to get transaction counter series")
	return testutil.ToFloat64(c)
}

// transactionHistogramSampleCount returns the number of observations recorded on
// the database_transaction_duration_seconds series for the given labels.
func transactionHistogramSampleCount(t *testing.T, m *databaseMetrics, txnType, code string) uint64 {
	t.Helper()
	observer, err := m.transactionDuration.GetMetricWithLabelValues(txnType, code)
	require.NoError(t, err, "failed to get transaction histogram series")
	metric, ok := observer.(prometheus.Metric)
	require.True(t, ok, "histogram observer is not a prometheus.Metric")
	var dtoMetric dto.Metric
	require.NoError(t, metric.Write(&dtoMetric), "failed to write histogram metric")
	return dtoMetric.GetHistogram().GetSampleCount()
}

// TestInstrumentedTransactionRecordsMetrics verifies that Execute increments the
// transaction counter and records a duration observation, with code="200" on
// success and the configured transaction_type label.
func TestInstrumentedTransactionRecordsMetrics(t *testing.T) {
	ctx := context.Background()
	const txnType = "TestTransactionType"

	// A dedicated registry keeps this test's series isolated from every other
	// test (and from the production legacy registry). sharedDatabaseMetrics
	// returns the same collectors the decorator registers for reg, so the
	// assertions below read exactly what Execute records.
	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	txn := InstrumentTransaction(&mockTransaction{}, txnType, reg)

	beforeCount := transactionCounterValue(t, metrics, txnType, "200")
	beforeSamples := transactionHistogramSampleCount(t, metrics, txnType, "200")

	_, err := txn.Execute(ctx, nil)
	require.NoError(t, err, "execute should succeed")

	assert.Equal(t, beforeCount+1, transactionCounterValue(t, metrics, txnType, "200"),
		"counter should increment by one for a successful Execute")
	assert.Equal(t, beforeSamples+1, transactionHistogramSampleCount(t, metrics, txnType, "200"),
		"histogram should record one observation for a successful Execute")
}

// TestInstrumentedTransactionErrorCodes verifies the mapping from Execute result
// to the "code" metric label: nil -> 200, azcore.ResponseError (including
// wrapped) -> its HTTP status, TransactionStepError (including wrapped) -> its
// HTTP status, and any other error -> 500.
func TestInstrumentedTransactionErrorCodes(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"success", nil, "200"},
		{"response_error_not_found", &azcore.ResponseError{StatusCode: http.StatusNotFound}, "404"},
		{"response_error_conflict", &azcore.ResponseError{StatusCode: http.StatusConflict}, "409"},
		{
			"wrapped_response_error",
			fmt.Errorf("batch failed: %w", &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}),
			"412",
		},
		{
			"transaction_step_error",
			NewTransactionStepError(2, 3, http.StatusPreconditionFailed, CosmosDBTransactionStepDetails{}),
			"412",
		},
		{
			"wrapped_transaction_step_error",
			fmt.Errorf("execute failed: %w", NewTransactionStepError(1, 2, http.StatusConflict, CosmosDBTransactionStepDetails{})),
			"409",
		},
		{"generic_error", errors.New("connection reset"), "500"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A dedicated registry per case keeps each series isolated so
			// absolute assertions (sample count == 1) are stable.
			reg := prometheus.NewRegistry()
			metrics := sharedDatabaseMetrics(reg)
			const txnType = "TestErrorCodes"
			txn := InstrumentTransaction(&mockTransaction{err: tc.err}, txnType, reg)

			_, err := txn.Execute(ctx, nil)
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			assert.Equal(t, float64(1), transactionCounterValue(t, metrics, txnType, tc.wantCode),
				"counter for code %s should be one", tc.wantCode)
			assert.Equal(t, uint64(1), transactionHistogramSampleCount(t, metrics, txnType, tc.wantCode),
				"histogram for code %s should record one observation", tc.wantCode)

			// No other code label should have been touched for this transaction type.
			for _, otherCode := range []string{"200", "404", "409", "412", "500"} {
				if otherCode == tc.wantCode {
					continue
				}
				assert.Zero(t, transactionCounterValue(t, metrics, txnType, otherCode),
					"counter for unexpected code %s should be zero", otherCode)
			}
		})
	}
}

// TestInstrumentedTransactionTypeLabel verifies that the transaction_type label
// reflects the value supplied to the constructor and that two decorators with
// different transaction types record to independent series.
func TestInstrumentedTransactionTypeLabel(t *testing.T) {
	ctx := context.Background()

	const typeA = "TransactionTypeA"
	const typeB = "TransactionTypeB"

	// Both decorators share a single registry so the two transaction types are
	// independent series within the same collectors.
	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	txnA := InstrumentTransaction(&mockTransaction{}, typeA, reg)
	txnB := InstrumentTransaction(&mockTransaction{}, typeB, reg)

	beforeA := transactionCounterValue(t, metrics, typeA, "200")
	beforeB := transactionCounterValue(t, metrics, typeB, "200")

	_, err := txnA.Execute(ctx, nil)
	require.NoError(t, err)

	// An Execute on txnA affects only typeA's series.
	assert.Equal(t, beforeA+1, transactionCounterValue(t, metrics, typeA, "200"),
		"typeA counter should increment after an Execute on txnA")
	assert.Equal(t, beforeB, transactionCounterValue(t, metrics, typeB, "200"),
		"typeB counter should be unaffected by an Execute on txnA")

	_, err = txnB.Execute(ctx, nil)
	require.NoError(t, err)

	// An Execute on txnB affects only typeB's series.
	assert.Equal(t, beforeB+1, transactionCounterValue(t, metrics, typeB, "200"),
		"typeB counter should increment after an Execute on txnB")
	assert.Equal(t, beforeA+1, transactionCounterValue(t, metrics, typeA, "200"),
		"typeA counter should be unchanged by an Execute on txnB")
}

// TestInstrumentedTransactionPassthrough verifies that AddStep, OnSuccess and
// GetPartitionKey delegate to the wrapped transaction (they perform no Cosmos
// I/O and are intentionally not instrumented).
func TestInstrumentedTransactionPassthrough(t *testing.T) {
	mock := &mockTransaction{pk: "sub-123"}
	txn := InstrumentTransaction(mock, "TestPassthrough", prometheus.NewRegistry())

	assert.Equal(t, "sub-123", txn.GetPartitionKey(),
		"GetPartitionKey should delegate to the inner transaction")

	txn.AddStep(CosmosDBTransactionStepDetails{}, func(b *azcosmos.TransactionalBatch) (string, error) {
		return "", nil
	})
	assert.Equal(t, 1, mock.addStepCalls, "AddStep should delegate to the inner transaction")

	txn.OnSuccess(func(DBTransactionResult) {})
	assert.Equal(t, 1, mock.onSuccessCalls, "OnSuccess should delegate to the inner transaction")

	// UnwrapTransaction should peel the instrumented decorator back to the mock.
	assert.Same(t, mock, UnwrapTransaction(txn), "UnwrapTransaction should return the inner transaction")
}

// A pair of real ARM resource IDs (and their expected sanitized resource_type
// labels) used by the per-step metric tests. The nested node-pool ID exercises
// the multi-segment resource type path.
const (
	stepClusterResourceID  = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"
	stepNodePoolResourceID = stepClusterResourceID + "/nodePools/mypool"

	stepClusterResourceType  = "Microsoft_RedHatOpenShift_hcpOpenShiftClusters"
	stepNodePoolResourceType = "Microsoft_RedHatOpenShift_hcpOpenShiftClusters_nodePools"
)

// noopStep is a CosmosDBTransactionStep that does nothing; the per-step metrics
// are derived from the step details passed to AddStep, not from the step func.
func noopStep(b *azcosmos.TransactionalBatch) (string, error) { return "", nil }

// stepCounterValue reads the current value of the database_transaction_step_total
// series for the given labels.
func stepCounterValue(t *testing.T, m *databaseMetrics, txnType, verb, resourceType, code string) float64 {
	t.Helper()
	c, err := m.transactionStepTotal.GetMetricWithLabelValues(txnType, verb, resourceType, code)
	require.NoError(t, err, "failed to get transaction step counter series")
	return testutil.ToFloat64(c)
}

// stepHistogramSampleCount returns the number of observations recorded on the
// database_transaction_step_duration_seconds series for the given labels.
func stepHistogramSampleCount(t *testing.T, m *databaseMetrics, txnType, verb, resourceType, code string) uint64 {
	t.Helper()
	observer, err := m.transactionStepDuration.GetMetricWithLabelValues(txnType, verb, resourceType, code)
	require.NoError(t, err, "failed to get transaction step histogram series")
	metric, ok := observer.(prometheus.Metric)
	require.True(t, ok, "histogram observer is not a prometheus.Metric")
	var dtoMetric dto.Metric
	require.NoError(t, metric.Write(&dtoMetric), "failed to write histogram metric")
	return dtoMetric.GetHistogram().GetSampleCount()
}

// TestInstrumentedTransactionRecordsStepMetrics verifies that Execute records a
// per-step counter and duration sample for every step queued via AddStep, each
// labelled with the step's verb (lower-cased ActionType) and resource type
// (parsed from the step's ResourceID), all sharing the batch's success code.
func TestInstrumentedTransactionRecordsStepMetrics(t *testing.T) {
	ctx := context.Background()
	const txnType = "TestStepMetrics"

	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	txn := InstrumentTransaction(&mockTransaction{}, txnType, reg)

	txn.AddStep(CosmosDBTransactionStepDetails{ActionType: "Create", ResourceID: stepClusterResourceID}, noopStep)
	txn.AddStep(CosmosDBTransactionStepDetails{ActionType: "Replace", ResourceID: stepNodePoolResourceID}, noopStep)

	_, err := txn.Execute(ctx, nil)
	require.NoError(t, err, "execute should succeed")

	assert.Equal(t, float64(1), stepCounterValue(t, metrics, txnType, "create", stepClusterResourceType, "200"),
		"create step counter should increment once")
	assert.Equal(t, uint64(1), stepHistogramSampleCount(t, metrics, txnType, "create", stepClusterResourceType, "200"),
		"create step histogram should record one observation")
	assert.Equal(t, float64(1), stepCounterValue(t, metrics, txnType, "replace", stepNodePoolResourceType, "200"),
		"replace step counter should increment once")
	assert.Equal(t, uint64(1), stepHistogramSampleCount(t, metrics, txnType, "replace", stepNodePoolResourceType, "200"),
		"replace step histogram should record one observation")
}

// TestInstrumentedTransactionStepMetricsShareBatchOutcome verifies that when the
// batch fails, every step is recorded under the batch's HTTP status code (not
// 200), reflecting that a TransactionalBatch commits atomically. Repeated steps
// of the same verb/resource type accumulate on the same series.
func TestInstrumentedTransactionStepMetricsShareBatchOutcome(t *testing.T) {
	ctx := context.Background()
	const txnType = "TestStepErrorCode"

	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	wantErr := NewTransactionStepError(0, 2, http.StatusPreconditionFailed, CosmosDBTransactionStepDetails{})
	txn := InstrumentTransaction(&mockTransaction{err: wantErr}, txnType, reg)

	// Two create steps on the same resource type accumulate on one series.
	txn.AddStep(CosmosDBTransactionStepDetails{ActionType: "Create", ResourceID: stepClusterResourceID}, noopStep)
	txn.AddStep(CosmosDBTransactionStepDetails{ActionType: "Create", ResourceID: stepClusterResourceID}, noopStep)

	_, err := txn.Execute(ctx, nil)
	require.Error(t, err, "execute should return the configured error")

	assert.Equal(t, float64(2), stepCounterValue(t, metrics, txnType, "create", stepClusterResourceType, "412"),
		"both create steps should be recorded under the batch's 412 status code")
	assert.Equal(t, uint64(2), stepHistogramSampleCount(t, metrics, txnType, "create", stepClusterResourceType, "412"),
		"both create steps should record a duration observation under 412")
	assert.Zero(t, stepCounterValue(t, metrics, txnType, "create", stepClusterResourceType, "200"),
		"the success series must be untouched on the error path")
}

// TestInstrumentedTransactionNoStepsNoStepMetrics verifies that a transaction
// with no queued steps records the whole-batch transaction metrics but emits no
// per-step samples.
func TestInstrumentedTransactionNoStepsNoStepMetrics(t *testing.T) {
	ctx := context.Background()
	const txnType = "TestNoSteps"

	reg := prometheus.NewRegistry()
	metrics := sharedDatabaseMetrics(reg)
	txn := InstrumentTransaction(&mockTransaction{}, txnType, reg)

	_, err := txn.Execute(ctx, nil)
	require.NoError(t, err, "execute should succeed")

	assert.Equal(t, float64(1), transactionCounterValue(t, metrics, txnType, "200"),
		"the whole-batch transaction counter should still increment")
	assert.Zero(t, stepCounterValue(t, metrics, txnType, "create", stepClusterResourceType, "200"),
		"no per-step samples should be recorded when there are no steps")
}

// TestStepVerb verifies the ActionType -> step_verb mapping: the verb is
// lower-cased, and an empty ActionType becomes "unknown".
func TestStepVerb(t *testing.T) {
	cases := []struct {
		actionType string
		want       string
	}{
		{"Create", "create"},
		{"Replace", "replace"},
		{"Delete", "delete"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, stepVerb(tc.actionType), "stepVerb(%q)", tc.actionType)
	}
}

// TestStepResourceType verifies that step_resource_type is derived from the ARM
// ResourceID when present, falls back to the sanitized Go type otherwise, and
// finally to "unknown" when neither is available.
func TestStepResourceType(t *testing.T) {
	cases := []struct {
		name    string
		details CosmosDBTransactionStepDetails
		want    string
	}{
		{
			name:    "parses_arm_resource_id",
			details: CosmosDBTransactionStepDetails{ResourceID: stepClusterResourceID},
			want:    stepClusterResourceType,
		},
		{
			name:    "parses_nested_arm_resource_id",
			details: CosmosDBTransactionStepDetails{ResourceID: stepNodePoolResourceID},
			want:    stepNodePoolResourceType,
		},
		{
			name:    "falls_back_to_sanitized_go_type",
			details: CosmosDBTransactionStepDetails{GoType: "*coreapi.HCPOpenShiftCluster"},
			want:    "_coreapi_HCPOpenShiftCluster",
		},
		{
			name:    "unknown_when_empty",
			details: CosmosDBTransactionStepDetails{},
			want:    "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stepResourceType(tc.details)
			assert.Equal(t, tc.want, got, "stepResourceType")
			assert.Regexp(t, `^[a-zA-Z0-9_]+$`, got, "label must contain only Prometheus-safe characters")
		})
	}
}
