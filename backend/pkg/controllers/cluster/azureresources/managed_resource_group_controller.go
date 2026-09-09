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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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
	clusterLister                corelisters.ClusterLister
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
	_, clusterLister := informers.Clusters()

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
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

// NeedsWork reports whether SyncOnce has anything to do.
//
//   - While the cluster is being deleted there is work only while a reference is
//     still set: we re-check Azure until the managed resource group is gone, then
//     clear the references (opening the deletion gate) and have nothing more to do.
//   - While the cluster is not being deleted there is work only until the managed
//     resource group is confirmed as AzureResource; it is immutable, so once
//     confirmed there is nothing new to observe.
func (c *managedResourceGroupSyncer) NeedsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	managedResourceGroup := serviceProviderCluster.Status.AzureResources.ManagedResourceGroup
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return managedResourceGroup.PendingAzureResource != nil || managedResourceGroup.AzureResource != nil
	}
	return managedResourceGroup.AzureResource == nil
}

// SyncOnce reads the cluster and ServiceProviderCluster from the informer caches,
// short-circuits via NeedsWork, and then dispatches to the deletion or
// non-deletion (reconcile) path. This controller never creates or deletes the
// resource group.
func (c *managedResourceGroupSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
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

	// Short-circuit on the cheap lister reads before doing anything fallible.
	if !c.NeedsWork(cluster, existingServiceProviderCluster) {
		return nil
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return c.deleteManagedResourceGroup(ctx, cluster, existingServiceProviderCluster)
	}
	return c.reconcileManagedResourceGroup(ctx, cluster, existingServiceProviderCluster)
}

