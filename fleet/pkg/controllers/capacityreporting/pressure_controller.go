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

package capacityreporting

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ManagementClusterPressureControllerName = "ManagementClusterPressureController"
	pressureResyncPeriod                    = 1 * time.Minute
)

type pressureSyncer struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
	clock         utilsclock.PassiveClock
	settings      pressureSettings
	enqueueAfter  controllerutils.AfterEnqueuer
}

func NewManagementClusterPressureController(
	managementClusterInformer, schedulingInformer, stampInformer cache.SharedIndexInformer,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
) fleetcontrollers.Controller {
	syncer := &pressureSyncer{
		fleetDBClient: fleetDBClient,
		clock:         utilsclock.RealClock{},
		settings:      defaultPressureSettings(),
	}

	controller := fleetcontrollers.NewStampWatchingController(
		ManagementClusterPressureControllerName,
		stampInformer,
		pressureResyncPeriod,
		syncer,
		fleetcontrollers.StampWatchingControllerConfig{CooldownPeriod: time.Minute},
	)

	controller.AddInterestingResource(fleetapi.StampResourceType, "*")
	controller.AddInterestingResource(fleetapi.ManagementClusterResourceType, fleetapi.ManagementClusterResourceName)
	controller.AddInterestingResource(fleetapi.ManagementClusterSchedulingResourceType, fleetapi.SchedulingResourceName)
	if err := controller.QueueForInformers(0, managementClusterInformer, schedulingInformer); err != nil {
		panic(err)
	}
	syncer.enqueueAfter = controller
	return controller
}

func (s *pressureSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	// Do not create scheduling children for absent management clusters.
	_, err := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	existing, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, s.fleetDBClient, key.StampIdentifier)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	updated := existing.DeepCopy()
	condition := evaluatePressure(existing.Status, now, s.settings)
	condition.LastTransitionTime = metav1.NewTime(now)
	// SetStatusCondition preserves the persisted LastTransitionTime whenever
	// Status is unchanged, including reason/message changes and process restarts.
	meta.SetStatusCondition(&updated.Status.Conditions, condition)
	if controllerutils.NeedsUpdate(existing, updated) {
		crud := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).Scheduling()
		if _, err := crud.Replace(ctx, updated, nil); err != nil {
			return utils.TrackError(err)
		}
	}
	// Expiry is an input change even when the Cosmos change feed is silent.
	// Equality at maxAge is valid; evaluate just after the expiry boundary.
	if s.enqueueAfter != nil {
		for _, input := range []struct {
			timestamp *metav1.Time
			maxAge    time.Duration
		}{
			{existing.Status.ObservedResources.LastReportedAt, capacityReportMaxAge},
			{existing.Status.ScaleCeiling.LastReportedAt, s.settings.scaleCeilingMaxAge},
		} {
			if input.timestamp != nil {
				delay := input.timestamp.Add(input.maxAge).Sub(now) + time.Nanosecond
				if delay > 0 {
					s.enqueueAfter.EnqueueAfter(key, delay)
				}
			}
		}
	}
	return nil
}
