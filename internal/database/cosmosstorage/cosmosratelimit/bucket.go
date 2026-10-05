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
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// TokenBucket limits requests by their actual Cosmos request charge. It starts
// full, admits requests when its balance is at least zero, and debits fractional
// RUs after each HTTP attempt. Debt is never discarded, even when a single
// request costs more than the bucket's capacity. Concurrent in-flight requests
// may overshoot the budget; subsequent requests wait for all accumulated debt.
//
// Pass the bucket to a container storage constructor to bind all
// requests from that client to it. Share one bucket across clients and workers
// using the same RU budget. It can also provide an earlier admission check via
// the ResourceCRUDLayer interface, without storing the bucket in context.
type TokenBucket struct {
	unlimited       bool
	metrics         *rateLimitMetrics
	mu              sync.Mutex
	name            string
	capacity        float64
	refillPerSecond float64
	tokens          float64
	updated         time.Time
}

// NewTokenBucket creates a bucket with capacity RUs and a refill rate in RU/s.
// Both values must be finite and positive. name identifies the budget in logs.
func NewTokenBucket(name string, capacity, refillPerSecond float64) (*TokenBucket, error) {
	if !positiveFinite(capacity) || !positiveFinite(refillPerSecond) {
		return nil, fmt.Errorf("cosmos RU bucket capacity and refill rate must be finite and positive")
	}
	return &TokenBucket{metrics: defaultMetrics, name: name, capacity: capacity, refillPerSecond: refillPerSecond, tokens: capacity, updated: time.Now()}, nil
}

// NewUnlimitedTokenBucket explicitly preserves unlimited throughput for callers
// that have not allocated an RU budget. The client still records request metrics.
func NewUnlimitedTokenBucket(name string) *TokenBucket {
	return &TokenBucket{name: name, unlimited: true, metrics: defaultMetrics}
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

// Do implements the CRUD layer contract as an optional early admission check.
// The Cosmos client must independently be constructed with the same bucket to
// account for actual charges and enforce limits on query pages and SDK retries.
func (b *TokenBucket) Do(ctx context.Context, operation func(context.Context) error) error {
	if err := b.Wait(ctx); err != nil {
		return err
	}
	return operation(ctx)
}

// Wait blocks until the balance is nonnegative or ctx is canceled. It does not
// reserve estimated RUs: the actual cost is only known after the response.
func (b *TokenBucket) Wait(ctx context.Context) error {
	if b.unlimited {
		return ctx.Err()
	}
	waitStarted := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		delay, tokens := b.delayLocked(time.Now())
		b.mu.Unlock()
		if delay == 0 {
			return nil
		}
		if !waitStarted {
			finishWait := b.metrics.beginWait(ctx)
			defer finishWait()
			waitStarted = true
		}
		utils.LoggerFromContext(ctx).Info("Cosmos requests are being rate limited; waiting for RU bucket to refill",
			"rate_limiter", b.name, "available_rus", tokens, "refill_rus_per_second", b.refillPerSecond, "estimated_wait", delay.String())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Other in-flight requests may have added more debt while waiting.
		}
	}
}

func (b *TokenBucket) refillLocked(now time.Time) {
	// Advance the balance before consuming so the refill during a long-running
	// request cannot erase the charge when the response arrives.
	if now.After(b.updated) {
		b.tokens = math.Min(b.capacity, b.tokens+now.Sub(b.updated).Seconds()*b.refillPerSecond)
		b.updated = now
	}
}

func (b *TokenBucket) delayLocked(now time.Time) (time.Duration, float64) {
	b.refillLocked(now)
	if b.tokens >= 0 {
		return 0, b.tokens
	}
	nanos := math.Ceil(-b.tokens / b.refillPerSecond * float64(time.Second))
	if nanos >= float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64), b.tokens
	}
	return max(time.Nanosecond, time.Duration(nanos)), b.tokens
}

// Consume subtracts an actual request charge. Invalid or negative charges are
// ignored, matching Cosmos metrics accounting. Zero is a valid no-op charge.
func (b *TokenBucket) Consume(charge float64) {
	if b.unlimited || !positiveFinite(charge) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(time.Now())
	b.tokens = math.Max(-math.MaxFloat64, b.tokens-charge)
}
