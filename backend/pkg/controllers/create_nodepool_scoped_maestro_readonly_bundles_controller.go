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
package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// readonlyBundleManagedByK8sLabelValueNodePoolScoped is the K8s label associated to the readonlyBundleManagedByK8sLabelKey
	// key that indicates that the readonly Maestro bundle is managed by the create
	// nodepool scoped maestro readonly bundles controller.
	readonlyBundleManagedByK8sLabelValueNodePoolScoped = "create-nodepool-scoped-maestro-readonly-bundles-controller"
)

// createNodePoolScopedMaestroReadonlyBundlesSyncer reconciles Maestro readonly bundles scoped to a node pool.
//
// While the node pool is not being deleted, it is responsible for creating the Maestro readonly bundles and storing a reference to them in Cosmos.
// It does not persist the content of the Maestro bundles themselves. That is handled by readAndPersistMaestroReadonlyBundlesContentSyncer.
//
// During node pool deletion, when the "serviceProviderProperties.clusterServiceID" property of the node pool is cleared, it deletes Maestro bundles and
// clears references from the ServiceProviderNodePool document when possible.
//
// TODO the name of this controller is no longer accurate, at some point we should rename it to reflect the fact that now it has
// both create and delete reconciliation paths. When doing that, we should make sure it does not impact functionality and
// references to the old name in Cosmos are cleaned up.
type createNodePoolScopedMaestroReadonlyBundlesSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient database.ResourcesDBClient
	fleetDBClient     database.FleetDBClient

	nodePoolLister                listers.NodePoolLister
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroAPIMaestroBundleNameGenerator maestro.MaestroAPIMaestroBundleNameGenerator
}

var _ controllerutils.NodePoolSyncer = (*createNodePoolScopedMaestroReadonlyBundlesSyncer)(nil)

// NewCreateNodePoolScopedMaestroReadonlyBundlesController returns a node pool watching controller that reconciles Maestro
// readonly bundles scoped to a node pool. This includes both create and delete reconciliation paths.
func NewCreateNodePoolScopedMaestroReadonlyBundlesController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	fleetDBClient database.FleetDBClient,

	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) controllerutils.Controller {

	_, nodePoolLister := informers.NodePools()
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:                    resourcesDBClient,
		fleetDBClient:                        fleetDBClient,
		nodePoolLister:                       nodePoolLister,
		serviceProviderNodePoolLister:        serviceProviderNodePoolLister,
		clusterServiceClient:                 clusterServiceClient,
		activeOperationLister:                activeOperationLister,
		maestroSourceEnvironmentIdentifier:   maestroSourceEnvironmentIdentifier,
		maestroClientBuilder:                 maestroClientBuilder,
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"CreateNodePoolScopedMaestroReadonlyBundles",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)

	return controller
}

// cachedShouldReconcileCreateOrDelete is a cache-only coarse gate used before reading Cosmos. It returns whether create or
// delete reconciliation may be worth attempting. syncCreate and syncDelete can still no-op after the authoritative read.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) cachedShouldReconcileCreateOrDelete(ctx context.Context, cachedNodePool *api.HCPOpenShiftClusterNodePool) (bool, error) {
	if cachedNodePool.ServiceProviderProperties.DeletionTimestamp == nil {
		return c.shouldReconcileCreate(cachedNodePool), nil
	}

	cachedServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, cachedNodePool.ID.SubscriptionID, cachedNodePool.ID.ResourceGroupName, cachedNodePool.ID.Parent.Name, cachedNodePool.ID.Name)
	if database.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from cache: %w", err))
	}
	if c.shouldReconcileDelete(cachedNodePool, cachedServiceProviderNodePool) {
		return true, nil
	}
	return false, nil
}

// shouldReconcileCreate returns whether the create reconciliation path may run: the node pool is not deleting and has a
// ClusterServiceID. It does not check whether bundles still need to be created. syncCreate decides that from
// ServiceProviderNodePool status.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) shouldReconcileCreate(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil &&
		(nodePool.ServiceProviderProperties.ClusterServiceID != nil && len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) > 0)
}

