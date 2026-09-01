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

package clusterprovisioningcontrollers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// ClusterManagedResourceGroupLifecycleControllerName is the name of the controller that ensures that the
	// Cluster's Managed Resource Group is reconciled/deleted.
	ClusterManagedResourceGroupLifecycleControllerName = "ClusterManagedResourceGroupLifecycle"
)

// clusterManagedResourceGroupLifecycleSyncer is a Cluster syncer that ensures that the
// Cluster's Managed Resource Group is provisioned.
type clusterManagedResourceGroupLifecycleSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient            database.ResourcesDBClient
	subscriptionLister           listers.SubscriptionLister
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister

	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterManagedResourceGroupLifecycleSyncer)(nil)

func NewClusterManagedResourceGroupLifecycleController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterManagedResourceGroupLifecycleSyncer{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		subscriptionLister:           subscriptionLister,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterManagedResourceGroupLifecycleControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterManagedResourceGroupLifecycleSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		err := c.syncDeletion(ctx, cluster)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}

	// TODO decide if this check is needed or not here. Within syncCreation we also end up performing the check as of now.
	// if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
	// 	len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
	// 	return nil
	// }

	err = c.syncCreation(ctx, cluster)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *clusterManagedResourceGroupLifecycleSyncer) creationNeedsWork(cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	if cluster.ServiceProviderProperties.ClusterServiceID != nil && len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
		return false
	}

	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource == nil && serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource != nil {
		return false
	}

	return true
}

func (c *clusterManagedResourceGroupLifecycleSyncer) deletionNeedsWork(cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp == nil {
		return false
	}

	// We do not proceed with deletion until CS Cluster deletion has completed. This is because we want to perform resource deletion
	// in an ordered and coordinated way. We delete resources before others. For example, we don't want to delete the managed resource group until
	// the cluster on the management cluster side has been deleted, which has resources in the managed resource group. Or for example, we don't want
	// to delete the managed resource group until the cluster's deny assignments, which are created within the managed resource group have been deleted.
	// CS currently has logic to perform the managed resource group deletion in that ordered way. By gating until we know that CS cluster deletion process
	// has completed we ensure that we perform the managed resource group deletion in the same ordered way. When other deletion tasks are moved to the
	// RP we will need to gate them so they are performed in the same ordered way. This check won't be enough because CS will end up being fully
	// removed.
	if cluster.ServiceProviderProperties.ClusterServiceID != nil && len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
		return false
	}

	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource == nil && serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource == nil {
		return false
	}

	return true
}

func (c *clusterManagedResourceGroupLifecycleSyncer) syncCreation(ctx context.Context, cluster *api.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, cluster.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if !c.creationNeedsWork(cluster, serviceProviderCluster) {
		return nil
	}

	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenantId", cluster.ID.SubscriptionID))
	}
	clusterTenantID := *subscription.Properties.TenantId

	resourceGroupsClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(clusterTenantID, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource groups client: %w", err))
	}

	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)

	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource == nil && serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource == nil {
		pendingMRGResourceID, err := api.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build pending managed resource group resource ID: %w", err))
		}
		serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource = pendingMRGResourceID
		// We reuse the serviceProviderCluster variable to store the replacement. This is done because the Replace call returns the new resource,
		// and we want to have the new ETag if a new Replace call occurs afterwards.
		serviceProviderCluster, err = serviceProviderClusterCRUD.Replace(ctx, serviceProviderCluster, nil)
		if database.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		logger.Info("Initialized PendingManagedResourceGroupResourceID", "pendingManagedResourceGroupResourceID", pendingMRGResourceID)
	}

	err = c.ensureManagedResourceGroup(ctx, cluster, serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource, resourceGroupsClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure managed resource group created: %w", err))
	}

	logger.Info("Ensured managed resource group", "managed_resource_group_resource_id", serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource)
	replacement := serviceProviderCluster.DeepCopy()
	replacement.Status.AzureResources.ManagedResourceGroup.AzureResource = serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource
	replacement.Status.AzureResources.ManagedResourceGroup.PendingAzureResource = nil
	if equality.Semantic.DeepEqual(serviceProviderCluster, replacement) {
		return nil
	}
	_, err = serviceProviderClusterCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	logger.Info("Updated ServiceProviderCluster", "pendingManagedResourceGroupResourceID", replacement.Status.AzureResources.ManagedResourceGroup.PendingAzureResource, "managedResourceGroupResourceID", replacement.Status.AzureResources.ManagedResourceGroup.AzureResource)

	return nil
}

