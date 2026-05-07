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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
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

// createNodePoolScopedMaestroReadonlyBundlesSyncer is a controller that creates Maestro readonly bundles for the node pools.
// It is responsible for creating the Maestro readonly bundles and storing a reference to them in Cosmos. It does
// not persist the content of the Maestro bundles themselves. That is the responsibility of the
// readAndPersistMaestroReadonlyBundlesContentSyncer controller.
// As of now we support the creation of a Maestro readonly bundle for the Hypershift's NodePool CRs associated to
// the Cluster.
type createNodePoolScopedMaestroReadonlyBundlesSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient database.ResourcesDBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroSourceEnvironmentIdentifier string

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroAPIMaestroBundleNameGenerator maestro.MaestroAPIMaestroBundleNameGenerator
}

var _ controllerutils.NodePoolSyncer = (*createNodePoolScopedMaestroReadonlyBundlesSyncer)(nil)

func NewCreateNodePoolScopedMaestroReadonlyBundlesController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) controllerutils.Controller {

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:                    resourcesDBClient,
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

func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingNodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil // nodepool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}
	if len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		// TODO remove this once we have the information all in cosmos.
		return nil
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.resourcesDBClient, key.GetResourceID())
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
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
		key.HCPNodePoolName,
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
	maestroClient, err := createMaestroClientFromCSProvisionShard(ctx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, clusterProvisionShard)
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

// syncMaestroBundle ensures the given Maestro bundle exists in Maestro, as well as a reference to it in ServiceProviderNodePool.
// It returns the updated ServiceProviderNodePool (after any Replace calls) so the caller can pass it into the next sync.
// On error, the first return value is always the lastest persisted ServiceProviderNodePool, so the
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

	// If the Maestro API MaestroBundle ID is not set we store the returned Maestro Bundle ID in the corresponding Maestro Bundle reference of the ServiceProviderNodePool in Cosmos.
	if len(existingMaestroBundleRef.MaestroAPIMaestroBundleID) == 0 {
		bundleID := string(resultMaestroBundle.UID)
		existingMaestroBundleRef.MaestroAPIMaestroBundleID = bundleID
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

func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
