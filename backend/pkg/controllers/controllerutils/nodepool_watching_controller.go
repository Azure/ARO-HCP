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

package controllerutils

import (
	"context"
	"errors"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type NodePoolSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPNodePoolKey) error
	CooldownChecker() CooldownChecker
}

type nodePoolWatchingController struct {
	name   string
	syncer NodePoolSyncer

	cosmosClient database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[HCPNodePoolKey]
}

// NewNodePoolWatchingController periodically looks up all NodePools and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
func NewNodePoolWatchingController(
	name string,
	cosmosClient database.DBClient,
	informers informers.BackendInformers,
	resyncDuration time.Duration,
	syncer NodePoolSyncer,
) Controller {
	nodePoolSyncer := &nodePoolWatchingController{
		name:         name,
		cosmosClient: cosmosClient,
		syncer:       syncer,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedItemBasedRateLimiter[HCPNodePoolKey](),
			workqueue.TypedRateLimitingQueueConfig[HCPNodePoolKey]{
				Name: name,
			},
		),
	}
	nodePoolController := newGenericWatchingController(name, api.NodePoolResourceType, nodePoolSyncer)

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if informers != nil {
		nodePoolInformer, _ := informers.NodePools()
		serviceProviderInformer, _ := informers.ServiceProviderClusters()
		err := nodePoolController.QueueForInformers(resyncDuration, nodePoolInformer, serviceProviderInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return nodePoolController
}

func (c *nodePoolWatchingController) SyncOnce(ctx context.Context, key HCPNodePoolKey) error {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *nodePoolWatchingController) CooldownChecker() CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *nodePoolWatchingController) MakeKey(resourceID *azcorearm.ResourceID) HCPNodePoolKey {
	return HCPNodePoolKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Parent.Name,
		HCPNodePoolName:   resourceID.Name,
	}
}
