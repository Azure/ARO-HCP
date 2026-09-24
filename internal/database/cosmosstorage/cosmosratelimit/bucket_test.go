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

package cosmosratelimit

import (
	"context"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestTokenBucketValidation(t *testing.T) {
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := NewTokenBucket("test", value, 1)
		require.Error(t, err)
		_, err = NewTokenBucket("test", 1, value)
		require.Error(t, err)
	}
}

func TestTokenBucketDebtAndRefill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := NewTokenBucket("cleanup", 10, 2)
		require.NoError(t, err)
		start := time.Now()
		bucket.Consume(10)
		require.NoError(t, bucket.Wait(t.Context()), "a zero balance must admit requests")
		require.Equal(t, start, time.Now())
		bucket.Consume(30.5) // Greater than capacity, with fractional RUs.
		var logs []string
		ctx := utils.ContextWithLogger(t.Context(), funcr.New(func(_, message string) { logs = append(logs, message) }, funcr.Options{}))
		require.NoError(t, bucket.Wait(ctx))
		require.Equal(t, 15250*time.Millisecond, time.Since(start))
		require.Len(t, logs, 1)
		require.Contains(t, logs[0], "rate limited")
		require.Contains(t, logs[0], "cleanup")
		require.Contains(t, logs[0], "15.25s")

		time.Sleep(time.Hour)
		bucket.Consume(11)
		start = time.Now()
		require.NoError(t, bucket.Wait(t.Context()))
		require.Equal(t, 500*time.Millisecond, time.Since(start), "idle refill must be capped at capacity")
	})
}

func TestTokenBucketCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := NewTokenBucket("test", 1, 1)
		require.NoError(t, err)
		bucket.Consume(11)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		called := false
		err = bucket.Do(ctx, func(context.Context) error { called = true; return nil })
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.False(t, called)
		require.Equal(t, -10.0, bucket.tokens, "cancellation must not forgive debt")
		start := time.Now()
		require.NoError(t, bucket.Wait(t.Context()))
		require.Equal(t, 9*time.Second, time.Since(start))

		canceled, stop := context.WithCancel(t.Context())
		stop()
		require.ErrorIs(t, bucket.Wait(canceled), context.Canceled, "cancellation also applies to a full bucket")
	})
}

func TestTokenBucketConcurrentChargesAndWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := NewTokenBucket("test", 1, 10)
		require.NoError(t, err)
		var wg sync.WaitGroup
		for range 100 {
			wg.Go(func() { bucket.Consume(0.5) })
		}
		wg.Wait()
		require.Equal(t, -49.0, bucket.tokens)
		start := time.Now()
		for range 5 {
			wg.Go(func() { require.NoError(t, bucket.Wait(t.Context())) })
		}
		synctest.Wait()
		time.Sleep(time.Second)
		bucket.Consume(10) // Extend existing waits by another second.
		wg.Wait()
		require.Equal(t, 5900*time.Millisecond, time.Since(start))
	})
}

func TestTokenBucketIgnoresInvalidCharges(t *testing.T) {
	bucket, err := NewTokenBucket("test", 1, 1)
	require.NoError(t, err)
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		bucket.Consume(value)
	}
	require.Equal(t, 1.0, bucket.tokens)
}

func TestTokenBucketLargeDebtDoesNotOverflowWait(t *testing.T) {
	bucket, err := NewTokenBucket("test", 1, math.SmallestNonzeroFloat64)
	require.NoError(t, err)
	bucket.Consume(math.MaxFloat64)
	bucket.Consume(math.MaxFloat64)
	delay, balance := bucket.delayLocked(bucket.updated)
	require.Equal(t, time.Duration(math.MaxInt64), delay)
	require.False(t, math.IsInf(balance, 0))
}

func TestTokenBucketLayerPreservesContext(t *testing.T) {
	bucket, err := NewTokenBucket("test", 1, 1)
	require.NoError(t, err)
	ctx := t.Context()
	require.NoError(t, bucket.Do(ctx, func(actual context.Context) error {
		require.Same(t, ctx, actual, "the token bucket must not be stored in context")
		return nil
	}))
}

func TestUnlimitedTokenBucket(t *testing.T) {
	bucket := NewUnlimitedTokenBucket("test")
	bucket.Consume(math.MaxFloat64)
	require.NoError(t, bucket.Wait(t.Context()))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, bucket.Wait(ctx), context.Canceled)
}
