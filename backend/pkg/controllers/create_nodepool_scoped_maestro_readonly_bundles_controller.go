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
	"net/http"
	"time"

	"github.com/google/uuid"
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
	// node pool scoped maestro readonly bundles controller.
	readonlyBundleManagedByK8sLabelValueNodePoolScoped = "create-nodepool-scoped-maestro-readonly-bundles-controller"
)

// createNodePoolScopedMaestroReadonlyBundlesSyncer is a controller that creates Maestro readonly bundles for node pools.
// It is responsible for creating the Maestro readonly bundles and storing a reference to them in
// ServiceProviderNodePool.Status.MaestroReadonlyBundles in Cosmos. It does not persist the content of the Maestro
// bundles themselves. That is the responsibility of the readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer controller.
// As of now we support the creation of a Maestro readonly bundle for the Hypershift NodePool CR associated to
// the node pool.
type createNodePoolScopedMaestroReadonlyBundlesSyncer struct {
	maestroReadonlyBundleHelper

	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*createNodePoolScopedMaestroReadonlyBundlesSyncer)(nil)

func NewCreateNodePoolScopedMaestroReadonlyBundlesController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	maestroSourceEnvironmentIdentifier string,
	maestroClientBuilder maestro.MaestroClientBuilder,
) controllerutils.Controller {

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
			maestroClientBuilder:               maestroClientBuilder,
			uuidV4Generator:                    uuid.NewRandom,
		},
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:          cosmosClient,
		clusterServiceClient:  clusterServiceClient,
		activeOperationLister: activeOperationLister,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"CreateNodePoolScopedMaestroReadonlyBundles",
		cosmosClient,
		informers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingNodePool, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // node pool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}

	// The node pool may exist in Cosmos before it has been registered in Cluster Service.
	// Without a ClusterServiceID we cannot determine the HyperShift NodePool name, so
	// skip until the ID is populated by the creation flow.
	if len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	// The list of Maestro Bundle internal names that are recognized by the controller.
	recognizedMaestroBundles := []api.MaestroBundleInternalName{
		api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
	}

	var maestroBundlesToSync []api.MaestroBundleInternalName
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

	serviceProviderNodePoolsDBClient := c.cosmosClient.ServiceProviderNodePools(
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
		key.HCPNodePoolName,
	)

	// We need the parent cluster to get the provision shard.
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get parent Cluster: %w", err))
	}

	clusterProvisionShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard from Cluster Service: %w", err))
	}

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from Cluster Service: %w", err))
	}
	csClusterDomainPrefix := csCluster.DomainPrefix()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maestroClient, err := c.createMaestroClientFromProvisionShard(ctx, clusterProvisionShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
	}

	var syncErrors []error
	for _, maestroBundleInternalName := range maestroBundlesToSync {
		updatedSPNP, syncErr := c.syncMaestroBundle(
			ctx, maestroBundleInternalName, existingServiceProviderNodePool, existingNodePool, existingCluster, maestroClient,
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
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) syncMaestroBundle(
	ctx context.Context,
	maestroBundleInternalName api.MaestroBundleInternalName,
	existingServiceProviderNodePool *api.ServiceProviderNodePool,
	existingNodePool *api.HCPOpenShiftClusterNodePool,
	existingCluster *api.HCPOpenShiftCluster,
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

	if existingMaestroBundleRef == nil {
		var err error
		existingMaestroBundleRef, err = c.buildInitialMaestroBundleReference(maestroBundleInternalName)
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

	maestroBundleNamespacedName := types.NamespacedName{
		Name:      existingMaestroBundleRef.MaestroAPIMaestroBundleName,
		Namespace: clusterProvisionShard.MaestroConfig().ConsumerName(),
	}

	var desiredMaestroBundle *workv1.ManifestWork
	switch maestroBundleInternalName {
	case api.MaestroBundleInternalNameReadonlyHypershiftNodePool:
		desiredMaestroBundle = c.buildInitialReadonlyMaestroBundleForNodePool(existingNodePool, existingCluster, maestroBundleNamespacedName, csClusterDomainPrefix)
	default:
		return lastPersistedSPNP, utils.TrackError(fmt.Errorf("unrecognized Maestro Bundle internal name: %s", maestroBundleInternalName))
	}

	resultMaestroBundle, err := c.getOrCreateMaestroBundle(ctx, maestroClient, desiredMaestroBundle)
	if err != nil {
		return lastPersistedSPNP, utils.TrackError(fmt.Errorf("failed to get or create Maestro Bundle: %w", err))
	}

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

// buildNodePoolEmptyHypershiftNodePool returns an empty Hypershift NodePool representing the node pool's resource
// on the management cluster. It strictly contains the type information and the object meta information necessary
// to identify the resource in the management cluster.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) buildNodePoolEmptyHypershiftNodePool(csNodePoolID string, csClusterID string, csClusterDomainPrefix string) *hsv1beta1.NodePool {
	nodePoolNamespace := c.getHostedClusterNamespace(c.maestroSourceEnvironmentIdentifier, csClusterID)
	nodePoolName := fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID)

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

// buildInitialReadonlyMaestroBundleForNodePool builds an initial readonly Maestro Bundle for the Hypershift NodePool.
func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) buildInitialReadonlyMaestroBundleForNodePool(
	nodePool *api.HCPOpenShiftClusterNodePool,
	cluster *api.HCPOpenShiftCluster,
	maestroBundleNamespacedName types.NamespacedName,
	csClusterDomainPrefix string,
) *workv1.ManifestWork {
	csNodePoolID := nodePool.ServiceProviderProperties.ClusterServiceID.ID()
	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hypershiftNodePool := c.buildNodePoolEmptyHypershiftNodePool(csNodePoolID, csClusterID, csClusterDomainPrefix)
	maestroBundleResourceIdentifier := workv1.ResourceIdentifier{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Resource:  "nodepools",
		Name:      hypershiftNodePool.Name,
		Namespace: hypershiftNodePool.Namespace,
	}

	maestroBundleObjMeta := metav1.ObjectMeta{
		Name:            maestroBundleNamespacedName.Name,
		Namespace:       maestroBundleNamespacedName.Namespace,
		ResourceVersion: "0",
		Labels: map[string]string{
			readonlyBundleManagedByK8sLabelKey: readonlyBundleManagedByK8sLabelValueNodePoolScoped,
		},
	}

	return c.buildInitialReadonlyMaestroBundle(maestroBundleObjMeta, maestroBundleResourceIdentifier, hypershiftNodePool)
}

func (c *createNodePoolScopedMaestroReadonlyBundlesSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
