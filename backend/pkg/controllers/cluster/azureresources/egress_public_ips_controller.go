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

package azureresources

import (
	"context"
	"errors"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const EgressPublicIPsControllerName = "ObserveEgressPublicIPs"

type egressPublicIPsSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

func NewEgressPublicIPsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &egressPublicIPsSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}
	controller := controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, resourcesDBClient, informers, kubeApplierInformers, 5*time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")
	return controller
}

func (c *egressPublicIPsSyncer) NeedsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || !coreapihelpers.UsesLoadBalancerEgress(cluster) {
		return false
	}
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	if err != nil || len(expected) == 0 {
		return true
	}
	for _, resourceID := range expected {
		if serviceProviderCluster.Status.AzureResources.EgressPublicIPs[resourceID.String()] != coreapi.AzureResourceFound {
			return true
		}
	}
	return false
}

func (c *egressPublicIPsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	existing, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.NeedsWork(cluster, existing) {
		return nil
	}
	observations, observationErr := c.observe(ctx, cluster)
	replacement := existing.DeepCopy()
	replacement.Status.AzureResources.EgressPublicIPs = observations
	if controllerutil.NeedsUpdate(existing, replacement) {
		if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(errors.Join(observationErr, err))
		}
	}
	return observationErr
}

func (c *egressPublicIPsSyncer) observe(ctx context.Context, cluster *coreapi.Cluster) (map[string]coreapi.AzureResourceObservation, error) {
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	if err != nil || len(expected) == 0 {
		return nil, err
	}
	observations := make(map[string]coreapi.AzureResourceObservation, len(expected))
	for _, resourceID := range expected {
		observations[resourceID.String()] = coreapi.AzureResourceUnknown
	}
	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if cosmosstorageutils.IsNotFoundError(err) {
		return observations, nil
	}
	if err != nil {
		return observations, utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || *subscription.Properties.TenantId == "" {
		return observations, nil
	}
	client, err := c.azureFPAClientBuilder.PublicIPAddressesClient(*subscription.Properties.TenantId, cluster.ID.SubscriptionID)
	if err != nil {
		return observations, utils.TrackError(err)
	}
	var errs []error
	for _, resourceID := range expected {
		_, err := client.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		switch {
		case err == nil:
			observations[resourceID.String()] = coreapi.AzureResourceFound
		case azureclient.IsAzureCoreResponseErrorHTTP404(err):
			observations[resourceID.String()] = coreapi.AzureResourceNotFound
		default:
			errs = append(errs, utils.TrackError(err))
		}
	}
	return observations, errors.Join(errs...)
}
