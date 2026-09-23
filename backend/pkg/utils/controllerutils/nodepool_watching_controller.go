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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type NodePoolSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPNodePoolKey) error
}

type nodePoolWatchingController struct {
	name   string
	syncer NodePoolSyncer

	nodePoolLister    corelisters.NodePoolLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewNodePoolWatchingController periodically looks up all NodePools and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
//
// kubeApplierInformers is optional: when non-nil, the controller also enqueues
// on node-pool-scoped ReadDesire and ApplyDesire events from the union
// kube-applier informer surface. The kube-applier writes per-desire status
// (including a Degraded condition) back onto these desires, so a desire update
// is how this controller learns "the kube-applier reported something new about
// a node pool" — the node-pool Degraded aggregator folds node-pool-scoped
// ApplyDesire/ReadDesire Degraded status into the node pool's Degraded
// condition. Only node-pool-scoped desires are watched (cluster-scoped ones are
// ignored, see below). Delete desires are not wired in because their status
// does not carry node-pool-state signal.
func NewNodePoolWatchingController(
	name string,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	resyncDuration time.Duration,
	syncer NodePoolSyncer,
) Controller {
	controller := &nodePoolWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}
	nodePoolController := newGenericWatchingController(name, coreapi.NodePoolResourceType, controller)

	nodePoolInformer, nodePoolLister := informers.NodePools()
	serviceProviderNodePoolInformer, _ := informers.ServiceProviderNodePools()
	controller.nodePoolLister = nodePoolLister

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

	if kubeApplierInformers != nil {
		// Node-pool-scoped ReadDesires/ApplyDesires sit one level below the node
		// pool (.../nodePools/<np>/{read,apply}Desires/<name>), so a maxDepth of 1
		// reaches the node pool and stops there. Cluster-scoped desires live
		// above the node pool and are ignored on purpose — this controller is
		// "nodepool-scoped only". ApplyDesire is wired in alongside ReadDesire
		// because the node-pool Degraded aggregator now folds node-pool-scoped
		// ApplyDesire Degraded status into the node pool condition; Delete
		// desires are still not wired in.
		readDesireInformer, _ := kubeApplierInformers.ReadDesires()
		if err := nodePoolController.QueueForInformersWithMaxDepth(resyncDuration, 1, readDesireInformer); err != nil {
			panic(err) // coding error
		}
		applyDesireInformer, _ := kubeApplierInformers.ApplyDesires()
		if err := nodePoolController.QueueForInformersWithMaxDepth(resyncDuration, 1, applyDesireInformer); err != nil {
			panic(err) // coding error
		}
	}

	return nodePoolController
}

func (c *nodePoolWatchingController) SyncOnce(ctx context.Context, key HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController))

	_, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	switch {
	case cosmosstorageutils.IsNotFoundError(err):
		logger.Info("node pool not found, skipping sync")
		return nil
	case err != nil:
		// do nothing, let the controller decide what it wants to do.
	}

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
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