func (c *clusterManagedResourceGroupLifecycleSyncer) syncDeletion(ctx context.Context, cluster *api.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.deletionNeedsWork(cluster, serviceProviderCluster) {
		return nil
	}

	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenantId", cluster.ID.SubscriptionID))
	}
	clusterTenantID := *subscription.Properties.TenantId

	resourceGroupsClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(clusterTenantID, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource groups client: %w", err))
	}

	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)

	// Prefer the successfully provisioned ID. Fall back to Pending for deletes that
	// interrupt creation after Pending was written but before Managed was promoted.
	mrgResourceID := serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource
	if mrgResourceID == nil {
		mrgResourceID = serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource
	}

	err = c.ensureManagedResourceGroupDeleted(ctx, cluster, mrgResourceID, resourceGroupsClient)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure managed resource group deleted: %w", err))
	}

	logger.Info("Ensured managed resource group deleted", "managed_resource_group_resource_id", mrgResourceID)
	replacement := serviceProviderCluster.DeepCopy()
	replacement.Status.AzureResources.ManagedResourceGroup.AzureResource = nil
	replacement.Status.AzureResources.ManagedResourceGroup.PendingAzureResource = nil
	if equality.Semantic.DeepEqual(serviceProviderCluster, replacement) {
		return nil
	}
	_, err = serviceProviderClusterCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	logger.Info("Updated ServiceProviderCluster", "pendingManagedResourceGroupResourceID", replacement.Status.AzureResources.ManagedResourceGroup.PendingAzureResource, "managedResourceGroupResourceID", replacement.Status.AzureResources.ManagedResourceGroup.AzureResource)

	return nil
}

func (c *clusterManagedResourceGroupLifecycleSyncer) ensureManagedResourceGroupDeleted(ctx context.Context, cluster *api.HCPOpenShiftCluster, mrgResourceID *azcorearm.ResourceID, resourceGroupsClient azureclient.ResourceGroupsClient) error {
	logger := utils.LoggerFromContext(ctx)

	logger = logger.WithValues("managed_resource_group_resource_id", mrgResourceID)
	ctx = utils.ContextWithLogger(ctx, logger)

	getResp, err := resourceGroupsClient.Get(ctx, mrgResourceID.Name, nil)
	if err != nil && azureclient.IsResourceGroupNotFoundErr(err) {
		return nil
	}
	if err != nil && azureclient.IsInvalidResourceGroupErr(err) {
		// TODO do we want to keep this additional check? In CS we had to add it because the
		// validations were not robust enough at the time. Now we have better validations but maybe we wamt to keep
		// this for additional extra defense?
		// We cover the potential case where the managed resource group name provided during creation
		// was somehow invalid (if somehow the validations didn't catch an invalid name). In this case the managed
		// resource group is not created and we can proceed skipping deletion.
		logger.Info("failed getting managed resource group because it is invalid. Skipping deletion", "error", err)
		return nil
	}
	if err != nil && !azureclient.IsResourceGroupNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("failed to get managed resource group: %w", err))
	}

	// TODO should we error or just log and consider success
	if getResp.ManagedBy == nil || *getResp.ManagedBy != cluster.ID.String() {
		logger.Info("managed resource group is not managed by the cluster. Skipping deletion", "managed_by", ptr.Deref(getResp.ManagedBy, "<null>"))
		return nil
	}

	// At this point we know that the managed resource group exists.
	logger.Info("sending delete request to Azure")
	poller, err := resourceGroupsClient.BeginDelete(ctx, mrgResourceID.Name, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to begin delete managed resource group: %w", err))
	}
	logger.Info("delete request sent to Azure")

	// TODO: do we want to poll until done or do we want to implement something that allows earlies requeue time?
	logger.Info("polling until deletion ends up in terminal state")
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		// An error is returned here also if the LRO ended up in a terminal state failed or canceled.
		return utils.TrackError(fmt.Errorf("failed to delete managed resource group: %w", err))
	}
	logger.Info("deletion completed successfully")

	return nil
}

