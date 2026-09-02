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

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

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
const ManagedResourceGroupControllerName = "EnsureManagedResourceGroup"

// managedResourceGroupProvisioningRequeueInterval is how long to wait before re-checking a
// managed resource group that Azure reports is still provisioning (e.g. Creating/Updating).
// The reconcile schedules an explicit requeue after this delay instead of confirming it or
// treating the in-progress state as an error.
const managedResourceGroupProvisioningRequeueInterval = 10 * time.Second

// managedResourceGroupSyncer ensures the cluster's managed resource group (MRG)
// exists in Azure and reflects its state onto
// ServiceProviderCluster.Status.AzureResources.ManagedResourceGroup.
//
// In the non-deletion path this controller is the actor that CREATES the MRG:
// when the resource group does not exist yet it calls CreateOrUpdate, claiming
// ownership via ManagedBy = cluster ID, and then records it as confirmed so that
// DB-consuming code (for example the cluster child-resources cleanup gate) can
// reason about the MRG without reaching into Azure directly.
//
// Deletion is still owned by Cluster Service: the deletion path is observe-only
// (it never calls BeginDelete) and merely mirrors the observed state so the
// cluster deletion gate can decide when it is safe to proceed.
type managedResourceGroupSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder

	// enqueueAfter lets the syncer schedule a delayed re-processing of a cluster key
	// (bypassing the workqueue's rate limiter) when Azure reports the managed resource
	// group is still provisioning. It is wired from the controller in
	// NewManagedResourceGroupController.
	enqueueAfter controllerutils.AfterEnqueuer
}

var _ controllerutils.ClusterSyncer = (*managedResourceGroupSyncer)(nil)

// NewManagedResourceGroupController creates a cluster-watching controller that
// creates the cluster's managed resource group in Azure when it does not exist
// (non-deletion path) and keeps
// ServiceProviderCluster.Status.AzureResources.ManagedResourceGroup in sync with
// its observed state.
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

	controller := controllerutils.NewClusterWatchingController(
		ManagedResourceGroupControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)

	// The syncer schedules its own fast requeue (via EnqueueAfter) when Azure reports the
	// managed resource group is still provisioning, rather than relying on error-based
	// rate-limited requeue. genericWatchingController implements AfterEnqueuer; assert it here
	// so a wiring regression fails loudly at startup.
	if enqueuer, ok := controller.(controllerutils.AfterEnqueuer); ok {
		syncer.enqueueAfter = enqueuer
	} else {
		panic(fmt.Sprintf("%s controller must implement AfterEnqueuer", ManagedResourceGroupControllerName))
	}

	return controller
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
// non-deletion (reconcile) path. The non-deletion path creates the resource group
// when it is missing; deletion remains observe-only (Cluster Service owns it).
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
	return c.reconcileManagedResourceGroup(ctx, key, cluster, existingServiceProviderCluster)
}

