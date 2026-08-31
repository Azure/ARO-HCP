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
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDegradedAggregator rolls per-controller Degraded conditions up
// onto HCPOpenShiftClusterNodePool.Status.Conditions. See the package and
// clusterDegradedAggregator docs for the overall design.
type nodePoolDegradedAggregator struct {
	nodePoolLister    corelisters.NodePoolLister
	controllerLister  corelisters.ControllerLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	inertia           statusutils.Inertia
	clock             utilsclock.PassiveClock
	firstObservedBad  *statusutils.FirstObservedBadCache
	// applyDesireLister and readDesireLister expose the node pool's
	// kube-applier ApplyDesires/ReadDesires so their (already-True) Degraded
	// conditions can be folded into the same aggregate Degraded condition.
	applyDesireLister kubeapplierlisters.ApplyDesireLister
	readDesireLister  kubeapplierlisters.ReadDesireLister
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDegradedAggregator)(nil)

// nodePoolDegradedAggregatorInertia is the inertia config used by the
// node-pool aggregator. Same shape as clusterDegradedAggregatorInertia
// and kept independent so node-pool-specific controllers can be tuned
// without affecting cluster-scoped propagation.
func nodePoolDegradedAggregatorInertia() statusutils.Inertia {
	return statusutils.MustNewInertia(statusutils.DefaultInertia).Inertia
}

// NewNodePoolDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given HCPOpenShiftClusterNodePool onto the node pool's
// Status.Conditions.
//
// See NewClusterDegradedAggregatorController for the clock semantics —
// they are identical across the three aggregators.
func NewNodePoolDegradedAggregatorController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	nodePoolLister corelisters.NodePoolLister,
	controllerLister corelisters.ControllerLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clock utilsclock.PassiveClock,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	// The per-node-pool desire listers are already reachable from the union
	// kube-applier informers handed to this constructor, so no additional
	// wiring in the caller is needed.
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()
	syncer := &nodePoolDegradedAggregator{
		nodePoolLister:    nodePoolLister,
		controllerLister:  controllerLister,
		resourcesDBClient: resourcesDBClient,
		inertia:           nodePoolDegradedAggregatorInertia(),
		clock:             clock,
		firstObservedBad:  statusutils.NewFirstObservedBadCache(clock),
		applyDesireLister: applyDesireLister,
		readDesireLister:  readDesireLister,
	}
	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDegradedAggregator",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *nodePoolDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existing, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool from cache: %w", err))
	}

	controllers, err := c.controllerLister.ListForNodePool(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Controllers from cache: %w", err))
	}

	// ListForNodePool returns exactly the node-pool-scoped desires for this node
	// pool (desires nested under a node pool are node-pool-scoped), so no extra
	// scope filtering is needed here — cluster-scoped and other node pools'
	// desires are not returned.
	applyDesires, err := c.applyDesireLister.ListForNodePool(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ApplyDesires from cache: %w", err))
	}

	readDesires, err := c.readDesireLister.ListForNodePool(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ReadDesires from cache: %w", err))
	}

	// Fold the node pool's controllers together with any degraded
	// node-pool-scoped ApplyDesire/ReadDesire into a single "Degraded"
	// condition. Unlike controllers, only actually-degraded desires are counted
	// (see statusutils.CollectDegradedDesireConditions); healthy or
	// not-yet-reported desires contribute nothing to the aggregate. With
	// report-only-degraded filtering, an all-healthy node pool produces zero
	// sources and UnionCondition returns Degraded=Unknown/NoData ("no data").
	sources := statusutils.CollectDegradedConditions(controllers, c.firstObservedBad)
	sources = append(sources, statusutils.CollectDegradedDesireConditions(
		statusutils.ApplyDesireSourcePrefix, applyDesires,
		func(d *kubeapplierapi.ApplyDesire) []metav1.Condition { return d.Status.Conditions },
	)...)
	sources = append(sources, statusutils.CollectDegradedDesireConditions(
		statusutils.ReadDesireSourcePrefix, readDesires,
		func(d *kubeapplierapi.ReadDesire) []metav1.Condition { return d.Status.Conditions },
	)...)

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

	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace NodePool: %w", err))
	}
	return nil
}