// shouldReconcileDelete returns whether the delete reconciliation path may run: the node pool is deleting, The node pool
// is considered as fully deleted from Cluster's Service perspective (ClusterServiceDeletionTimestamp set, ClusterServiceID cleared), and
// and the ServiceProviderNodePool still lists Maestro readonly bundle references to remove.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) shouldReconcileDelete(nodePool *api.HCPOpenShiftClusterNodePool, serviceProviderNodePool *api.ServiceProviderNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID == nil &&
		len(serviceProviderNodePool.Status.MaestroReadonlyBundles) > 0
}

func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	// First we do a quick check to see if there's some work needed. If not, we return early. If the cache is outdated
	// a next reconcile cycle should deal with it.
	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	cachedWorkNeeded, err := c.cachedShouldReconcileCreateOrDelete(ctx, cachedNodePool)
	if err != nil {
		return err
	}
	if !cachedWorkNeeded {
		return nil
	}

	existingNodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil // nodepool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}

	err = c.syncCreateOrDelete(ctx, existingNodePool)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to sync create or deletion: %w", err))
	}

	return nil
}

// syncCreateOrDelete runs the create or delete path for Maestro readonly bundles on a node pool, depending on its deletion state.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) syncCreateOrDelete(ctx context.Context, existingNodePool *api.HCPOpenShiftClusterNodePool) error {
	if existingNodePool.ServiceProviderProperties.DeletionTimestamp == nil {
		return c.syncCreate(ctx, existingNodePool)
	}

	spnpCRUD := c.resourcesDBClient.ServiceProviderNodePools(existingNodePool.ID.SubscriptionID, existingNodePool.ID.ResourceGroupName, existingNodePool.ID.Parent.Name, existingNodePool.Name)
	spnp, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}
	if !c.shouldReconcileDelete(existingNodePool, spnp) {
		return nil
	}
	return c.syncDelete(ctx, existingNodePool)
}

// syncCreate ensures that each recognized readonly bundle internal name is created in Maestro and a reference to it is stored in Cosmos.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) syncCreate(ctx context.Context, existingNodePool *api.HCPOpenShiftClusterNodePool) error {
	if !c.shouldReconcileCreate(existingNodePool) {
		return nil
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.resourcesDBClient, existingNodePool.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	// The list of Maestro Bundle internal names that are recognized by the controller.
	// Any Maestro Bundle internal name that is not in this list will not be synced by the
	// controller and reported as an error.
	recognizedMaestroBundles := []api.MaestroBundleInternalName{
		api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
	}

	var maestroBundlesToSync []api.MaestroBundleInternalName
	// We first check if there's any recognized Maestro Bundle reference that needs to be synced.
	for _, maestroBundleInternalName := range recognizedMaestroBundles {
		currentMaestroBundleReference, err := existingServiceProviderNodePool.Status.MaestroReadonlyBundles.Get(maestroBundleInternalName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
		}

		if currentMaestroBundleReference == nil {
			maestroBundlesToSync = append(maestroBundlesToSync, maestroBundleInternalName)
			continue
		}
		if len(currentMaestroBundleReference.MaestroAPIMaestroBundleName) == 0 {
			maestroBundlesToSync = append(maestroBundlesToSync, maestroBundleInternalName)
			continue
		}
		if len(currentMaestroBundleReference.MaestroAPIMaestroBundleID) == 0 {
			maestroBundlesToSync = append(maestroBundlesToSync, maestroBundleInternalName)
			continue
		}
	}
	if len(maestroBundlesToSync) == 0 {
		return nil
	}

	serviceProviderNodePoolsDBClient := c.resourcesDBClient.ServiceProviderNodePools(
		existingNodePool.ID.SubscriptionID,
		existingNodePool.ID.ResourceGroupName,
		existingNodePool.ID.Parent.Name,
		existingNodePool.Name,
	)

	// We get the provision shard (management cluster) the CS cluster is allocated to.
	// As of now in CS the shard allocation occurs synchronously during aro-hcp cluster creation call in CS API so
	// we are guaranteed to have a shard allocated for the cluster. If this changes in the future
	// we would need to change the logic in controllers to check that the retrieved cluster has a
	// shard allocated.

	csClusterID := existingNodePool.ServiceProviderProperties.ClusterServiceID.ClusterID()
	csClusterHREF := ocm.GenerateAROHCPClusterHREF(csClusterID)
	csClusterInternalID := api.Must(api.NewInternalID(csClusterHREF))
	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, csClusterInternalID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard from Cluster Service: %w", err))
	}

	// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
	// This is important to avoid leaking resources when the sync is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maestroClient, err := CreateMaestroClientFromCSProvisionShard(ctx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, csClusterInternalID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from Cluster Service: %w", err))
	}
	csClusterDomainPrefix := csCluster.DomainPrefix()

	// We sync the Maestro Bundles that need to be synced.
	// We pass the latest existingServiceProviderNodePool into each iteration and use the returned
	// updated SPNP for the next, so that multiple bundles see persisted updates from previous iterations.
	// We always apply updatedSPNP (even on error) so in-memory state stays in sync with Cosmos
	// when syncMaestroBundle persisted a partial change before failing.
	var syncErrors []error
	for _, maestroBundleInternalName := range maestroBundlesToSync {
		updatedSPNP, syncErr := c.syncMaestroBundle(
			ctx, maestroBundleInternalName, existingServiceProviderNodePool, existingNodePool, maestroClient,
			serviceProviderNodePoolsDBClient, clusterProvisionShard, csClusterDomainPrefix,
		)
		existingServiceProviderNodePool = updatedSPNP
		if syncErr != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to sync Maestro Bundle %q: %w", maestroBundleInternalName, syncErr)))
		}
	}

	return utils.TrackError(errors.Join(syncErrors...))
}

