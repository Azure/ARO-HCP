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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterBaseDomainPrefixSyncer synchronizes CustomerProperties.DNS.BaseDomainPrefix from
// Cluster Service to Cosmos DB when the field is unset.
type clusterBaseDomainPrefixSyncer struct {
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*clusterBaseDomainPrefixSyncer)(nil)

// NewClusterBaseDomainPrefixSyncController creates a controller that synchronizes
// CustomerProperties.DNS.BaseDomainPrefix from Cluster Service to Cosmos DB.
func NewClusterBaseDomainPrefixSyncController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()

	syncer := &clusterBaseDomainPrefixSyncer{
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterBaseDomainPrefixSync",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *clusterBaseDomainPrefixSyncer) needsWork(existingCluster *api.HCPOpenShiftCluster) bool {
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil ||
		len(existingCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return false
	}

	return len(existingCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0
}

func (c *clusterBaseDomainPrefixSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.needsWork(cachedCluster) {
		return nil
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if !c.needsWork(existingCluster) {
		return nil
	}

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, *existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	replacement := existingCluster.DeepCopy()
	replacement.CustomerProperties.DNS.BaseDomainPrefix = csCluster.DomainPrefix()

	if equality.Semantic.DeepEqual(existingCluster, replacement) {
		return nil
	}

	if _, err := clusterCRUD.Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("synced cluster base domain prefix")
	return nil
}