// TODO as of now CS has an inflight validation that ensures that the managed resource group must not exist beforehand. If we want to introduce
// this we would need to remove that validation, and we would temporarily be without the check
// TODO we have a documented validation (not fully enforced) where the managed resource group must be unique within a subscription: It must not be reused across clusters,
// including clusters in different regions. CS enforces the no reuse across clusters within the same subscription and region by leveraging its
// database. However, it does not enforce preventing cross-region reuse within the same subscription because the database is regional. What do we want to
// do about this? As a note about resource groups: a resource group is a regional resource, and Azure enforces that no two resource group
// can have the same name if they belong to the same subscription, even across regions. Note that we also have the ManagedBy attribute of the ManagedResourceGroup which
// includes the cluster's full resource id.
func (c *clusterManagedResourceGroupLifecycleSyncer) ensureManagedResourceGroup(ctx context.Context, cluster *api.HCPOpenShiftCluster, mrgResourceID *azcorearm.ResourceID, resourceGroupsClient azureclient.ResourceGroupsClient) error {
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("managed_resource_group_resource_id", mrgResourceID.String())
	ctx = utils.ContextWithLogger(ctx, logger)

	// TODO if it exists, do we consider the work done or do we want to somehow
	// check if existing differs from desired and apply changes? The implications
	// of attempting changes are:
	// * Some attributes in Azure are READ-ONLY an cannot be changed after
	//   creation
	// * If there are changes that we do not have in desired we would "undo" them
	getResp, err := resourceGroupsClient.Get(ctx, mrgResourceID.Name, nil)
	if err != nil && !azureclient.IsResourceGroupNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("failed to get managed resource group: %w", err))
	}

	var existingManagedResourceGroup *armresources.ResourceGroup
	if err == nil {
		existingManagedResourceGroup = &getResp.ResourceGroup
	}

	desiredManagedResourceGroup := c.buildDesiredManagedResourceGroup(cluster, mrgResourceID.Name)

	if existingManagedResourceGroup != nil {
		// TODO do we want to do this?
		err := c.validateExistingManagedResourceGroup(existingManagedResourceGroup, desiredManagedResourceGroup)
		if err != nil {
			return utils.TrackError(fmt.Errorf("existing managed resource group is not valid: %w", err))
		}
	}

	logger.Info("creating or updating managed resource group")
	createOrUpdateResp, err := resourceGroupsClient.CreateOrUpdate(ctx, mrgResourceID.Name, *desiredManagedResourceGroup, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create or update the managed resource group: %w", err))
	}
	logger.Info("managed resource group created or updated")

	existingManagedResourceGroup = &createOrUpdateResp.ResourceGroup
	// We validate again to cover the case where we evaluated that the managed resource group did not exist and when we issued the CreateOrUpdate call
	// it was created externally.
	// TODO do we want to do this?
	err = c.validateExistingManagedResourceGroup(existingManagedResourceGroup, desiredManagedResourceGroup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("existing managed resource group is not valid: %w", err))
	}

	// TODO do we want to do this?
	err = c.validateExistingManagedResourceGroupProvisioningState(existingManagedResourceGroup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("existing managed resource group provisioning state is not valid: %w", err))
	}

	return nil
}

