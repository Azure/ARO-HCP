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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagedResourceGroupControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ManagedResourceGroupControllerName = "ObserveManagedResourceGroup"

// managedResourceGroupSyncer OBSERVES the cluster's managed resource group (MRG)
// in Azure and reflects its existence onto
// ServiceProviderCluster.Status.AzureResources.ManagedResourceGroup.
//
// Cluster Service is the actor that creates and deletes the MRG. This controller
// is strictly read-only against Azure: it never calls CreateOrUpdate or
// BeginDelete on a resource group. Its only job is to mirror the observed state
// so that DB-consuming code (for example the cluster child-resources cleanup
// gate) can reason about the MRG without reaching into Azure directly.
type managedResourceGroupSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*managedResourceGroupSyncer)(nil)

// NewManagedResourceGroupController creates a cluster-watching controller that
// keeps ServiceProviderCluster.Status.AzureResources.ManagedResourceGroup in sync
// with the observed existence of the cluster's managed resource group in Azure.
func NewManagedResourceGroupController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            resourcesDBClient,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ManagedResourceGroupControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

// SyncOnce observes the managed resource group and reflects its state onto the
// ServiceProviderCluster. Behavior branches on whether the cluster is being
// deleted:
//
//   - Not deleting: if the MRG is missing it is recorded as PendingAzureResource;
//     once it exists it is recorded as AzureResource (and Pending is cleared).
//   - Deleting: once the MRG is gone both references are cleared; while it still
//     exists the reflected state is left untouched so the deletion gate keeps the
//     ServiceProviderCluster document alive.
//
// This controller never creates or deletes the resource group.
func (c *managedResourceGroupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	managedResourceGroupName := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if managedResourceGroupName == "" {
		// Cluster Service has not recorded a managed resource group name yet;
		// nothing to observe.
		return nil
	}

	managedResourceGroupID, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, managedResourceGroupName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via
		// the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	isDeleting := cluster.ServiceProviderProperties.DeletionTimestamp != nil
	existingReference := existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup

	// Deletion fast-path: nothing left to reflect once both references are cleared.
	if isDeleting && existingReference.AzureResource == nil && existingReference.PendingAzureResource == nil {
		return nil
	}

	rgClient, err := c.resourceGroupsClient(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	_, getErr := rgClient.Get(ctx, managedResourceGroupID.Name, nil)
	resourceGroupMissing := azureclient.IsResourceGroupNotFoundErr(getErr)
	if getErr != nil && !resourceGroupMissing {
		return utils.TrackError(fmt.Errorf("failed to get managed resource group %q: %w", managedResourceGroupName, getErr))
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	managedResourceGroupReference := &replacement.Status.AzureResources.ManagedResourceGroup
	switch {
	case isDeleting:
		if !resourceGroupMissing {
			// Still present while deleting: leave reflected state untouched so the
			// ServiceProviderCluster deletion gate keeps waiting.
			return nil
		}
		managedResourceGroupReference.AzureResource = nil
		managedResourceGroupReference.PendingAzureResource = nil
	case resourceGroupMissing:
		managedResourceGroupReference.PendingAzureResource = managedResourceGroupID
		managedResourceGroupReference.AzureResource = nil
	default:
		managedResourceGroupReference.AzureResource = managedResourceGroupID
		managedResourceGroupReference.PendingAzureResource = nil
	}

	if equality.Semantic.DeepEqual(existingServiceProviderCluster, replacement) {
		return nil
	}

	logger.Info("reflecting managed resource group state onto ServiceProviderCluster",
		"managedResourceGroup", managedResourceGroupName,
		"exists", !resourceGroupMissing,
		"deleting", isDeleting)

	_, err = c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// Another writer updated the document first; we'll be re-enqueued and retry.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// resourceGroupsClient builds an FPA-credentialed Azure ResourceGroups client for
// the given subscription, resolving the tenant ID from the subscription document.
func (c *managedResourceGroupSyncer) resourceGroupsClient(ctx context.Context, subscriptionID string) (azureclient.ResourceGroupsClient, error) {
	subscription, err := c.subscriptionLister.Get(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription %q: %w", subscriptionID, err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return nil, fmt.Errorf("subscription %q has no tenant ID", subscriptionID)
	}

	rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(*subscription.Properties.TenantId, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to build resource groups client: %w", err)
	}
	return rgClient, nil
}
