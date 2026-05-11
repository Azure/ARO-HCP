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
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// readAndPersistClusterScopedKubeContentSyncer mirrors the kube-applier's
// observation of the cluster's HostedCluster (held in
// ReadDesire.Status.KubeContent) into the cluster-scoped
// ManagementClusterContent document. Downstream consumers
// (operation_cluster_create, control_plane_active_version_controller,
// maestrohelpers) keep reading ManagementClusterContent unchanged; only
// the upstream source has moved from Maestro to ReadDesire.
//
// Replaces readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer.
type readAndPersistClusterScopedKubeContentSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClient  database.KubeApplierDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*readAndPersistClusterScopedKubeContentSyncer)(nil)

func NewReadAndPersistClusterScopedKubeContentController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClient database.KubeApplierDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &readAndPersistClusterScopedKubeContentSyncer{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		activeOperationLister: activeOperationLister,
		resourcesDBClient:     resourcesDBClient,
		kubeApplierDBClient:   kubeApplierDBClient,
		clusterServiceClient:  clusterServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ReadAndPersistClusterScopedKubeContent",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *readAndPersistClusterScopedKubeContentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// CS reference not yet set; retrigger once it is.
		return nil
	}

	// We need the management cluster (= kube-applier container partition
	// key) to locate the ReadDesire. We look it up from CS each sync to
	// avoid a side-channel cache; this matches the cadence the
	// maestro-sourced flow used.
	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, *existingCluster.ServiceProviderProperties.ClusterServiceID)
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
	}
	readDesireCRUD, err := c.kubeApplierDBClient.KubeApplier(managementCluster).ReadDesires(parent)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	managementClusterContentsDBClient := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ManagementClusterContents(key.HCPClusterName)

	return persistManagementClusterContentFromReadDesire(
		ctx,
		existingCluster.ID,
		readDesireNameReadonlyHostedCluster,
		func(ctx context.Context, name string) (*kubeapplier.ReadDesire, error) {
			return readDesireCRUD.Get(ctx, name)
		},
		managementClusterContentsDBClient,
	)
}

func (c *readAndPersistClusterScopedKubeContentSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