// reconcileManagedResourceGroup observes the managed resource group for a cluster
// that is not being deleted and reflects its state onto the ServiceProviderCluster.
//
// It first records the resource group as PendingAzureResource and persists that
// intent BEFORE querying Azure, so that a Get failure - or a resource group that
// does not exist yet - still leaves a durable pending marker (keeping the deletion
// gate closed) rather than an empty reference. It then queries Azure and:
//
//   - not found: does nothing, leaving the pending marker in place (Cluster Service
//     owns creation; this controller is observe-only).
//   - other error: returns the error so the sync retries.
//   - exists: if the resource group is owned by another cluster (its ManagedBy is
//     set and does not equal this cluster's ID) it returns an error; otherwise it
//     clears the pending marker and records the resource group as AzureResource.
func (c *managedResourceGroupSyncer) reconcileManagedResourceGroup(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster) error {
	// A cluster should always have a managed resource group name recorded on its
	// CustomerProperties. If it is empty, something is wrong upstream; return a hard
	// error so the syncer retries rather than silently skipping.
	managedResourceGroupName := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if len(managedResourceGroupName) == 0 {
		return utils.TrackError(fmt.Errorf("managed resource group name is empty for cluster %q", cluster.ID.String()))
	}

	managedResourceGroupID, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, managedResourceGroupName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
	}

	// Set pending before Get: persist the intent so a subsequent Get failure or a
	// not-yet-created resource group still leaves a durable pending marker.
	if existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup.PendingAzureResource == nil {
		replacement := existingServiceProviderCluster.DeepCopy()
		replacement.Status.AzureResources.ManagedResourceGroup.PendingAzureResource = managedResourceGroupID
		existingServiceProviderCluster, err = c.persistIfChanged(ctx, cluster, existingServiceProviderCluster, replacement)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	rgClient, err := c.resourceGroupsClient(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	getResponse, getErr := rgClient.Get(ctx, managedResourceGroupID.Name, nil)
	switch {
	case isNotFound(getErr):
		// The managed resource group does not exist yet. Cluster Service owns its
		// creation; leave the pending marker in place and wait for a later pass.
		// TODO: create the managed resource group.
		return nil
	case getErr != nil:
		return utils.TrackError(fmt.Errorf("failed to get managed resource group %q: %w", managedResourceGroupID.Name, getErr))
	default:
		// The managed resource group exists. getResponse is a value type; its
		// ManagedBy (*string) is only meaningful here, where the Get succeeded.
		if ownedByAnotherCluster(getResponse.ManagedBy, cluster.ID) {
			return utils.TrackError(fmt.Errorf("managed resource group %q is owned by another cluster (ManagedBy=%q), not %q",
				managedResourceGroupID.Name, managedByValue(getResponse.ManagedBy), cluster.ID.String()))
		}
		replacement := existingServiceProviderCluster.DeepCopy()
		reference := &replacement.Status.AzureResources.ManagedResourceGroup
		reference.PendingAzureResource = nil
		reference.AzureResource = managedResourceGroupID
		_, err = c.persistIfChanged(ctx, cluster, existingServiceProviderCluster, replacement)
		return utils.TrackError(err)
	}
}

// deleteManagedResourceGroup observes the managed resource group while the cluster
// is being deleted and reflects its state so the cluster child-resources cleanup
// gate can decide when it is safe to remove the ServiceProviderCluster document.
//
// NeedsWork guarantees a reference is still set when we reach here, so we derive the
// managed resource group ID from that reference, query Azure and:
//
//   - not found: clear both references so the deletion gate opens.
//   - other error: return the error so the gate stays closed until we can positively
//     determine the resource group state.
//   - exists but owned by another cluster: a foreign / pre-existing resource group that
//     Cluster Service will not delete on our behalf, so clear both references to open the
//     deletion gate rather than blocking cluster deletion forever.
//   - exists and owned by this cluster: do nothing and leave the reference in place so the
//     gate stays closed. Cluster Service owns the resource group's deletion. TODO: begin
//     deletion.
func (c *managedResourceGroupSyncer) deleteManagedResourceGroup(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster) error {
	// A reference is guaranteed set here (see NeedsWork). Prefer the confirmed
	// AzureResource, falling back to the PendingAzureResource marker.
	currentReference := existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup
	managedResourceGroupID := currentReference.AzureResource
	if managedResourceGroupID == nil {
		managedResourceGroupID = currentReference.PendingAzureResource
	}

	rgClient, err := c.resourceGroupsClient(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	getResponse, getErr := rgClient.Get(ctx, managedResourceGroupID.Name, nil)
	switch {
	case isNotFound(getErr):
		// The managed resource group is gone: clear both references so the deletion
		// gate opens and the ServiceProviderCluster document can be removed.
		return utils.TrackError(c.clearManagedResourceGroupReferences(ctx, cluster, existingServiceProviderCluster))
	case getErr != nil:
		return utils.TrackError(getErr)
	case ownedByAnotherCluster(getResponse.ManagedBy, cluster.ID):
		// The managed resource group exists but is owned by another cluster: a
		// foreign / pre-existing resource group that Cluster Service will not delete
		// on our behalf. It is not ours to wait on, so clear both references to open
		// the deletion gate rather than blocking cluster deletion forever.
		return utils.TrackError(c.clearManagedResourceGroupReferences(ctx, cluster, existingServiceProviderCluster))
	default:
		// The managed resource group still exists and is owned by this cluster.
		// Cluster Service owns its deletion; leave the reference in place so the
		// deletion gate stays closed.
		// TODO: begin deletion of the managed resource group.
		return nil
	}
}

// clearManagedResourceGroupReferences clears both the pending and confirmed managed
// resource group references on the ServiceProviderCluster and persists the change,
// opening the cluster deletion gate. persistIfChanged makes this a no-op write when the
// references are already clear.
func (c *managedResourceGroupSyncer) clearManagedResourceGroupReferences(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster) error {
	replacement := existingServiceProviderCluster.DeepCopy()
	reference := &replacement.Status.AzureResources.ManagedResourceGroup
	reference.PendingAzureResource = nil
	reference.AzureResource = nil
	_, err := c.persistIfChanged(ctx, cluster, existingServiceProviderCluster, replacement)
	return err
}

// persistIfChanged replaces the ServiceProviderCluster when replacement differs
// from existing and returns the object to use for any subsequent write (the freshly
// persisted document on success, or existing when nothing changed). A Cosmos
// precondition conflict is treated as success (another writer updated the document
// first; we'll be re-enqueued and retry).
func (c *managedResourceGroupSyncer) persistIfChanged(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existing, replacement *coreapi.ServiceProviderCluster) (*coreapi.ServiceProviderCluster, error) {
	if !controllerutil.NeedsUpdate(existing, replacement) {
		return existing, nil
	}

	logger := utils.LoggerFromContext(ctx)
	managedResourceGroup := replacement.Status.AzureResources.ManagedResourceGroup
	logger.Info("reflecting managed resource group state onto ServiceProviderCluster",
		"azureResource", resourceIDString(managedResourceGroup.AzureResource),
		"pendingAzureResource", resourceIDString(managedResourceGroup.PendingAzureResource))

	updated, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return existing, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	return updated, nil
}

// isNotFound reports whether err indicates the managed resource group does not
// exist in Azure.
func isNotFound(err error) bool {
	return azureclient.IsResourceGroupNotFoundErr(err)
}

// ownedByAnotherCluster reports whether a resource group's ManagedBy value refers
// to a cluster other than clusterID. An empty ManagedBy means the resource group is
// unclaimed and is therefore not owned by another cluster. A ManagedBy that is set
// but does not parse as a resource ID is treated as owned by another cluster: it is
// demonstrably not this cluster, so we fail closed and surface an error.
func ownedByAnotherCluster(managedBy *string, clusterID *azcorearm.ResourceID) bool {
	if managedBy == nil || len(*managedBy) == 0 {
		return false
	}
	managedByID, err := azcorearm.ParseResourceID(*managedBy)
	if err != nil {
		return true
	}
	return !controllerutil.ResourceIDsEqual(managedByID, clusterID)
}

// managedByValue safely dereferences a resource group's ManagedBy for logging.
func managedByValue(managedBy *string) string {
	if managedBy == nil {
		return ""
	}
	return *managedBy
}

// resourceIDString renders an optional resource ID for structured logging without
// panicking on a nil pointer (azcorearm.ResourceID.String has a pointer receiver).
func resourceIDString(id *azcorearm.ResourceID) string {
	if id == nil {
		return ""
	}
	return id.String()
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
