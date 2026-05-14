// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// readAndPersistNodePoolScopedKubeContentSyncer mirrors the kube-applier's
// observation of the nodepool's Hypershift NodePool (held in
// ReadDesire.Status.KubeContent) into the nodepool-scoped
// ManagementClusterContent document. Downstream consumers keep reading
// ManagementClusterContent unchanged.
//
// Replaces readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer.
type readAndPersistNodePoolScopedKubeContentSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClient  database.KubeApplierDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*readAndPersistNodePoolScopedKubeContentSyncer)(nil)

func NewReadAndPersistNodePoolScopedKubeContentController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClient database.KubeApplierDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &readAndPersistNodePoolScopedKubeContentSyncer{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		activeOperationLister: activeOperationLister,
		resourcesDBClient:     resourcesDBClient,
		kubeApplierDBClient:   kubeApplierDBClient,
		clusterServiceClient:  clusterServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"ReadAndPersistNodePoolScopedKubeContent",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *readAndPersistNodePoolScopedKubeContentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingNodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}
	if len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}

	csClusterID := existingNodePool.ServiceProviderProperties.ClusterServiceID.ClusterID()
	csClusterHREF := ocm.GenerateAROHCPClusterHREF(csClusterID)
	csClusterInternalID := api.Must(api.NewInternalID(csClusterHREF))

	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, csClusterInternalID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard from Cluster Service: %w", err))
	}
	managementCluster := clusterProvisionShard.MaestroConfig().ConsumerName()
	if len(managementCluster) == 0 {
		return nil
	}

	parent := database.ResourceParent{
		SubscriptionID:    key.SubscriptionID,
		ResourceGroupName: key.ResourceGroupName,
		ClusterName:       key.HCPClusterName,
		NodePoolName:      key.HCPNodePoolName,
	}
	readDesireCRUD, err := c.kubeApplierDBClient.KubeApplier(managementCluster).ReadDesires(parent)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	managementClusterContentsDBClient := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).ManagementClusterContents(key.HCPNodePoolName)

	return persistManagementClusterContentFromReadDesire(
		ctx,
		existingNodePool.ID,
		readDesireNameReadonlyNodePool,
		func(ctx context.Context, name string) (*kubeapplier.ReadDesire, error) {
			return readDesireCRUD.Get(ctx, name)
		},
		managementClusterContentsDBClient,
	)
}

func (c *readAndPersistNodePoolScopedKubeContentSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
