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

package statuscontrollers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// degradedConditionType is the metav1.Condition.Type our controllers write
// onto their per-controller Controller document and that we aggregate up
// onto the parent.
const degradedConditionType = "Degraded"

// clusterDegradedAggregator rolls per-controller Degraded conditions
// (api.Controller.Status.Conditions[Degraded]) up onto
// HCPOpenShiftCluster.Status.Conditions, using the library-go-style union
// with configurable per-controller inertia.
//
// All reads come from listers — there are no live Cosmos GETs on the
// reconcile path. Writes go through the CRUD layer because that is the
// only way to persist; the lister snapshot used as the basis for the
// Replace carries its own etag so optimistic concurrency still applies,
// and a stale-etag failure just retries on the next reconcile.
type clusterDegradedAggregator struct {
	clusterLister     listers.ClusterLister
	controllerLister  listers.ControllerLister
	resourcesDBClient database.ResourcesDBClient
	inertia           Inertia
	clock             utilsclock.PassiveClock
	// firstObservedBad supplies a LastTransitionTime for controllers that
	// have not yet reported a definite Degraded condition (missing or
	// Unknown) so they too get inertia protection.
	firstObservedBad *firstObservedBadCache
}

var _ controllerutils.ClusterSyncer = (*clusterDegradedAggregator)(nil)

// clusterDegradedAggregatorInertia is the inertia config used by the
// cluster aggregator. It is built here, not passed in, so all tuning
// for cluster-scoped Degraded propagation lives next to the controller
// that uses it. Add per-controller-name overrides to the variadic args
// when a specific sub-controller needs a wider (or narrower) window than
// the 30s default.
func clusterDegradedAggregatorInertia() Inertia {
	return MustNewInertia(DefaultInertia).Inertia
}

// NewClusterDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given HCPOpenShiftCluster onto the cluster's Status.Conditions.
//
// clock is used to compute "now" for inertia evaluation; pass nil for
// utilsclock.RealClock{}.
func NewClusterDegradedAggregatorController(
	resourcesDBClient database.ResourcesDBClient,
	clusterLister listers.ClusterLister,
	controllerLister listers.ControllerLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clock utilsclock.PassiveClock,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	syncer := &clusterDegradedAggregator{
		clusterLister:     clusterLister,
		controllerLister:  controllerLister,
		resourcesDBClient: resourcesDBClient,
		inertia:           clusterDegradedAggregatorInertia(),
		clock:             clock,
		firstObservedBad:  newFirstObservedBadCache(clock),
	}
	return controllerutils.NewClusterWatchingController(
		"ClusterDegradedAggregator",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existing, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	controllers, err := c.controllerLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Controllers from cache: %w", err))
	}

	aggregated := UnionCondition(
		degradedConditionType,
		metav1.ConditionFalse,
		c.inertia,
		c.clock.Now(),
		collectDegradedConditions(controllers, c.firstObservedBad)...,
	)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, aggregated)
	if equality.Semantic.DeepEqual(existing.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err = clusterCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}
	return nil
}
