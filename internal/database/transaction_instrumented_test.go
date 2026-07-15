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
// wrapped) -> its HTTP status, and any other error -> 500.
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
			NewTransactionStepError(2, 3, http.StatusPreconditionFailed),
			"412",
		},
		{
			"wrapped_transaction_step_error",
			fmt.Errorf("execute failed: %w", NewTransactionStepError(1, 2, http.StatusConflict)),
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
}
