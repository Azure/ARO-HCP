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
	"errors"
	"time"

	"github.com/go-logr/logr"

	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const DefaultStampCooldownPeriod = 10 * time.Minute

const DefaultInformerResyncPeriod = 5 * time.Minute

var ErrStampNotApproved = errors.New("parent stamp is not approved")

// StampKey identifies a Stamp in the workqueue.
type StampKey struct {
	StampIdentifier string
}

func (k StampKey) String() string {
	return k.StampIdentifier
}

func (k StampKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToStampResourceID(k.StampIdentifier))
}

func (k StampKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

// StampSyncer is the interface that concrete stamp controllers implement.
type StampSyncer interface {
	SyncOnce(ctx context.Context, key StampKey) error
}

// StampWatchingControllerConfig tunes the controller's cooldown behavior.
type StampWatchingControllerConfig struct {
	// Cooldown overrides the default time-based cooldown checker.
	// When set, CooldownPeriod and Clock are ignored.
	Cooldown controllerutils.CooldownChecker

	CooldownPeriod time.Duration
	Clock          utilsclock.PassiveClock
}

func (c StampWatchingControllerConfig) withDefaults() StampWatchingControllerConfig {
	if c.CooldownPeriod == 0 {
		c.CooldownPeriod = DefaultStampCooldownPeriod
	}
	if c.Clock == nil {
		c.Clock = utilsclock.RealClock{}
	}
	return c
}

// NewStampWatchingController creates a controller and delegates
// reconciliation to the syncer. Call QueueForInformers to register informers.
func NewStampWatchingController(
	name string,
	syncer StampSyncer,
	cfg StampWatchingControllerConfig,
) Controller {
	cfg = cfg.withDefaults()

	var cooldown controllerutils.CooldownChecker
	if cfg.Cooldown != nil {
		cooldown = cfg.Cooldown
	} else {
		checker := controllerutils.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
		checker.SetClock(cfg.Clock)
		cooldown = checker
	}

	adapter := &genericStampSyncer{syncer: syncer, cooldown: cooldown}
	return controllerutils.NewGenericWatchingController(name, fleetapi.StampResourceType, adapter, ReconcileTotal)
}

// genericStampSyncer adapts a StampSyncer into a controllerutils.GenericSyncer[StampKey].
type genericStampSyncer struct {
	syncer   StampSyncer
	cooldown controllerutils.CooldownChecker
}

func (a *genericStampSyncer) SyncOnce(ctx context.Context, key StampKey) error {
	return a.syncer.SyncOnce(ctx, key)
}

func (a *genericStampSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return a.cooldown
}

func (a *genericStampSyncer) MakeKey(resourceID *azcorearm.ResourceID) StampKey {
	return StampKey{StampIdentifier: resourceID.Name}
}
