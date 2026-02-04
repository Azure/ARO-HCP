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

package controllerutils

import (
	"context"
	"time"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
)

type CooldownChecker interface {
	// returns true if we can synchronize this particular key
	CanSync(ctx context.Context, key any) bool
}

type TimeBasedCooldownChecker struct {
	clock            utilsclock.PassiveClock
	cooldownDuration time.Duration
	nextExecTime     *lru.Cache
}

func NewTimeBasedCooldownChecker(cooldownDuration time.Duration) *TimeBasedCooldownChecker {
	return &TimeBasedCooldownChecker{
		clock:            utilsclock.RealClock{},
		cooldownDuration: cooldownDuration,
		nextExecTime:     lru.New(1000000), // only holding item keys, so they are small

	}
}

func (c *TimeBasedCooldownChecker) CanSync(ctx context.Context, key any) bool {
	now := c.clock.Now()

	nextExecTime, ok := c.nextExecTime.Get(key)
	if !ok || now.After(nextExecTime.(time.Time)) {
		c.nextExecTime.Add(key, now.Add(c.cooldownDuration))
		return true
	}

	return false
}

type ActiveOperationBasedChecker struct {
	clock                 utilsclock.PassiveClock
	activeOperationLister listers.ActiveOperationLister

	activeOperationTimer   CooldownChecker
	inactiveOperationTimer CooldownChecker
}

func DefaultActiveOperationPrioritizingCooldown(activeOperationLister listers.ActiveOperationLister) *ActiveOperationBasedChecker {
	return NewActiveOperationPrioritizingCooldown(activeOperationLister, 10*time.Second, 5*time.Minute)
}

func NewActiveOperationPrioritizingCooldown(activeOperationLister listers.ActiveOperationLister, activeOperationCooldown, inactiveOperationCooldown time.Duration) *ActiveOperationBasedChecker {
	return &ActiveOperationBasedChecker{
		clock:                  utilsclock.RealClock{},
		activeOperationLister:  activeOperationLister,
		activeOperationTimer:   NewTimeBasedCooldownChecker(activeOperationCooldown),
		inactiveOperationTimer: NewTimeBasedCooldownChecker(inactiveOperationCooldown),
	}
}

func (c *ActiveOperationBasedChecker) CanSync(ctx context.Context, key any) bool {

	var activeOperations []*api.Operation
	var err error
	switch castKey := key.(type) {
	case HCPClusterKey:
		activeOperations, err = c.activeOperationLister.ListActiveOperationsForCluster(ctx, castKey.SubscriptionID, castKey.ResourceGroupName, castKey.HCPClusterName)
	case OperationKey:
		return c.activeOperationTimer.CanSync(ctx, key)
	}

	if err != nil {
		return c.activeOperationTimer.CanSync(ctx, key)
	}
	if len(activeOperations) == 0 {
		return c.inactiveOperationTimer.CanSync(ctx, key)
	}

	return c.activeOperationTimer.CanSync(ctx, key)
}