// reconcileManagedResourceGroup ensures the managed resource group exists for a
// cluster that is not being deleted and reflects its state onto the
// ServiceProviderCluster.
//
// It first records the resource group as PendingAzureResource and persists that
// intent BEFORE querying Azure, so that a Get failure - or a resource group that
// does not exist yet - still leaves a durable pending marker (keeping the deletion
// gate closed) rather than an empty reference. It then queries Azure and:
//
//   - not found: creates the resource group via CreateOrUpdate, claiming ownership via
//     ManagedBy = this cluster's ID. A create failure returns an error so the sync retries
//     with the pending marker still in place.
//   - other error: returns the error so the sync retries.
//   - exists: proceeds with the resource group returned by Get.
//
// In both the not-found (post-create) and exists cases the resource group passes through the
// same provisioning-state gate before being confirmed (see confirmProvisionedManagedResourceGroup):
// only a Succeeded resource group is recorded as AzureResource (unless it is owned by another
// cluster, which is an error); an in-progress state (e.g. Creating/Updating) schedules a short
// requeue and leaves the pending marker intact; any other state (e.g. Failed/Canceled) is an
// error so the sync retries.
func (c *managedResourceGroupSyncer) reconcileManagedResourceGroup(ctx context.Context, key controllerutils.HCPClusterKey, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster) error {
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
		// The managed resource group does not exist yet. Create it in Azure, claiming
		// ownership via ManagedBy = this cluster's ID. A create failure returns an error so
		// a later pass retries with the pending marker still in place. On success the
		// created resource group goes through the shared provisioning-state gate below
		// before being confirmed.
		created, createErr := c.createManagedResourceGroup(ctx, cluster, managedResourceGroupID.Name, rgClient)
		if createErr != nil {
			return utils.TrackError(fmt.Errorf("failed to create managed resource group %q: %w", managedResourceGroupID.Name, createErr))
		}
		return c.confirmProvisionedManagedResourceGroup(ctx, key, cluster, existingServiceProviderCluster, managedResourceGroupID, created)
	case getErr != nil:
		return utils.TrackError(fmt.Errorf("failed to get managed resource group %q: %w", managedResourceGroupID.Name, getErr))
	default:
		// The managed resource group already exists. getResponse is a value type; its
		// fields are only meaningful here, where the Get succeeded. Run it through the same
		// provisioning-state gate before confirming.
		return c.confirmProvisionedManagedResourceGroup(ctx, key, cluster, existingServiceProviderCluster, managedResourceGroupID, getResponse.ResourceGroup)
	}
}

// createManagedResourceGroup creates (or updates) the managed resource group in Azure with
// the desired Location and ManagedBy = this cluster's ID, and returns the resulting resource
// group. The caller is responsible for gating on its provisioning state before confirming it
// (see confirmProvisionedManagedResourceGroup).
//
// CreateOrUpdate is idempotent: if the resource group was created concurrently with a matching
// ManagedBy, Azure returns it unchanged. A conflicting ManagedBy or Location (for example a
// foreign or pre-existing resource group that appeared between the Get and this call) makes
// Azure return an error, which is surfaced to the caller so the sync retries rather than
// recording someone else's resource group as ours.
func (c *managedResourceGroupSyncer) createManagedResourceGroup(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, managedResourceGroupName string, rgClient azureclient.ResourceGroupsClient) (armresources.ResourceGroup, error) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("creating managed resource group", "managedResourceGroup", managedResourceGroupName)

	response, err := rgClient.CreateOrUpdate(ctx, managedResourceGroupName, buildDesiredManagedResourceGroup(cluster, managedResourceGroupName), nil)
	if err != nil {
		return armresources.ResourceGroup{}, err
	}
	logger.Info("created managed resource group", "managedResourceGroup", managedResourceGroupName)
	return response.ResourceGroup, nil
}

// confirmManagedResourceGroup records a resource group whose ManagedBy has just been
// observed (via Get or CreateOrUpdate) as the confirmed AzureResource. If the resource
// group is owned by another cluster it returns an error and records nothing; otherwise
// it clears the PendingAzureResource marker, sets AzureResource, and persists the
// change (a no-op write when nothing changed).
func (c *managedResourceGroupSyncer) confirmManagedResourceGroup(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster, managedResourceGroupID *azcorearm.ResourceID, managedBy *string) error {
	if ownedByAnotherCluster(managedBy, cluster.ID) {
		return utils.TrackError(fmt.Errorf("managed resource group %q is owned by another cluster (ManagedBy=%q), not %q",
			managedResourceGroupID.Name, managedByValue(managedBy), cluster.ID.String()))
	}
	replacement := existingServiceProviderCluster.DeepCopy()
	reference := &replacement.Status.AzureResources.ManagedResourceGroup
	reference.PendingAzureResource = nil
	reference.AzureResource = managedResourceGroupID
	_, err := c.persistIfChanged(ctx, cluster, existingServiceProviderCluster, replacement)
	return utils.TrackError(err)
}

