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
	"strings"
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
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const EgressPublicIPsControllerName = "ObserveEgressPublicIPs"

type egressPublicIPsSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
	enqueueAfter                 controllerutils.AfterEnqueuer
}

func NewEgressPublicIPsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
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
	controller := controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, resourcesDBClient, informers, kubeApplierInformers, 5*time.Minute, syncer)
	syncer.enqueueAfter = controller.(controllerutils.AfterEnqueuer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")
	return controller
}

func (c *egressPublicIPsSyncer) NeedsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || !coreapihelpers.UsesLoadBalancerEgress(cluster) {
		return false
	}
	if recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]; recheck != nil {
		return !c.clock.Now().Before(recheck.Time)
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
	_, err = c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || !coreapihelpers.UsesLoadBalancerEgress(cluster) {
		return nil
	}
	existing, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.NeedsWork(cluster, existing) {
		if recheck := existing.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]; recheck != nil && c.clock.Now().Before(recheck.Time) && cluster.ServiceProviderProperties.DeletionTimestamp == nil && coreapihelpers.UsesLoadBalancerEgress(cluster) {
			c.scheduleRecheck(key, existing)
			return controllerutils.ErrSyncSkipped
		}
		return nil
	}
	invalidation := existing.DeepCopy()
	for resourceID, observation := range invalidation.Status.AzureResources.EgressPublicIPs {
		if observation == coreapi.AzureResourceNotFound {
			invalidation.Status.AzureResources.EgressPublicIPs[resourceID] = coreapi.AzureResourceUnknown
		}
	}
	if controllerutil.NeedsUpdate(existing, invalidation) {
		existing, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, invalidation, nil)
		if err != nil {
			return utils.TrackError(err)
		}
	}
	observations, observationErr := c.observe(ctx, cluster)
	replacement := existing.DeepCopy()
	replacement.Status.AzureResources.EgressPublicIPs = observations
	delete(replacement.Spec.EarliestRecheckTimesByController, EgressPublicIPsControllerName)
	for _, observation := range observations {
		if observation == coreapi.AzureResourceNotFound || observationErr != nil {
			if replacement.Spec.EarliestRecheckTimesByController == nil {
				replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
			}
			recheck := metav1.NewTime(c.clock.Now().Add(wait.Jitter(time.Minute, 0.5)))
			replacement.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName] = &recheck
			break
		}
	}
	if controllerutil.NeedsUpdate(existing, replacement) {
		if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(errors.Join(observationErr, err))
		}
	}
	c.scheduleRecheck(key, replacement)
	return observationErr
}

func (syncer *egressPublicIPsSyncer) scheduleRecheck(key controllerutils.HCPClusterKey, serviceProviderCluster *coreapi.ServiceProviderCluster) {
	if recheck := serviceProviderCluster.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]; recheck != nil && syncer.enqueueAfter != nil {
		syncer.enqueueAfter.EnqueueAfter(key, recheck.Sub(syncer.clock.Now()))
	}
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
	var invalidRecordErr error
	pager := client.NewListPager(expected[0].ResourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return observations, utils.TrackError(errors.Join(invalidRecordErr, err))
		}
		for _, publicIP := range page.Value {
			if publicIP == nil || publicIP.ID == nil {
				invalidRecordErr = errors.New("public IP enumeration returned a record without an ARM resource ID")
				continue
			}
			resourceID, err := azcorearm.ParseResourceID(*publicIP.ID)
			if err != nil {
				invalidRecordErr = errors.New("public IP enumeration returned an invalid ARM resource ID")
				continue
			}
			for _, expectedID := range expected {
				if strings.EqualFold(resourceID.String(), expectedID.String()) {
					observations[expectedID.String()] = coreapi.AzureResourceFound
					break
				}
			}
		}
	}
	if invalidRecordErr != nil {
		return observations, utils.TrackError(invalidRecordErr)
	}
	for resourceID, observation := range observations {
		if observation != coreapi.AzureResourceFound {
			observations[resourceID] = coreapi.AzureResourceNotFound
		}
	}
	return observations, nil
}
