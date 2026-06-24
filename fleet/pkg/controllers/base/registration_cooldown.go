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

package base

import (
	"context"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers"
)

const (
	DefaultRegisteredCooldown   = 5 * time.Minute
	DefaultUnregisteredCooldown = 10 * time.Second
)

var _ controllerutils.CooldownChecker = (*RegistrationAwareCooldown)(nil)

// RegistrationAwareCooldown uses a shorter cooldown for management clusters
// that are not yet Ready, mirroring the backend's
// ActiveOperationBasedChecker pattern for clusters with active operations.
type RegistrationAwareCooldown struct {
	managementClusterLister listers.ManagementClusterLister
	registeredTimer         *controllerutils.TimeBasedCooldownChecker
	unregisteredTimer       *controllerutils.TimeBasedCooldownChecker
}

func DefaultRegistrationAwareCooldown(
	managementClusterLister listers.ManagementClusterLister,
) *RegistrationAwareCooldown {
	return NewRegistrationAwareCooldown(managementClusterLister, DefaultRegisteredCooldown, DefaultUnregisteredCooldown)
}

func NewRegistrationAwareCooldown(
	managementClusterLister listers.ManagementClusterLister,
	registeredCooldown, unregisteredCooldown time.Duration,
) *RegistrationAwareCooldown {
	return &RegistrationAwareCooldown{
		managementClusterLister: managementClusterLister,
		registeredTimer:         controllerutils.NewTimeBasedCooldownChecker(registeredCooldown),
		unregisteredTimer:       controllerutils.NewTimeBasedCooldownChecker(unregisteredCooldown),
	}
}

func (c *RegistrationAwareCooldown) SetClock(clock utilsclock.PassiveClock) {
	c.registeredTimer.SetClock(clock)
	c.unregisteredTimer.SetClock(clock)
}

func (c *RegistrationAwareCooldown) CanSync(ctx context.Context, key any) bool {
	stampKey, ok := key.(StampKey)
	if !ok {
		return c.unregisteredTimer.CanSync(ctx, key)
	}

	managementCluster, err := c.managementClusterLister.Get(ctx, stampKey.StampIdentifier)
	if err != nil {
		return c.unregisteredTimer.CanSync(ctx, key)
	}

	if apimeta.IsStatusConditionTrue(managementCluster.Status.Conditions, string(fleet.ManagementClusterConditionReady)) {
		return c.registeredTimer.CanSync(ctx, key)
	}
	return c.unregisteredTimer.CanSync(ctx, key)
}