// syncDelete ensures, when possible, that the Maestro readonly bundles referenced by the node pool's ServiceProviderNodePool are deleted in Maestro
// and from Cosmos. It runs after the node pool's serviceProviderProperties.clusterServiceID attribute has been cleared and
// removes each bundle from the Maestro API, then clears the corresponding reference from the ServiceProviderNodePool in Cosmos.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) syncDelete(ctx context.Context, existingNodePool *api.HCPOpenShiftClusterNodePool) error {
	logger := utils.LoggerFromContext(ctx)

	spnpCRUD := c.resourcesDBClient.ServiceProviderNodePools(existingNodePool.ID.SubscriptionID, existingNodePool.ID.ResourceGroupName, existingNodePool.ID.Parent.Name, existingNodePool.Name)
	existingSPNP, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}
	if !c.shouldReconcileDelete(existingNodePool, existingSPNP) {
		return nil
	}

	spc, err := c.resourcesDBClient.ServiceProviderClusters(existingNodePool.ID.SubscriptionID, existingNodePool.ID.ResourceGroupName, existingNodePool.ID.Parent.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("ServiceProviderCluster not found"))
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	// If the ServiceProviderCluster has no management cluster resource ID, we clear the
	// maestro bundle references from the ServiceProviderNodePool and return, as there's nothing else we can do
	// at that point.
	if spc.Status.ManagementClusterResourceID == nil {
		logger.Info("ServiceProviderCluster has no management cluster resource ID, deferring maestro bundle cleanup to orphaned bundles controller")
		existingSPNP.Status.MaestroReadonlyBundles = nil
		_, err = spnpCRUD.Replace(ctx, existingSPNP, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to persist ServiceProviderNodePool: %w", err))
		}
		return nil
	}

	managementCluster, err := c.fleetDBClient.Stamps().ManagementClusters(spc.Status.ManagementClusterResourceID.Parent.Name).Get(ctx, spc.Status.ManagementClusterResourceID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get management cluster: %w", err))
	}
	// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
	// This is important to avoid leaking resources when the sync is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maestroClient, err := c.createMaestroClientFromManagementCluster(ctx, managementCluster, c.maestroClientBuilder, c.maestroSourceEnvironmentIdentifier)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	var syncErrors []error
	var bundlesToRemove []api.MaestroBundleInternalName
	for _, ref := range existingSPNP.Status.MaestroReadonlyBundles {
		if len(ref.MaestroAPIMaestroBundleName) == 0 {
			logger.Info("skipping bundle reference with empty Maestro API name", "bundleInternalName", ref.Name)
			bundlesToRemove = append(bundlesToRemove, ref.Name)
			continue
		}

		logger.Info("sending Maestro readonly bundle delete", "bundleInternalName", ref.Name, "maestroAPIMaestroBundleName", ref.MaestroAPIMaestroBundleName)
		err := maestroClient.Delete(ctx, ref.MaestroAPIMaestroBundleName, metav1.DeleteOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete Maestro Bundle %q: %w", ref.MaestroAPIMaestroBundleName, err)))
			continue
		}

		_, err = maestroClient.Get(ctx, ref.MaestroAPIMaestroBundleName, metav1.GetOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to verify deletion of Maestro Bundle %q: %w", ref.MaestroAPIMaestroBundleName, err)))
			continue
		}
		if err == nil {
			logger.Info("Maestro readonly bundle still exists after delete, will retry", "bundleInternalName", ref.Name, "maestroAPIMaestroBundleName", ref.MaestroAPIMaestroBundleName)
			continue
		}

		logger.Info("deleted Maestro readonly bundle", "bundleInternalName", ref.Name, "maestroAPIMaestroBundleName", ref.MaestroAPIMaestroBundleName)
		bundlesToRemove = append(bundlesToRemove, ref.Name)
	}

	if len(bundlesToRemove) > 0 {
		for _, name := range bundlesToRemove {
			if err := existingSPNP.Status.MaestroReadonlyBundles.Remove(name); err != nil {
				syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to remove bundle reference %q: %w", name, err)))
			}
		}
		_, err = spnpCRUD.Replace(ctx, existingSPNP, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to persist ServiceProviderNodePool after deleting bundles: %w", err)))
		}
	}

	return utils.TrackError(errors.Join(syncErrors...))
}

