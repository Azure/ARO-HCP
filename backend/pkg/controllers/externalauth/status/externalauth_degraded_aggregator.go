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
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/metrics/legacyregistry"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// externalAuthDegradedAggregator rolls per-controller Degraded conditions
// up onto ExternalAuth.Status.Conditions. See the
// package and clusterDegradedAggregator docs for the overall design.
type externalAuthDegradedAggregator struct {
	externalAuthLister corelisters.ExternalAuthLister
	controllerLister   corelisters.ControllerLister
	resourcesDBClient  corecosmosstorage.ResourcesDBClient
	inertia            statusutils.Inertia
	clock              utilsclock.PassiveClock
	firstObservedBad   *statusutils.FirstObservedBadCache
	// degradedGauge backs backend_externalauth_degraded{resource_id, controller}:
	// one series per sub-controller found under the external auth, set to 1
	// when that sub-controller is currently a degraded source (reporting
	// Degraded=True/Unknown) and 0 otherwise. It is independent of inertia —
	// unlike the aggregated condition persisted to Cosmos, it reflects each
	// sub-controller's raw, un-debounced state.
	degradedGauge *prometheus.GaugeVec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthDegradedAggregator)(nil)

const ExternalAuthDegradedAggregatorControllerName = "ExternalAuthDegradedAggregator"

// externalAuthDegradedAggregatorInertia is the inertia config used by the
// external-auth aggregator. Kept independent of the cluster / node-pool
// variants so external-auth-specific controllers can be tuned in
// isolation.
func externalAuthDegradedAggregatorInertia() statusutils.Inertia {
	return statusutils.MustNewInertia(statusutils.DefaultInertia).Inertia
}

// NewExternalAuthDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given ExternalAuth onto the external auth's
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
		degradedGauge: promauto.With(legacyregistry.Registerer()).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_externalauth_degraded",
				Help: "Whether a sub-controller under an external auth is currently reporting Degraded=True (1) or not (0). One series per sub-controller.",
			},
			[]string{"resource_id", "controller"},
		),
	}
	return controllerutils.NewExternalAuthWatchingController(
		ExternalAuthDegradedAggregatorControllerName,
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

	// With report-only-degraded collection an all-healthy external auth produces
	// zero sources and UnionCondition returns the good default (Degraded=False/AsExpected).
	sources := statusutils.CollectDegradedConditions(
		controllers, statusutils.ConditionsOfKnown, "", c.firstObservedBad)
	aggregated := statusutils.UnionCondition(
		statusutils.DegradedConditionType,
		metav1.ConditionFalse,
		c.inertia,
		c.clock.Now(),
		sources...,
	)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, aggregated)
	if equality.Semantic.DeepEqual(existing.Status.Conditions, replacement.Status.Conditions) {
		c.recordDegradedGauge(existing.ResourceID, controllers, sources)
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

	// Set the per-sub-controller gauge after the aggregated Degraded condition
	// has been durably written, so metric and Cosmos state move together.
	c.recordDegradedGauge(existing.ResourceID, controllers, sources)
	return nil
}

// recordDegradedGauge sets backend_externalauth_degraded for every
// sub-controller found under this external auth: 1 for sub-controllers
// present in degradedSources (currently reporting Degraded=True/Unknown), 0
// for every other known sub-controller. Stale series belonging to
// sub-controllers that no longer exist are cleared first so a removed
// sub-controller doesn't leave a dangling series behind.
func (c *externalAuthDegradedAggregator) recordDegradedGauge(
	externalAuthResourceID *azcorearm.ResourceID,
	controllers []*coreapi.Controller,
	degradedSources []statusutils.SourcedCondition,
) {
	if externalAuthResourceID == nil {
		return
	}
	resourceID := strings.ToLower(externalAuthResourceID.String())

	degraded := make(map[string]struct{}, len(degradedSources))
	for _, src := range degradedSources {
		degraded[src.ControllerName] = struct{}{}
	}

	c.degradedGauge.DeletePartialMatch(prometheus.Labels{"resource_id": resourceID})
	for _, controller := range controllers {
		if controller.ResourceID == nil {
			continue
		}
		value := 0.0
		if _, ok := degraded[controller.ResourceID.Name]; ok {
			value = 1.0
		}
		c.degradedGauge.With(prometheus.Labels{
			"resource_id": resourceID,
			"controller":  controller.ResourceID.Name,
		}).Set(value)
	}
}
