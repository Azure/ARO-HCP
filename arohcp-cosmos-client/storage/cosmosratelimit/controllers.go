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
	"maps"
	"sync"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const DefaultControllerUtilization = 0.8

// ControllerRateLimitOptions divides an application's allocated throughput among
// its controllers. Both the default bucket capacity (RUs) and its refill rate
// (RU/s) are TotalRUsPerSecond * Utilization / ControllerCount, allowing one
// second's worth of burst capacity. Each bucket starts full.
//
// These are process-local budgets, not a distributed limiter. TotalRUsPerSecond
// should be this process's allocation if other services or active replicas share
// the same provisioned throughput. All requests issued by clients bound to a
// bucket count against its allocation, including informers and transactions.
type ControllerRateLimitOptions struct {
	TotalRUsPerSecond float64
	ControllerCount   int
	// Utilization defaults to 0.8, leaving 20% headroom. Values must be in (0, 1].
	Utilization float64
	// ControllerFractions reduces individual controllers' shares. A missing
	// entry means 1.0 (normal); 0.1 gives a cleanup controller 10% of normal.
	// Both capacity and refill rate are scaled. Fractions must be in (0, 1];
	// zero does not disable a controller. Unused shares are not redistributed.
	// Use controller name constants as keys.
	ControllerFractions map[string]float64
}

// ControllerRateLimits owns one shared TokenBucket per controller name. Create
// it once at application startup and bind its buckets to the Cosmos clients
// used by each controller.
//
// It implements cosmosstorageutils.ResourceCRUDLayer by selecting the controller
// from utils.ControllerNameFromContext for optional early admission. Use
// ForController to obtain the bucket required by NewCosmosDatabaseClient;
// the client owns that bucket and accounts for all its requests.
type ControllerRateLimits struct {
	mu              sync.Mutex
	defaultRUs      float64
	controllerCount int
	fractions       map[string]float64
	buckets         map[string]*TokenBucket
}

func NewControllerRateLimits(options ControllerRateLimitOptions) (*ControllerRateLimits, error) {
	if !positiveFinite(options.TotalRUsPerSecond) {
		return nil, fmt.Errorf("total controller RU/s must be finite and positive")
	}
	if options.ControllerCount <= 0 {
		return nil, fmt.Errorf("controller count must be positive")
	}
	if options.Utilization == 0 {
		options.Utilization = DefaultControllerUtilization
	}
	if !positiveFinite(options.Utilization) || options.Utilization > 1 {
		return nil, fmt.Errorf("controller RU utilization must be in (0, 1]")
	}
	defaultRUs := options.TotalRUsPerSecond * options.Utilization / float64(options.ControllerCount)
	if !positiveFinite(defaultRUs) {
		return nil, fmt.Errorf("computed per-controller RU budget must be finite and positive")
	}
	if len(options.ControllerFractions) > options.ControllerCount {
		return nil, fmt.Errorf("controller fraction overrides exceed controller count %d", options.ControllerCount)
	}
	for name, fraction := range options.ControllerFractions {
		if name == "" {
			return nil, fmt.Errorf("controller fraction override requires a controller name")
		}
		if !positiveFinite(fraction) || fraction > 1 || !positiveFinite(defaultRUs*fraction) {
			return nil, fmt.Errorf("controller %q RU fraction must be in (0, 1] and produce a positive budget", name)
		}
	}
	return &ControllerRateLimits{
		defaultRUs:      defaultRUs,
		controllerCount: options.ControllerCount,
		fractions:       maps.Clone(options.ControllerFractions),
		buckets:         map[string]*TokenBucket{},
	}, nil
}

// ForController returns the same bucket on every call for name. Sharing this
// factory preserves debt across new CRUD handles and concurrent workers. More
// distinct names than ControllerCount is a configuration error, preventing
// accidental allocation beyond the configured total budget.
func (c *ControllerRateLimits) ForController(name string) (*TokenBucket, error) {
	if name == "" {
		return nil, fmt.Errorf("controller RU budget requires a controller name")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if bucket, ok := c.buckets[name]; ok {
		return bucket, nil
	}
	if len(c.buckets) >= c.controllerCount {
		return nil, fmt.Errorf("cannot allocate RU budget for controller %q: configured controller count %d exceeded", name, c.controllerCount)
	}
	fraction := 1.0
	if override, ok := c.fractions[name]; ok {
		fraction = override
	}
	budget := c.defaultRUs * fraction
	bucket, err := NewTokenBucket(name, budget, budget)
	if err != nil {
		return nil, err
	}
	c.buckets[name] = bucket
	return bucket, nil
}

// Do selects a controller's bucket using the operation's context. A missing name
// is an error rather than silently issuing an unlimited request. List consumers
// must carry their controller name in the Items context as well as List's.
func (c *ControllerRateLimits) Do(ctx context.Context, operation func(context.Context) error) error {
	name, _ := utils.ControllerNameFromContext(ctx)
	bucket, err := c.ForController(name)
	if err != nil {
		return err
	}
	return bucket.Do(ctx, operation)
}