// syncMaestroBundle ensures the given Maestro bundle exists in Maestro, as well as a reference to it in ServiceProviderNodePool.
// It returns the updated ServiceProviderNodePool (after any Replace calls) so the caller can pass it into the next sync.
// On error, the first return value is always the latest persisted ServiceProviderNodePool, so the
// caller can keep in-memory state in sync and subsequent bundle syncs in the same run never see stale data.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) syncMaestroBundle(
	ctx context.Context,
	maestroBundleInternalName api.MaestroBundleInternalName,
	existingServiceProviderNodePool *api.ServiceProviderNodePool,
	existingNodePool *api.HCPOpenShiftClusterNodePool,
	maestroClient maestro.Client,
	serviceProviderNodePoolsDBClient database.ServiceProviderNodePoolCRUD,
	clusterProvisionShard *arohcpv1alpha1.ProvisionShard,
	csClusterDomainPrefix string,
) (*api.ServiceProviderNodePool, error) {
	lastPersistedSPNP := existingServiceProviderNodePool

	existingMaestroBundleRef, err := existingServiceProviderNodePool.Status.MaestroReadonlyBundles.Get(maestroBundleInternalName)
	if err != nil {
		return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
	}
	// If the Maestro Bundle reference does not exist, we create a new Maestro Bundle
	// reference for the Maestro API Maestro Bundle name. When this occurs we also immediately
	// store the content in Cosmos. This ensures that we have the name reserved for it
	// and it makes it resistant to crashes/reboots.
	if existingMaestroBundleRef == nil {
		var err error
		existingMaestroBundleRef, err = buildInitialMaestroBundleReference(maestroBundleInternalName, c.maestroAPIMaestroBundleNameGenerator)
		if err != nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to build initial Maestro Bundle reference: %w", err))
		}
		err = existingServiceProviderNodePool.Status.MaestroReadonlyBundles.Set(existingMaestroBundleRef)
		if err != nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to set internal Maestro Bundle reference: %w", err))
		}
		existingServiceProviderNodePool, err = serviceProviderNodePoolsDBClient.Replace(ctx, existingServiceProviderNodePool, nil)
		if err != nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool in database: %w", err))
		}
		lastPersistedSPNP = existingServiceProviderNodePool
		existingMaestroBundleRef, err = existingServiceProviderNodePool.Status.MaestroReadonlyBundles.Get(maestroBundleInternalName)
		if err != nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
		}
		if existingMaestroBundleRef == nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("maestro Bundle reference %q not found in ServiceProviderNodePool", maestroBundleInternalName))
		}
	}

	// We ensure that the Maestro Bundle exists using the Maestro API
	maestroBundleNamespacedName := types.NamespacedName{
		Name:      existingMaestroBundleRef.MaestroAPIMaestroBundleName,
		Namespace: clusterProvisionShard.MaestroConfig().ConsumerName(),
	}

	var desiredMaestroBundle *workv1.ManifestWork
	switch maestroBundleInternalName {
	case api.MaestroBundleInternalNameReadonlyHypershiftNodePool:
		desiredMaestroBundle = c.buildInitialReadonlyMaestroBundleForNodePool(existingNodePool, csClusterDomainPrefix, maestroBundleNamespacedName)
	default:
		return lastPersistedSPNP, utils.TrackError(fmt.Errorf("unrecognized Maestro Bundle internal name: %s", maestroBundleInternalName))
	}

	resultMaestroBundle, err := maestro.GetOrCreateMaestroBundle(ctx, maestroClient, desiredMaestroBundle)
	if err != nil {
		return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to get or create Maestro Bundle: %w", err))
	}

	// Backfill any missing Maestro Bundle reference attributes individually,
	// then persist at most once if anything changed.
	needsUpdate := false

	if len(existingMaestroBundleRef.MaestroAPIMaestroBundleID) == 0 {
		existingMaestroBundleRef.MaestroAPIMaestroBundleID = string(resultMaestroBundle.UID)
		needsUpdate = true
	}

	if len(existingMaestroBundleRef.ResourceIdentifiers) != len(desiredMaestroBundle.Spec.ManifestConfigs) {
		existingMaestroBundleRef.ResourceIdentifiers = resourceIdentifiersFromManifestWork(desiredMaestroBundle)
		needsUpdate = true
	}

	if needsUpdate {
		err = existingServiceProviderNodePool.Status.MaestroReadonlyBundles.Set(existingMaestroBundleRef)
		if err != nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to set Maestro Bundle reference: %w", err))
		}
		existingServiceProviderNodePool, err = serviceProviderNodePoolsDBClient.Replace(ctx, existingServiceProviderNodePool, nil)
		if err != nil {
			return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool in database: %w", err))
		}
		lastPersistedSPNP = existingServiceProviderNodePool
	}

	return lastPersistedSPNP, nil
}

