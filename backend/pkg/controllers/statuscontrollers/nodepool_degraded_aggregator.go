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
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDegradedAggregator rolls per-controller Degraded conditions up
// onto HCPOpenShiftClusterNodePool.Status.Conditions. See the package and
// clusterDegradedAggregator docs for the overall design.
type nodePoolDegradedAggregator struct {
	nodePoolLister    listers.NodePoolLister
	controllerLister  listers.ControllerLister
	resourcesDBClient database.ResourcesDBClient
	inertia           Inertia
	clock             utilsclock.PassiveClock
	firstObservedBad  *firstObservedBadCache
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDegradedAggregator)(nil)

// nodePoolDegradedAggregatorInertia is the inertia config used by the
// node-pool aggregator. Same shape as clusterDegradedAggregatorInertia
// and kept independent so node-pool-specific controllers can be tuned
// without affecting cluster-scoped propagation.
func nodePoolDegradedAggregatorInertia() Inertia {
	return MustNewInertia(DefaultInertia).Inertia
}

// NewNodePoolDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given HCPOpenShiftClusterNodePool onto the node pool's
// Status.Conditions.
//
// See NewClusterDegradedAggregatorController for the clock semantics —
// they are identical across the three aggregators.
func NewNodePoolDegradedAggregatorController(
	resourcesDBClient database.ResourcesDBClient,
	nodePoolLister listers.NodePoolLister,
	controllerLister listers.ControllerLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clock utilsclock.PassiveClock,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	syncer := &nodePoolDegradedAggregator{
		nodePoolLister:    nodePoolLister,
		controllerLister:  controllerLister,
		resourcesDBClient: resourcesDBClient,
		inertia:           nodePoolDegradedAggregatorInertia(),
		clock:             clock,
		firstObservedBad:  newFirstObservedBadCache(clock),
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

func (c *nodePoolDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) (controllerutil.SyncResult, error) {
	existing, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get NodePool from cache: %w", err))
	}

	controllers, err := c.controllerLister.ListForNodePool(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to list Controllers from cache: %w", err))
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
		return controllerutil.SyncResult{}, nil
	}

	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to replace NodePool: %w", err))
	}
	return controllerutil.SyncResult{}, nil
}
