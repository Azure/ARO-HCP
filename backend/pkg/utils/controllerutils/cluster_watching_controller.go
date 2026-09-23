// Copyright 2025 Microsoft Corporation
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

type ClusterSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPClusterKey) error
}

type clusterWatchingController struct {
	name   string
	syncer ClusterSyncer

	clusterLister     corelisters.ClusterLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewClusterWatchingController periodically looks up all clusters and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
//
// kubeApplierInformers is optional: when non-nil, the controller also enqueues
// on cluster-scoped ReadDesire and ApplyDesire events from the union
// kube-applier informer surface. The kube-applier writes per-desire status
// (including a Degraded condition) back onto these desires, so a desire update
// is how this controller learns "the kube-applier reported something new about
// a cluster" — the cluster Degraded aggregator folds cluster-scoped
// ApplyDesire/ReadDesire Degraded status into the cluster's Degraded condition.
// Only cluster-scoped desires are watched (node-pool-nested ones are ignored,
// see below). Delete desires are not wired in because their status does not
// carry cluster-state signal.
func NewClusterWatchingController(
	name string,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	resyncDuration time.Duration,
	syncer ClusterSyncer,
) Controller {

	controller := &clusterWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}
	clusterController := newGenericWatchingController(name, coreapi.ClusterResourceType, controller)

	clusterInformer, clusterLister := informers.Clusters()
	serviceProviderInformer, _ := informers.ServiceProviderClusters()
	controller.clusterLister = clusterLister

	err := clusterController.QueueForInformers(resyncDuration, clusterInformer, serviceProviderInformer)
	if err != nil {
		panic(err) // coding error
	}
	managementClusterContentInformer, _ := informers.ManagementClusterContents()
	// Limit the max depth of ManagementClusterContent to 1 to only consider the cluster-scoped ManagementClusterContents
	err = clusterController.QueueForInformersWithMaxDepth(resyncDuration, 1, managementClusterContentInformer)
	if err != nil {
		panic(err) // coding error
	}

	if kubeApplierInformers != nil {
		// Cluster-scoped ReadDesires/ApplyDesires sit one level below the
		// cluster (.../hcpOpenShiftClusters/<cluster>/{read,apply}Desires/<name>),
		// so a maxDepth of 1 reaches the cluster and stops there.
		// Node-pool-scoped desires live one level deeper and are ignored on
		// purpose — this controller is "cluster-scoped only". ApplyDesire is
		// wired in alongside ReadDesire because the cluster Degraded aggregator
		// now folds cluster-scoped ApplyDesire Degraded status into the cluster
		// condition; Delete desires are still not wired in.
		readDesireInformer, _ := kubeApplierInformers.ReadDesires()
		if err := clusterController.QueueForInformersWithMaxDepth(resyncDuration, 1, readDesireInformer); err != nil {
			panic(err) // coding error
		}
		applyDesireInformer, _ := kubeApplierInformers.ApplyDesires()
		if err := clusterController.QueueForInformersWithMaxDepth(resyncDuration, 1, applyDesireInformer); err != nil {
			panic(err) // coding error
		}
	}

	return clusterController
}

func (c *clusterWatchingController) SyncOnce(ctx context.Context, key HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.InitialController))

	_, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	switch {
	case cosmosstorageutils.IsNotFoundError(err):
		logger.Info("cluster not found, skipping sync")
		return nil
	case err != nil:
		// do nothing, let the controller decide what it wants to do.
	}

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *clusterWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

func (c *clusterWatchingController) MakeKey(resourceID *azcorearm.ResourceID) HCPClusterKey {
	return HCPClusterKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Name,
	}
}