// buildClusterEmptyNodePool returns an empty node pool representing a Cluster's Hypershift NodePool resource.
// It strictly contains the type information and the object meta information necessary to identify the resource in the management cluster.
// It can be used to provide as the input of a Maestro resource bundle.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) buildClusterEmptyNodePool(csClusterID string, csClusterDomainPrefix string, csNodePoolID string) *hsv1beta1.NodePool {
	// TODO To calculate the NodePool namespace we pass the maestro source ID because it turns out to have the same
	// value as the envName in CS. This is not accurate but it is good enough.
	// I would decouple what is the maestro source ID envname part from the envname. The reason being that they are
	// conceptually different things, they just happen to have the same value for the envName part.
	// I am hesitant to provide a generic "environment name" deployment parameter to backend because people might introduce conditional logic based
	// on the environment name which is fragile. The options I see are:
	// * Provide a deployment parameter to backend that is named something concrete like "k8s-names-calculations-env-name" or similar to indicate
	//   that is something that is used to calculate names/namespaces of some k8s resources.
	// * Expose in the CS API Cluster payload the "CDNamespace" associated to the cluster and start storing it in cosmos. This would allow to fully
	//   decouple from this concept of CDNamespace and we would use the stored value when needed. However, if we want to
	//   create resources in the same namespace as the old ones then we would still need to keep forever the concept of "env name part used to calculate
	//   some k8s resource names/namespaces".
	nodePoolNamespace := c.getNodePoolNamespace(c.maestroSourceEnvironmentIdentifier, csClusterID)
	nodePoolName := c.getNodePoolName(csClusterDomainPrefix, csNodePoolID)

	// We first build the resource (manifest) that we want to put within the Maestro Bundle.
	// The resource is empty and it only has the type information and the object meta
	// information necessary to identify the resource in the management cluster.
	nodePool := &hsv1beta1.NodePool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodePool",
			APIVersion: hsv1beta1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodePoolName,
			Namespace: nodePoolNamespace,
		},
	}

	return nodePool
}

