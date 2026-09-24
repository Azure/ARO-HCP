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
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type layerTestTransaction struct {
	pk        string
	details   []CosmosDBTransactionStepDetails
	steps     []CosmosDBTransactionStep
	callbacks []DBTransactionCallback
	calls     int
	execute   func(context.Context, *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error)
}

func (t *layerTestTransaction) GetPartitionKey() string { return t.pk }
func (t *layerTestTransaction) AddStep(details CosmosDBTransactionStepDetails, step CosmosDBTransactionStep) {
	t.details = append(t.details, details)
	t.steps = append(t.steps, step)
}
func (t *layerTestTransaction) OnSuccess(callback DBTransactionCallback) {
	t.callbacks = append(t.callbacks, callback)
}
func (t *layerTestTransaction) Execute(ctx context.Context, options *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
	t.calls++
	result, err := t.execute(ctx, options)
	if err == nil {
		for _, callback := range t.callbacks {
			callback(result)
		}
	}
	return result, err
}

type layerTestTransactionResult struct{ DBTransactionResult }

func TestTransactionLayerPreservesAssemblyAndExecution(t *testing.T) {
	result := &layerTestTransactionResult{}
	options := &azcosmos.TransactionalBatchOptions{}
	failure := errors.New("execution failed")
	for name, executeErr := range map[string]error{"success": nil, "failure": failure} {
		t.Run(name, func(t *testing.T) {
			inner := &layerTestTransaction{pk: "partition"}
			inner.execute = func(ctx context.Context, actual *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
				require.Same(t, options, actual)
				require.Equal(t, "layer", ctx.Value(layerContextKey{}))
				return result, executeErr
			}
			before, after, callbacks := 0, 0, 0
			transaction := NewLayeredTransaction(inner, crudLayerFunc(func(ctx context.Context, operation func(context.Context) error) error {
				before++
				err := operation(context.WithValue(ctx, layerContextKey{}, "layer"))
				after++
				return err
			}))
			details := CosmosDBTransactionStepDetails{ActionType: "Create", CosmosID: "item"}
			stepCalled := false
			transaction.AddStep(details, func(*azcosmos.TransactionalBatch) (string, error) {
				stepCalled = true
				return "item", nil
			})
			transaction.OnSuccess(func(actual DBTransactionResult) {
				require.Same(t, result, actual)
				callbacks++
			})
			require.Equal(t, "partition", transaction.GetPartitionKey())
			require.Equal(t, []CosmosDBTransactionStepDetails{details}, inner.details)
			require.Len(t, inner.steps, 1)
			require.Len(t, inner.callbacks, 1)
			require.Zero(t, before, "assembly must not invoke the layer")
			require.False(t, stepCalled, "assembly must not run steps")
			id, err := inner.steps[0](nil)
			require.NoError(t, err)
			require.Equal(t, "item", id)
			require.True(t, stepCalled, "forward the original step")

			actual, err := transaction.Execute(t.Context(), options)
			require.Same(t, result, actual)
			require.ErrorIs(t, err, executeErr)
			require.Equal(t, 1, inner.calls)
			require.Equal(t, 1, before)
			require.Equal(t, 1, after)
			if executeErr == nil {
				require.Equal(t, 1, callbacks)
			} else {
				require.Zero(t, callbacks)
			}
		})
	}
}

func TestRateLimitedTransactionCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := cosmosratelimit.NewTokenBucket("test", 1, 1)
		require.NoError(t, err)
		bucket.Consume(11)
		inner := &layerTestTransaction{pk: "partition"}
		transaction := NewLayeredTransaction(inner, bucket)
		start := time.Now()
		transaction.AddStep(CosmosDBTransactionStepDetails{}, func(*azcosmos.TransactionalBatch) (string, error) { return "item", nil })
		transaction.OnSuccess(func(DBTransactionResult) { t.Fatal("canceled execution must not run success callbacks") })
		require.Equal(t, "partition", transaction.GetPartitionKey())
		require.Equal(t, start, time.Now(), "assembly must not wait for RU debt")
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		result, err := transaction.Execute(ctx, nil)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Nil(t, result)
		require.Zero(t, inner.calls, "canceled wait must not execute the transaction")
		require.NoError(t, bucket.Wait(t.Context()))
		require.Equal(t, 10*time.Second, time.Since(start), "cancellation must not forgive the existing debt")
	})
}