// confirmProvisionedManagedResourceGroup gates an observed resource group (returned by Get or
// CreateOrUpdate) on its Azure provisioning state and, only when it is Succeeded, confirms it
// as the AzureResource. For an in-progress state it schedules a requeue and returns nil,
// leaving the pending marker in place; for a failed/terminal or unrecognized state it returns
// an error so the sync retries.
func (c *managedResourceGroupSyncer) confirmProvisionedManagedResourceGroup(ctx context.Context, key controllerutils.HCPClusterKey, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster, managedResourceGroupID *azcorearm.ResourceID, resourceGroup armresources.ResourceGroup) error {
	proceed, err := c.provisioningStateGate(ctx, key, managedResourceGroupID, resourceGroup)
	if err != nil || !proceed {
		return err
	}
	return c.confirmManagedResourceGroup(ctx, cluster, existingServiceProviderCluster, managedResourceGroupID, resourceGroup.ManagedBy)
}

// provisioningStateGate classifies a resource group's Azure provisioning state and decides how
// the reconcile should react before the resource group is confirmed:
//
//   - Succeeded: returns proceed=true so the caller confirms it.
//   - in-progress (Accepted/Creating/Updating/Deleting): another actor is mid-flight, so it
//     schedules a requeue after managedResourceGroupProvisioningRequeueInterval and returns
//     proceed=false with a nil error, leaving the pending marker in place.
//   - any other state (Failed/Canceled, an unrecognized state, or no state at all): returns an
//     error so the sync retries via the workqueue's rate limiter.
func (c *managedResourceGroupSyncer) provisioningStateGate(ctx context.Context, key controllerutils.HCPClusterKey, managedResourceGroupID *azcorearm.ResourceID, resourceGroup armresources.ResourceGroup) (bool, error) {
	if resourceGroup.Properties == nil || resourceGroup.Properties.ProvisioningState == nil {
		return false, utils.TrackError(fmt.Errorf("managed resource group %q has no provisioning state", managedResourceGroupID.Name))
	}

	state := *resourceGroup.Properties.ProvisioningState
	switch state {
	case string(armresources.ProvisioningStateSucceeded):
		return true, nil
	case string(armresources.ProvisioningStateAccepted),
		string(armresources.ProvisioningStateCreating),
		string(armresources.ProvisioningStateUpdating),
		string(armresources.ProvisioningStateDeleting):
		// Another actor (for example Cluster Service) is mid-flight. Do not confirm; schedule a
		// fast requeue and leave the pending marker in place so we re-check once provisioning
		// settles rather than waiting for the next full resync.
		logger := utils.LoggerFromContext(ctx)
		logger.Info("managed resource group is still provisioning; scheduling requeue",
			"managedResourceGroup", managedResourceGroupID.Name,
			"provisioningState", state,
			"requeueAfter", managedResourceGroupProvisioningRequeueInterval)
		if c.enqueueAfter != nil {
			c.enqueueAfter.EnqueueAfter(key, managedResourceGroupProvisioningRequeueInterval)
		}
		return false, nil
	default:
		// Failed, Canceled, or any unrecognized/terminal state: surface an error so the sync
		// retries rather than confirming a resource group that is not healthy.
		return false, utils.TrackError(fmt.Errorf("managed resource group %q is in provisioning state %q, want %q",
			managedResourceGroupID.Name, state, string(armresources.ProvisioningStateSucceeded)))
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

// buildDesiredManagedResourceGroup builds the Azure resource group this controller
// wants to create for the cluster: the Location is taken from the cluster and
// ManagedBy is set to the cluster's resource ID so the resource group is claimed as
// ours (and recognized as such by ownedByAnotherCluster on later observations). No
// tags are set.
func buildDesiredManagedResourceGroup(cluster *coreapi.HCPOpenShiftCluster, managedResourceGroupName string) armresources.ResourceGroup {
	return armresources.ResourceGroup{
		// Name is read-only per the SDK type, but the API accepts it on CreateOrUpdate
		// as long as it matches the name argument; set it for clarity.
		Name:      ptr.To(managedResourceGroupName),
		Location:  ptr.To(cluster.Location),
		ManagedBy: ptr.To(cluster.ID.String()),
	}
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
