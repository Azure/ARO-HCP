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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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
	clusterLister     corelisters.ClusterLister
	controllerLister  corelisters.ControllerLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	inertia           statusutils.Inertia
	clock             utilsclock.PassiveClock
	// firstObservedBad supplies a LastTransitionTime for controllers that
	// have not yet reported a definite Degraded condition (missing or
	// Unknown) so they too get inertia protection.
	firstObservedBad *statusutils.FirstObservedBadCache
	// applyDesireLister and readDesireLister expose the cluster's
	// kube-applier ApplyDesires/ReadDesires so their (already-True) Degraded
	// conditions can be folded into the same aggregate Degraded condition.
	applyDesireLister kubeapplierlisters.ApplyDesireLister
	readDesireLister  kubeapplierlisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*clusterDegradedAggregator)(nil)

// clusterDegradedAggregatorInertia is the inertia config used by the
// cluster aggregator. It is built here, not passed in, so all tuning
// for cluster-scoped Degraded propagation lives next to the controller
// that uses it. Add per-controller-name overrides to the variadic args
// when a specific sub-controller needs a wider (or narrower) window than
// the 30s default.
func clusterDegradedAggregatorInertia() statusutils.Inertia {
	return statusutils.MustNewInertia(statusutils.DefaultInertia).Inertia
}

// NewClusterDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given HCPOpenShiftCluster onto the cluster's Status.Conditions.
//
// clock is used to compute "now" for inertia evaluation; pass nil for
// utilsclock.RealClock{}.
func NewClusterDegradedAggregatorController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	controllerLister corelisters.ControllerLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clock utilsclock.PassiveClock,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	// The per-cluster desire listers are already reachable from the union
	// kube-applier informers handed to this constructor, so no additional
	// wiring in the caller is needed.
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()
	syncer := &clusterDegradedAggregator{
		clusterLister:     clusterLister,
		controllerLister:  controllerLister,
		resourcesDBClient: resourcesDBClient,
		inertia:           clusterDegradedAggregatorInertia(),
		clock:             clock,
		firstObservedBad:  statusutils.NewFirstObservedBadCache(clock),
		applyDesireLister: applyDesireLister,
		readDesireLister:  readDesireLister,
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
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	controllers, err := c.controllerLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Controllers from cache: %w", err))
	}

	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ApplyDesires from cache: %w", err))
	}

	readDesires, err := c.readDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ReadDesires from cache: %w", err))
	}

	// ListForCluster also returns node-pool-nested desires; the cluster
	// aggregator only folds in cluster-scoped desires (immediate parent is the
	// HCPOpenShiftCluster), matching the cluster-scoped, maxDepth-1 desire
	// watch. Node-pool-nested desires are aggregated onto their node pool, not
	// the cluster.
	clusterApplyDesires := clusterScopedDesires(applyDesires, kubeapplierapi.ClusterScopedApplyDesireResourceType)
	clusterReadDesires := clusterScopedDesires(readDesires, kubeapplierapi.ClusterScopedReadDesireResourceType)

	// Fold the cluster's controllers together with any degraded cluster-scoped
	// ApplyDesire/ReadDesire into a single "Degraded" condition. Unlike
	// controllers, only actually-degraded desires are counted (see
	// statusutils.CollectDegradedDesireConditions); healthy or not-yet-reported
	// desires contribute nothing to the aggregate.
	sources := statusutils.CollectDegradedConditions(controllers, c.firstObservedBad)
	sources = append(sources, statusutils.CollectDegradedDesireConditions(
		statusutils.ApplyDesireSourcePrefix, clusterApplyDesires,
		func(d *kubeapplierapi.ApplyDesire) []metav1.Condition { return d.Status.Conditions },
	)...)
	sources = append(sources, statusutils.CollectDegradedDesireConditions(
		statusutils.ReadDesireSourcePrefix, clusterReadDesires,
		func(d *kubeapplierapi.ReadDesire) []metav1.Condition { return d.Status.Conditions },
	)...)

	// With report-only-degraded filtering, an all-healthy cluster produces zero
	// sources and UnionCondition returns the good default (Degraded=False/AsExpected).
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
		return nil
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err = clusterCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}
	return nil
}

// clusterScopedDesires returns only the desires whose resource ID is directly
// cluster-scoped — its resource type equals clusterScopedType, i.e. the desire
// is nested immediately under the HCPOpenShiftCluster and NOT under a
// nodePools/... (or any other) segment. It is used to drop the node-pool-nested
// desires that ListForCluster also returns, keeping this aggregator
// "cluster-scoped only". Desires with a nil resource ID are dropped.
func clusterScopedDesires[T coreapi.CosmosMetadataAccessor](desires []T, clusterScopedType azcorearm.ResourceType) []T {
	out := make([]T, 0, len(desires))
	for _, desire := range desires {
		resourceID := desire.GetResourceID()
		if resourceID != nil && metadataapi.ResourceTypeEqual(resourceID.ResourceType, clusterScopedType) {
			out = append(out, desire)
		}
	}
	return out
}