func TestRateLimitedTransactionChargesBatchAndSharesCRUDBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		statuses      []int
		finalDebtWait time.Duration
	}{
		{name: "success", statuses: []int{http.StatusOK}, finalDebtWait: 2500 * time.Millisecond},
		{name: "charged failure", statuses: []int{http.StatusConflict}, finalDebtWait: 2500 * time.Millisecond},
		{name: "retry", statuses: []int{http.StatusInternalServerError, http.StatusOK}, finalDebtWait: 3500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits, err := cosmosratelimit.NewControllerRateLimits(cosmosratelimit.ControllerRateLimitOptions{
					TotalRUsPerSecond: 1.25, ControllerCount: 1, // 1 RU capacity and 1 RU/s after headroom.
				})
				require.NoError(t, err)
				bucket, err := limits.ForController("test")
				require.NoError(t, err)
				ctx := utils.ContextWithControllerName(t.Context(), "test")
				attempts := 0
				start := time.Now()
				pipeline := runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
					PerRetryPolicies: []policy.Policy{cosmosratelimit.NewPolicy(bucket)},
					Retry:            policy.RetryOptions{MaxRetries: 1, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond},
					Transport: layerTestTransport(func(req *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodPost, req.Method)
						require.Equal(t, "True", req.Header.Get("x-ms-cosmos-is-batch-request"))
						require.Less(t, attempts, len(tc.statuses))
						if attempts > 0 {
							require.Equal(t, 2500*time.Millisecond, time.Since(start), "retry must repay the first attempt's debt")
						}
						response := &http.Response{
							StatusCode: tc.statuses[attempts], Request: req, Header: http.Header{},
							Body: io.NopCloser(strings.NewReader("{}")),
						}
						response.Header.Set("x-ms-request-charge", "3.5")
						attempts++
						return response, nil
					}),
				})
				result := &layerTestTransactionResult{}
				failure := errors.New("batch failed")
				inner := &layerTestTransaction{execute: func(ctx context.Context, _ *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
					req, err := runtime.NewRequest(ctx, http.MethodPost, "https://cosmos.test/dbs/test/colls/Resources/docs")
					if err != nil {
						return nil, err
					}
					req.Raw().Header.Set("x-ms-cosmos-is-batch-request", "True")
					response, err := pipeline.Do(req)
					if response != nil {
						require.NoError(t, response.Body.Close())
					}
					if err != nil {
						return nil, err
					}
					if response.StatusCode != http.StatusOK {
						return nil, failure
					}
					return result, nil
				}}
				// Composing the same budget layer must not charge the batch twice.
				transaction := NewLayeredTransaction(NewLayeredTransaction(inner, limits), limits)
				for range 3 {
					transaction.AddStep(CosmosDBTransactionStepDetails{}, nil)
				}
				callbacks := 0
				transaction.OnSuccess(func(DBTransactionResult) { callbacks++ })
				actual, err := transaction.Execute(ctx, nil)
				if tc.statuses[len(tc.statuses)-1] == http.StatusOK {
					require.NoError(t, err)
					require.Same(t, result, actual)
					require.Equal(t, 1, callbacks)
				} else {
					require.ErrorIs(t, err, failure)
					require.Nil(t, actual)
					require.Zero(t, callbacks)
				}
				require.Equal(t, len(tc.statuses), attempts)
				require.Equal(t, 1, inner.calls)
				// A fresh CRUD wrapper must share the debt of the entire batch,
				// charged once per attempt rather than once per queued step.
				start = time.Now()
				crud := NewLayeredResourceCRUD(&recordingCRUD{}, limits)
				_, err = crud.Get(ctx, "item")
				require.NoError(t, err)
				require.Equal(t, tc.finalDebtWait, time.Since(start))
			})
		})
	}
}