// buildInitialReadonlyMaestroBundleForNodePool builds an initial readonly Maestro Bundle a the Cluster's Hypershift NodePool.
// Used to create the readonly Maestro bundle associated to it.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) buildInitialReadonlyMaestroBundleForNodePool(nodePool *api.HCPOpenShiftClusterNodePool, csClusterDomainPrefix string, maestroBundleNamespacedName types.NamespacedName) *workv1.ManifestWork {
	csClusterID := nodePool.ServiceProviderProperties.ClusterServiceID.ClusterID()
	hypershiftNodePool := c.buildClusterEmptyNodePool(csClusterID, csClusterDomainPrefix, nodePool.ID.Name)
	maestroBundleResourceIdentifier := workv1.ResourceIdentifier{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Resource:  "nodepools",
		Name:      hypershiftNodePool.Name,
		Namespace: hypershiftNodePool.Namespace,
	}

	return buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName, maestroBundleResourceIdentifier, hypershiftNodePool, readonlyBundleManagedByK8sLabelValueNodePoolScoped)
}

// getNodePoolNamespace gets the namespace for the node pool based on the environment name and the cluster service OCM Cluster ID.
// For example, if the Node Pool URL is /api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/nodepools/XXXX then the
// cluster service OCM Cluster ID is 11111111111111111111111111111111.
// The namespace is of the format ocm-<envName>-<csClusterID>. This is how CS calculates Hypershift's NodePool namespace.
// Internally in CS this is the "CDNamespace" attribute associated to the cluster.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) getNodePoolNamespace(envName string, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envName, csClusterID)
}

// getNodePoolName gets the name for the node pool based on the cluster domain prefix and the node pool service OCM Node Pool ID.
// For example, if the Node Pool URL is /api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/nodepools/XXXX and the
// cluster's domain prefix is test-domprefix then the name is test-domprefix-XXXX.
// The name is of the format <csClusterDomainPrefix>-<csNodePoolID>.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) getNodePoolName(csClusterDomainPrefix string, csNodePoolID string) string {
	return fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID)
}

func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// createMaestroClientFromManagementCluster creates a Maestro client for the given management cluster.
// the client is scoped to the Consumer Name associated to the management cluster, and to
// the source ID associated to the management cluster and the environment specified
// in maestroSourceEnvironmentIdentifier, which is a configuration parameter at
// deployment time.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) createMaestroClientFromManagementCluster(ctx context.Context, managementCluster *fleet.ManagementCluster, maestroClientBuilder maestro.MaestroClientBuilder, maestroSourceEnvironmentIdentifier string) (maestro.Client, error) {
	maestroRESTAPIEndpoint := managementCluster.Status.MaestroRESTAPIURL
	maestroGRPCAPIEndpoint := managementCluster.Status.MaestroGRPCTarget
	maestroConsumerName := managementCluster.Status.MaestroConsumerName
	maestroSourceID := maestro.GenerateMaestroSourceID(maestroSourceEnvironmentIdentifier, managementCluster.Status.ClusterServiceProvisionShardID.ID())

	maestroClient, err := maestroClientBuilder.NewClient(ctx, maestroRESTAPIEndpoint, maestroGRPCAPIEndpoint, maestroConsumerName, maestroSourceID)
	return maestroClient, err
}
