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

package status

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthDegradedAggregator rolls per-controller Degraded conditions
// up onto HCPOpenShiftClusterExternalAuth.Status.Conditions. See the
// package and clusterDegradedAggregator docs for the overall design.
type externalAuthDegradedAggregator struct {
	externalAuthLister corelisters.ExternalAuthLister
	controllerLister   corelisters.ControllerLister
	resourcesDBClient  corecosmosstorage.ResourcesDBClient
	inertia            statusutils.Inertia
	clock              utilsclock.PassiveClock
	firstObservedBad   *statusutils.FirstObservedBadCache
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthDegradedAggregator)(nil)

// externalAuthDegradedAggregatorInertia is the inertia config used by the
// external-auth aggregator. Kept independent of the cluster / node-pool
// variants so external-auth-specific controllers can be tuned in
// isolation.
func externalAuthDegradedAggregatorInertia() statusutils.Inertia {
	return statusutils.MustNewInertia(statusutils.DefaultInertia).Inertia
}

// NewExternalAuthDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given HCPOpenShiftClusterExternalAuth onto the external auth's
// Status.Conditions.
//
// See NewClusterDegradedAggregatorController for the clock semantics —
// they are identical across the three aggregators.
func NewExternalAuthDegradedAggregatorController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	controllerLister corelisters.ControllerLister,
	informers coreinformers.BackendInformers,
	clock utilsclock.PassiveClock,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	syncer := &externalAuthDegradedAggregator{
		externalAuthLister: externalAuthLister,
		controllerLister:   controllerLister,
		resourcesDBClient:  resourcesDBClient,
		inertia:            externalAuthDegradedAggregatorInertia(),
		clock:              clock,
		firstObservedBad:   statusutils.NewFirstObservedBadCache(clock),
	}
	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthDegradedAggregator",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from cache: %w", err))
	}

	controllers, err := c.controllerLister.ListForExternalAuth(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Controllers from cache: %w", err))
	}

	// With report-only-degraded filtering, an all-healthy external auth produces
	// zero sources and UnionCondition returns the good default (Degraded=False/AsExpected).
	aggregated := statusutils.UnionCondition(
		statusutils.DegradedConditionType,
		metav1.ConditionFalse,
		c.inertia,
		c.clock.Now(),
		statusutils.CollectDegradedConditions(controllers, c.firstObservedBad)...,
	)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, aggregated)
	if equality.Semantic.DeepEqual(existing.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	_, err = externalAuthCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ExternalAuth: %w", err))
	}
	return nil
}
