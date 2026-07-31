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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/statusutil"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// nodePoolRequirementsValidAggregatorControllerName is the controller name used for
	// metrics labels, ctx values, log fields, and the Controller document name.
	nodePoolRequirementsValidAggregatorControllerName = "NodePoolRequirementsValidAggregator"
)

// nodePoolRequirementsValidAggregator surfaces ServiceProviderNodePool.Status.Validations
// up onto HCPOpenShiftClusterNodePool.Status.UserFacingConditions as a single
// RequirementsValid condition.
//
// Failed or Unknown validations drive RequirementsValid=False/Degraded, with
// messages aggregated in the same "Source: message" form used by the Degraded
// aggregator. When every validation is True (or none exist) the condition is True/Valid.
type nodePoolRequirementsValidAggregator struct {
	nodePoolLister                listers.NodePoolLister
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister
	resourcesDBClient             database.ResourcesDBClient
}

var _ controllerutils.NodePoolSyncer = (*nodePoolRequirementsValidAggregator)(nil)

// NewNodePoolRequirementsValidAggregatorController creates a controller that
// aggregates ServiceProviderNodePool validations onto the node pool's
// Status.UserFacingConditions as RequirementsValid.
func NewNodePoolRequirementsValidAggregatorController(
	resourcesDBClient database.ResourcesDBClient,
	nodePoolLister listers.NodePoolLister,
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &nodePoolRequirementsValidAggregator{
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		resourcesDBClient:             resourcesDBClient,
	}
	return controllerutils.NewNodePoolWatchingController(
		nodePoolRequirementsValidAggregatorControllerName,
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *nodePoolRequirementsValidAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existing, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool from cache: %w", err))
	}
	if !c.needsWork(existing) {
		return nil
	}

	serviceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderNodePool will populate it. We'll be re-enqueued via the ServiceProviderNodePool informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from cache: %w", err))
	}

	aggregated := statusutil.AggregateRequirementsValidCondition(serviceProviderNodePool.Status.Validations)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, aggregated)
	if equality.Semantic.DeepEqual(existing.Status.UserFacingConditions, replacement.Status.UserFacingConditions) {
		return nil
	}

	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace NodePool: %w", err))
	}
	return nil
}

func (c *nodePoolRequirementsValidAggregator) needsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil
}
