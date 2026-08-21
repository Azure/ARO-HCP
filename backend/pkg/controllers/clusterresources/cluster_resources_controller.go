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

package clusterresources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/restmapper"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ClusterResourcesControllerName = "ClusterResourcesController"
)

// clusterResourcesController polls the Cluster Service SDK endpoint for cluster resources information
type clusterResourcesController struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clustersServiceClient        ocm.ClusterServiceClientSpec
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	billingDBClient              billingcosmosstorage.BillingDBClient
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
	passiveClock                 utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*clusterResourcesController)(nil)

func NewClusterResourcesController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	billingDBClient billingcosmosstorage.BillingDBClient,
	activeOperationLister corelisters.ActiveOperationLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clustersServiceClient ocm.ClusterServiceClientSpec,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()

	syncer := &clusterResourcesController{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clustersServiceClient:        clustersServiceClient,
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		billingDBClient:              billingDBClient,
		applyDesireLister:            applyDesireLister,
		passiveClock:                 clock,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterResourcesControllerName,
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute, // Poll every 1 minute
		syncer,
	)
}

// NeedsWork reports whether the controller has work to do for the given cluster.
// It requires the cluster to be placed on a management cluster. Beyond that:
// - Clusters being deleted need ApplyDesire cleanup
// - Clusters with a ClusterServiceID need resource syncing
func (c *clusterResourcesController) NeedsWork(cluster *coreapi.HCPOpenShiftCluster, managementCluster *azcorearm.ResourceID) bool {
	if managementCluster == nil {
		return false
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}

	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return false
	}

	return true
}

// SyncOnce polls the cluster resources endpoint and updates any relevant state.
// When the cluster is being deleted, it cleans up all ApplyDesires it owns
// instead of polling for resources.
func (c *clusterResourcesController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	defer utilruntime.HandleCrash()

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}

	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	managementCluster := spc.Status.ManagementClusterResourceID

	if !c.NeedsWork(cluster, managementCluster) {
		return nil
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return c.deleteAllOwnedApplyDesires(ctx, key, managementCluster)
	}

	clusterServiceID := *cluster.ServiceProviderProperties.ClusterServiceID
	if err := c.fetchAndProcessClusterResources(ctx, key, managementCluster, clusterServiceID); err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster resources: %w", err))
	}

	return nil
}

// deleteAllOwnedApplyDesires removes all ApplyDesire Cosmos documents owned by
// this controller for the given cluster. Called during cluster deletion — the
// underlying kube resources are cleaned up separately by the
// ClusterChildResourcesCleanupController, so we only need to purge the docs.
func (c *clusterResourcesController) deleteAllOwnedApplyDesires(ctx context.Context, key controllerutils.HCPClusterKey, managementCluster *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires for deletion cleanup: %w", err))
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}
	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier CRUD for deletion cleanup: %w", err))
	}

	for _, desire := range existing {
		if desire.Tags[kubeapplierapi.TagKeyControllerName] != ClusterResourcesControllerName {
			continue
		}
		if err := applyDesireCRUD.Delete(ctx, desire.ResourceID.Name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desire.ResourceID.Name, err))
		}
		logger.Info("deleted ApplyDesire document", "desireName", desire.ResourceID.Name)
	}

	return nil
}

// fetchAndProcessClusterResources calls the Cluster Service SDK to get cluster resources information
// and processes the resources.
func (c *clusterResourcesController) fetchAndProcessClusterResources(ctx context.Context,
	key controllerutils.HCPClusterKey, managementCluster *azcorearm.ResourceID, clusterServiceID metadataapi.InternalID) error {
	// Get cluster resources from the Cluster Service SDK
	resources, err := c.clustersServiceClient.GetClusterResources(ctx, clusterServiceID)
	if err != nil {
		return err
	}

	if resources != nil {
		if err := c.processClusterResources(ctx, key, managementCluster, resources); err != nil {
			return utils.TrackError(fmt.Errorf("failed to process cluster resources: %w", err))
		}
	}

	return nil
}

// processClusterResources converts each resource to ApplyDesire documents
func (c *clusterResourcesController) processClusterResources(ctx context.Context, key controllerutils.HCPClusterKey,
	managementCluster *azcorearm.ResourceID, resources *arohcpv1alpha1.ClusterResources) error {
	logger := utils.LoggerFromContext(ctx)

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}
	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier CRUD: %w", err))
	}

	parent := kubeapplierhelpers.DesireParent{}
	tags := map[string]string{kubeapplierapi.TagKeyControllerName: ClusterResourcesControllerName}

	resourceMap := resources.Resources()
	desiredNames := make(map[string]bool, len(resourceMap))
	var errs []error
	for resourceKey, resourceValue := range resourceMap {
		var unstructuredObj unstructured.Unstructured
		if err := json.Unmarshal([]byte(resourceValue), &unstructuredObj); err != nil {
			logger.Error(err, "failed to unmarshal resource, skipping", "resourceKey", resourceKey)
			continue
		}

		gvr, err := restmapper.ResourceFor(unstructuredObj.GroupVersionKind())
		if err != nil {
			logger.Error(err, "failed to resolve resource, skipping", "resourceKey", resourceKey)
			continue
		}

		desireName := desireNameFromGVR(gvr.Resource, unstructuredObj.GetNamespace(), unstructuredObj.GetName())
		desiredNames[desireName] = true

		target := kubeapplierapi.ResourceReference{
			Group:     gvr.Group,
			Version:   gvr.Version,
			Resource:  gvr.Resource,
			Name:      unstructuredObj.GetName(),
			Namespace: unstructuredObj.GetNamespace(),
		}

		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, c.applyDesireLister, parent,
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
			desireName, managementCluster, target, &unstructuredObj, tags); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	if err := c.deleteStaleApplyDesires(ctx, key, managementCluster, desiredNames); err != nil {
		return err
	}

	return nil
}

// applyDesireNameForResource derives a semantic, stable name for an
// ApplyDesire from the Kubernetes resource it carries. The format is
// "{resource}.{namespace}.{name}" for namespaced resources and
// "{resource}.{name}" for cluster-scoped ones, all lowercased.
func applyDesireNameForResource(resource *unstructured.Unstructured) (string, error) {
	gvr, err := restmapper.ResourceFor(resource.GroupVersionKind())
	if err != nil {
		return "", fmt.Errorf("resolve resource for %v: %w", resource.GroupVersionKind(), err)
	}
	return desireNameFromGVR(gvr.Resource, resource.GetNamespace(), resource.GetName()), nil
}

func desireNameFromGVR(resource, namespace, name string) string {
	if namespace != "" {
		return resource + "." + namespace + "." + name
	}
	return resource + "." + name
}

func (c *clusterResourcesController) deleteStaleApplyDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	managementCluster *azcorearm.ResourceID,
	desiredNames map[string]bool,
) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires for stale cleanup: %w", err))
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}
	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier CRUD for stale cleanup: %w", err))
	}

	for _, desire := range existing {
		if desire.Tags[kubeapplierapi.TagKeyControllerName] != ClusterResourcesControllerName {
			continue
		}
		if desiredNames[desire.ResourceID.Name] {
			continue
		}

		removed, err := kubeapplierhelpers.EnsureApplyDesireRemoved(ctx, desire.ResourceID.Name, applyDesireCRUD)
		if err != nil {
			return err
		}
		if removed {
			logger.Info("purged stale ApplyDesire", "desireName", desire.ResourceID.Name)
		} else {
			logger.Info("stale ApplyDesire pending deletion", "desireName", desire.ResourceID.Name)
		}
	}

	return nil
}
