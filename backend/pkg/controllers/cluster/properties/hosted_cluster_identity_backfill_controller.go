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
package properties

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const HostedClusterIdentityBackfillControllerName = "HostedClusterIdentityBackfill"

type hostedClusterIdentityBackfillSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clustersServiceClient        ocm.ClusterServiceClientSpec
	environmentIdentifier        string
}

func NewHostedClusterIdentityBackfillController(resourcesDBClient corecosmosstorage.ResourcesDBClient, clustersServiceClient ocm.ClusterServiceClientSpec, informers coreinformers.BackendInformers, environmentIdentifier string) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, spcLister := informers.ServiceProviderClusters()
	return controllerutils.NewClusterWatchingController(HostedClusterIdentityBackfillControllerName, resourcesDBClient, informers, nil, 5*time.Minute, &hostedClusterIdentityBackfillSyncer{resourcesDBClient, clusterLister, spcLister, clustersServiceClient, shortenClusterServiceEnvironment(environmentIdentifier)})
}

func (c *hostedClusterIdentityBackfillSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}
	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if spc.Status.HostedClusterNamespace != "" && spc.Status.HostedClusterName != "" {
		return nil
	}
	csCluster, err := c.clustersServiceClient.GetCluster(ctx, *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}
	replacement := spc.DeepCopy()
	if replacement.Status.HostedClusterNamespace == "" {
		replacement.Status.HostedClusterNamespace = controllerutils.HostedClusterNamespace(c.environmentIdentifier, csCluster.ID())
	}
	if replacement.Status.HostedClusterName == "" {
		replacement.Status.HostedClusterName = csCluster.DomainPrefix()
	}
	_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func shortenClusterServiceEnvironment(value string) string {
	if value == "integration" {
		return "int"
	}
	return value
}
