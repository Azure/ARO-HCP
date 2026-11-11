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

package egresspublicips

import (
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"

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
	"github.com/Azure/ARO-HCP/internal/utils"
)

const EgressPublicIPsControllerName = "EgressPublicIPs"

const (
	egressPublicIPRecheckInterval     = 6 * time.Hour
	egressPublicIPRecheckJitterFactor = 0.5
)

type egressPublicIPsSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*egressPublicIPsSyncer)(nil)

func NewEgressPublicIPsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &egressPublicIPsSyncer{
		clock:                        clock,
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}
	controller := controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, resourcesDBClient, informers, nil, 5*time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")
	return controller
}

func (c *egressPublicIPsSyncer) needsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || !coreapihelpers.UsesLoadBalancerEgress(cluster) {
		return false
	}
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	if err != nil {
		return true
	}
	if len(expected) == 0 {
		return false
	}
	references := coreapihelpers.EgressPublicIPReferences(expected, serviceProviderCluster.Status.AzureResources.EgressPublicIPs.AzureResources)
	if len(references.PendingAzureResources) != 0 || len(serviceProviderCluster.Status.AzureResources.EgressPublicIPs.PendingAzureResources) != 0 || len(serviceProviderCluster.Status.AzureResources.EgressPublicIPs.AzureResources) != len(expected) {
		return true
	}
	recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]
	return recheck == nil || !c.clock.Now().Before(recheck.Time)
}

func (c *egressPublicIPsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || !coreapihelpers.UsesLoadBalancerEgress(cluster) {
		return nil
	}
	existing, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(cluster, existing) {
		return nil
	}
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if len(expected) == 0 {
		return nil
	}
	replacement := existing.DeepCopy()
	replacement.Status.AzureResources.EgressPublicIPs = coreapihelpers.EgressPublicIPReferences(expected, existing.Status.AzureResources.EgressPublicIPs.AzureResources)
	if controllerutil.NeedsUpdate(existing, replacement) {
		delete(replacement.Spec.EarliestRecheckTimesByController, EgressPublicIPsControllerName)
		if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subscription %q: %w", cluster.ID.SubscriptionID, err))
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || len(*subscription.Properties.TenantId) == 0 {
		return utils.TrackError(fmt.Errorf("subscription %q has no tenant ID", cluster.ID.SubscriptionID))
	}
	observations, observationErr := c.observe(ctx, cluster, *subscription.Properties.TenantId, existing.Status.AzureResources.EgressPublicIPs)
	if observationErr != nil || observations == nil {
		return utils.TrackError(observationErr)
	}
	replacement = existing.DeepCopy()
	replacement.Status.AzureResources.EgressPublicIPs = *observations
	delete(replacement.Spec.EarliestRecheckTimesByController, EgressPublicIPsControllerName)
	if len(observations.PendingAzureResources) == 0 {
		if replacement.Spec.EarliestRecheckTimesByController == nil {
			replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
		}
		recheck := metav1.NewTime(c.clock.Now().Add(wait.Jitter(egressPublicIPRecheckInterval, egressPublicIPRecheckJitterFactor)))
		replacement.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName] = &recheck
	}
	if controllerutil.NeedsUpdate(existing, replacement) {
		if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(err)
		}
	}
	return nil
}

func (c *egressPublicIPsSyncer) observe(ctx context.Context, cluster *coreapi.Cluster, tenantID string, existing coreapi.AzureMultiReference) (*coreapi.AzureMultiReference, error) {
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(expected) == 0 {
		return nil, nil
	}
	references := coreapihelpers.EgressPublicIPReferences(expected, existing.AzureResources)
	if len(references.PendingAzureResources) == 0 {
		references = coreapihelpers.EgressPublicIPReferences(expected, nil)
	}
	client, err := c.azureFPAClientBuilder.PublicIPAddressesClient(tenantID, cluster.ID.SubscriptionID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	confirmed := slices.Clone(references.AzureResources)
	for _, resourceID := range references.PendingAzureResources {
		_, getErr := client.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		switch {
		case getErr == nil:
			confirmed = append(confirmed, resourceID)
		case azureclient.IsPublicIPAddressNotFoundErr(getErr):
			continue
		default:
			return nil, utils.TrackError(getErr)
		}
	}
	observed := coreapihelpers.EgressPublicIPReferences(expected, confirmed)
	return &observed, nil
}
