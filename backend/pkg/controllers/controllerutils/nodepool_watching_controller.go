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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type NodePoolSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPNodePoolKey) (controllerutil.SyncResult, error)
}

type nodePoolWatchingController struct {
	name   string
	syncer NodePoolSyncer

	resourcesDBClient database.ResourcesDBClient
}

// NewNodePoolWatchingController periodically looks up all NodePools and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
//
// kubeApplierInformers is optional: when non-nil, the controller also enqueues
// on ReadDesire events from the union kube-applier informer surface. The
// status that the kube-applier writes back lives on the ReadDesire, so a
// ReadDesire update is how this controller learns "the kube-applier
// reported something new about a node pool". Apply/Delete desires are not
// wired in because their status doesn't carry node-pool-state signal.
func NewNodePoolWatchingController(
	name string,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	resyncDuration time.Duration,
	syncer NodePoolSyncer,
) Controller {
	nodePoolSyncer := &nodePoolWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}
	nodePoolController := newGenericWatchingController(name, api.NodePoolResourceType, nodePoolSyncer)

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if informers != nil {
		nodePoolInformer, _ := informers.NodePools()
		serviceProviderNodePoolInformer, _ := informers.ServiceProviderNodePools()
		err := nodePoolController.QueueForInformers(resyncDuration, nodePoolInformer, serviceProviderNodePoolInformer)
		if err != nil {
			panic(err) // coding error
		}

		managementClusterContentInformer, _ := informers.ManagementClusterContents()
		// Limit the max depth of ManagementClusterContent to 1 to only consider the nodepool-scoped ManagementClusterContents
		err = nodePoolController.QueueForInformersWithMaxDepth(resyncDuration, 1, managementClusterContentInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	if kubeApplierInformers != nil {
		// Node-pool-scoped ReadDesires sit one level below the node pool
		// (.../nodePools/<np>/readDesires/<name>), so a maxDepth of 1
		// reaches the node pool and stops there. Cluster-scoped ReadDesires
		// live above the node pool and are ignored on purpose — this
		// controller is "nodepool-scoped only".
		readDesireInformer, _ := kubeApplierInformers.ReadDesires()
		if err := nodePoolController.QueueForInformersWithMaxDepth(resyncDuration, 1, readDesireInformer); err != nil {
			panic(err) // coding error
		}
	}

	return nodePoolController
}

func (c *nodePoolWatchingController) SyncOnce(ctx context.Context, key HCPNodePoolKey) (controllerutil.SyncResult, error) {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController))

	syncResult, syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return syncResult, errors.Join(syncErr, controllerWriteErr)
}

func (c *nodePoolWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

func (c *nodePoolWatchingController) MakeKey(resourceID *azcorearm.ResourceID) HCPNodePoolKey {
	return HCPNodePoolKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Parent.Name,
		HCPNodePoolName:   resourceID.Name,
	}
}
