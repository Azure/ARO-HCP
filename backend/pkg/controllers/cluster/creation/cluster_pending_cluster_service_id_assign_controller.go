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

package creation

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterPendingClusterServiceIDAssignSyncer struct {
	envIdentifier     string
	clusterLister     corelisters.ClusterLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterPendingClusterServiceIDAssignSyncer)(nil)

const ClusterPendingClusterServiceIDAssignControllerName = "ClusterPendingClusterServiceIDAssign"

func NewClusterPendingClusterServiceIDAssignController(resourcesDBClient corecosmosstorage.ResourcesDBClient, backendInformers coreinformers.BackendInformers, envIdentifier string) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	syncer := &clusterPendingClusterServiceIDAssignSyncer{
		envIdentifier:     envIdentifier,
		clusterLister:     clusterLister,
		resourcesDBClient: resourcesDBClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		ClusterPendingClusterServiceIDAssignControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)

	return controller
}

func (c *clusterPendingClusterServiceIDAssignSyncer) needsWork(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.PendingClusterServiceID == nil &&
		(cluster.ServiceProviderProperties.ClusterServiceID == nil ||
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *clusterPendingClusterServiceIDAssignSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	cluster, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if !c.needsWork(cluster) {
		return c.syncNamespaces(ctx, key, cluster)
	}

	uid := ocm.NewCSClusterUID()
	pendingID, err := metadataapi.NewInternalID(fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/%s", uid))
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create PendingClusterServiceID: %w", err))
	}

	logger.Info("Assigning PendingClusterServiceID", "pendingClusterServiceID", pendingID.String())
	replacement := cluster.DeepCopy()
	replacement.ServiceProviderProperties.PendingClusterServiceID = &pendingID
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	return c.syncNamespaces(ctx, key, replacement)
}

func (c *clusterPendingClusterServiceIDAssignSyncer) syncNamespacesNeedsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	return len(serviceProviderCluster.Status.HostedClusterNamespace) == 0 || len(serviceProviderCluster.Status.ControlPlaneNamespace) == 0
}

// syncNamespaces also runs for clusters with an assigned ID so a later domain
// prefix update or ServiceProviderCluster creation completes namespace assignment.
func (c *clusterPendingClusterServiceIDAssignSyncer) syncNamespaces(ctx context.Context, key controllerutils.HCPClusterKey, cluster *coreapi.Cluster) error {
	crud := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	existing, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.syncNamespacesNeedsWork(cluster, existing) {
		return nil
	}

	csID := cluster.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.ID()) == 0 {
		csID = cluster.ServiceProviderProperties.PendingClusterServiceID
	}
	if csID == nil || len(csID.ID()) == 0 {
		return nil
	}
	if len(c.envIdentifier) == 0 {
		return utils.TrackError(fmt.Errorf("environment identifier is required for namespace assignment"))
	}

	replacement := existing.DeepCopy()
	replacement.Status.HostedClusterNamespace = controllerutils.HostedClusterNamespace(c.envIdentifier, csID.ID())
	if prefix := cluster.CustomerProperties.DNS.BaseDomainPrefix; len(prefix) != 0 {
		replacement.Status.ControlPlaneNamespace = controllerutils.ControlPlaneNamespace(c.envIdentifier, csID.ID(), prefix)
	}
	if equality.Semantic.DeepEqual(existing, replacement) {
		return nil
	}
	_, err = crud.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	return utils.TrackError(err)
}