func (c *clusterManagedResourceGroupLifecycleSyncer) validateExistingManagedResourceGroup(existingManagedResourceGroup *armresources.ResourceGroup, desiredManagedResourceGroup *armresources.ResourceGroup) error {
	// Azure does not allow updating the Location attribute of a resource group after it's been created. If that is attempted the API
	// returns an error (HTTP 409 with error code InvalidResourceGroupLocation) and
	// message "Invalid resource group location '<desired_location'. The Resource group already exists in location '<current_location>'.
	// We error in the case there's a pre-existing resource group in the same Cluster's Subscription but in
	// a different Azure Location.
	if existingManagedResourceGroup.Location != desiredManagedResourceGroup.Location {
		return utils.TrackError(fmt.Errorf("existing managed resource group Location attribute differs from desired. Desired: %s, Existing: %s",
			*desiredManagedResourceGroup.Location, *existingManagedResourceGroup.Location,
		))
	}

	// Azure does not allow updating the ManagedBy attribute from unset to set. If that is attempted the API
	// returns an error (HTTP 400 with error code ResourceGroupManagedByMismatch) and
	// message "The managed by property of the resource group cannot be changed from its current value '<current_value>'."
	// We error if the existing resource group is not a managed resource group. This is, if the ManagedBy attribute is not set.
	if existingManagedResourceGroup.ManagedBy == nil {
		return utils.TrackError(fmt.Errorf("existing managed resource group is not a managed resource group (ManagedBy attribute is not set)"))
	}

	if desiredManagedResourceGroup.ManagedBy == nil {
		return utils.TrackError(fmt.Errorf("unexpected desired managed resource group not having ManagedBy attribute set"))
	}

	// Azure does not allow updating the ManagedBy attribute to a different value. If that is attempted the API
	// returns an error (HTTP 400 with error code ResourceGroupManagedByMismatch) and
	// message "The managed by property of the resource group cannot be changed from its current value '<current_value>'."
	// We error if the existing managed resource group is not managed by the cluster
	if *existingManagedResourceGroup.ManagedBy != *desiredManagedResourceGroup.ManagedBy {
		return utils.TrackError(fmt.Errorf("existing managed resource group ManagedBy attribute differs from desired. Desired: %s, Existing: %s",
			*desiredManagedResourceGroup.ManagedBy, *existingManagedResourceGroup.ManagedBy,
		))
	}

	return nil
}

func (c *clusterManagedResourceGroupLifecycleSyncer) validateExistingManagedResourceGroupProvisioningState(existingManagedResourceGroup *armresources.ResourceGroup) error {
	if existingManagedResourceGroup.Properties == nil {
		return utils.TrackError(fmt.Errorf("existing managed resource group Properties attribute is not set"))
	}
	if existingManagedResourceGroup.Properties.ProvisioningState == nil {
		return utils.TrackError(fmt.Errorf("existing managed resource group Properties.ProvisioningState attribute is not set"))
	}

	if *existingManagedResourceGroup.Properties.ProvisioningState != string(armresources.ProvisioningStateSucceeded) {
		return utils.TrackError(fmt.Errorf("existing managed resource group ProvisioningState attribute is not Succeeded"))
	}

	return nil
}

func (c *clusterManagedResourceGroupLifecycleSyncer) buildDesiredManagedResourceGroup(cluster *api.HCPOpenShiftCluster, desiredManagedResourceGroupName string) *armresources.ResourceGroup {
	managedBy := cluster.ID.String()
	return &armresources.ResourceGroup{
		// TODO do we set the Name for clarity too? According to the Go data type it is
		// a READ-ONLY field. The API allows setting it here too in the CreateOrUpdate call
		// The name is passed on the CreateOrUpdate call as another argument, and the value here
		// must match the value passed as the other argument.
		Name:      to.Ptr(desiredManagedResourceGroupName),
		Location:  to.Ptr(cluster.Location),
		ManagedBy: &managedBy,
	}
}
