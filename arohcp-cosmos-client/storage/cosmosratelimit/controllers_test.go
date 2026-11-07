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

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestControllerBudgets(t *testing.T) {
	for _, tc := range []struct {
		name            string
		utilization     float64
		normal, cleanup float64
	}{
		{name: "default 20 percent headroom", normal: 800, cleanup: 80},
		{name: "additional headroom", utilization: 0.5, normal: 500, cleanup: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fractions := map[string]float64{"cleanup": 0.1}
			limits, err := NewControllerRateLimits(ControllerRateLimitOptions{
				TotalRUsPerSecond: 10000, ControllerCount: 10, Utilization: tc.utilization, ControllerFractions: fractions,
			})
			require.NoError(t, err)
			fractions["cleanup"] = 1 // Caller's later mutation must not change allocation.
			normal, err := limits.ForController("normal")
			require.NoError(t, err)
			cleanup, err := limits.ForController("cleanup")
			require.NoError(t, err)
			require.Equal(t, tc.normal, normal.capacity)
			require.Equal(t, tc.normal, normal.refillPerSecond)
			require.Equal(t, tc.cleanup, cleanup.capacity)
			require.Equal(t, tc.cleanup, cleanup.refillPerSecond)
			require.Equal(t, tc.cleanup, cleanup.tokens)
		})
	}
}

func TestControllerBudgetIsolationAndReuse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits, err := NewControllerRateLimits(ControllerRateLimitOptions{
			TotalRUsPerSecond: 1000, ControllerCount: 2, ControllerFractions: map[string]float64{"cleanup": 0.1},
		})
		require.NoError(t, err)
		cleanup, err := limits.ForController("cleanup")
		require.NoError(t, err)
		cleanup.Consume(120) // 40 capacity, 80 debt, 40 RU/s => 2 seconds.
		same, err := limits.ForController("cleanup")
		require.NoError(t, err)
		require.Same(t, cleanup, same)
		start := time.Now()
		require.NoError(t, limits.Do(utils.ContextWithControllerName(t.Context(), "normal"), func(context.Context) error { return nil }))
		require.Equal(t, start, time.Now(), "cleanup debt does not block another controller")
		require.NoError(t, limits.Do(utils.ContextWithControllerName(t.Context(), "cleanup"), func(ctx context.Context) error {
			require.NotNil(t, ctx)
			return nil
		}))
		require.Equal(t, 2*time.Second, time.Since(start))
	})
}

func TestControllerBudgetConcurrentLookup(t *testing.T) {
	limits, err := NewControllerRateLimits(ControllerRateLimitOptions{TotalRUsPerSecond: 100, ControllerCount: 1})
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make([]*TokenBucket, 50)
	for i := range results {
		wg.Go(func() {
			bucket, err := limits.ForController("controller")
			require.NoError(t, err)
			results[i] = bucket
		})
	}
	wg.Wait()
	for _, bucket := range results {
		require.Same(t, results[0], bucket)
	}
}

func TestControllerBudgetRejectsUnidentifiedOrExcessControllers(t *testing.T) {
	limits, err := NewControllerRateLimits(ControllerRateLimitOptions{TotalRUsPerSecond: 100, ControllerCount: 1})
	require.NoError(t, err)
	err = limits.Do(t.Context(), func(context.Context) error { t.Fatal("missing name must not issue requests"); return nil })
	require.ErrorContains(t, err, "requires a controller name")
	_, err = limits.ForController("first")
	require.NoError(t, err)
	_, err = limits.ForController("second")
	require.ErrorContains(t, err, "controller count 1 exceeded")
	_, err = limits.ForController("first")
	require.NoError(t, err, "existing controllers still work after excess allocation is rejected")
}

func TestControllerBudgetValidation(t *testing.T) {
	tests := []ControllerRateLimitOptions{
		{},
		{TotalRUsPerSecond: -1, ControllerCount: 1},
		{TotalRUsPerSecond: math.Inf(1), ControllerCount: 1},
		{TotalRUsPerSecond: math.NaN(), ControllerCount: 1},
		{TotalRUsPerSecond: 100, ControllerCount: 0},
		{TotalRUsPerSecond: 100, ControllerCount: -1},
		{TotalRUsPerSecond: 100, ControllerCount: 1, Utilization: -0.1},
		{TotalRUsPerSecond: 100, ControllerCount: 1, Utilization: 1.1},
		{TotalRUsPerSecond: 100, ControllerCount: 1, Utilization: math.NaN()},
		{TotalRUsPerSecond: 100, ControllerCount: 1, Utilization: math.Inf(1)},
		{TotalRUsPerSecond: math.SmallestNonzeroFloat64, ControllerCount: 10},
		{TotalRUsPerSecond: 100, ControllerCount: 1, ControllerFractions: map[string]float64{"": 0.1}},
		{TotalRUsPerSecond: 100, ControllerCount: 1, ControllerFractions: map[string]float64{"one": 0.1, "two": 0.1}},
	}
	for _, fraction := range []float64{0, -0.1, 1.1, math.NaN(), math.Inf(1)} {
		tests = append(tests, ControllerRateLimitOptions{TotalRUsPerSecond: 100, ControllerCount: 1, ControllerFractions: map[string]float64{"cleanup": fraction}})
	}
	for _, options := range tests {
		_, err := NewControllerRateLimits(options)
		require.Error(t, err, "options: %+v", options)
	}
	limits, err := NewControllerRateLimits(ControllerRateLimitOptions{TotalRUsPerSecond: 1, ControllerCount: 10})
	require.NoError(t, err, "fractional per-controller budgets remain valid")
	bucket, err := limits.ForController("small")
	require.NoError(t, err)
	require.InDelta(t, 0.08, bucket.capacity, 1e-10)
}
