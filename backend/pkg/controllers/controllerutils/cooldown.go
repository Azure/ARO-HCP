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

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type ActiveOperationBasedChecker struct {
	clock                 utilsclock.PassiveClock
	activeOperationLister listers.ActiveOperationLister

	activeOperationTimer   controllerutil.CooldownChecker
	inactiveOperationTimer controllerutil.CooldownChecker
}

func DefaultActiveOperationPrioritizingCooldown(activeOperationLister listers.ActiveOperationLister) *ActiveOperationBasedChecker {
	return NewActiveOperationPrioritizingCooldown(activeOperationLister, 10*time.Second, 5*time.Minute)
}

func NewActiveOperationPrioritizingCooldown(activeOperationLister listers.ActiveOperationLister, activeOperationCooldown, inactiveOperationCooldown time.Duration) *ActiveOperationBasedChecker {
	return &ActiveOperationBasedChecker{
		clock:                  utilsclock.RealClock{},
		activeOperationLister:  activeOperationLister,
		activeOperationTimer:   controllerutil.NewTimeBasedCooldownChecker(activeOperationCooldown),
		inactiveOperationTimer: controllerutil.NewTimeBasedCooldownChecker(inactiveOperationCooldown),
	}
}

func (c *ActiveOperationBasedChecker) CanSync(ctx context.Context, key any) bool {
	logger := utils.LoggerFromContext(ctx)

	var activeOperations []*api.Operation
	var err error
	switch castKey := key.(type) {
	case HCPClusterKey:
		activeOperations, err = c.activeOperationLister.ListActiveOperationsForCluster(ctx, castKey.SubscriptionID, castKey.ResourceGroupName, castKey.HCPClusterName)
	case OperationKey:
		ret := c.activeOperationTimer.CanSync(ctx, key)
		return ret
	}

	if err != nil {
		logger.Error(err, "failed to list active operations")
		ret := c.activeOperationTimer.CanSync(ctx, key)
		return ret
	}
	if len(activeOperations) == 0 {
		ret := c.inactiveOperationTimer.CanSync(ctx, key)
		return ret
	}

	ret := c.activeOperationTimer.CanSync(ctx, key)
	return ret
}
