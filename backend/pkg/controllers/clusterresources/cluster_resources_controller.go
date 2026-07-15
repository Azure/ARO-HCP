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
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ClusterResourcesControllerName = "ClusterResourcesController"

// clusterResourcesController polls the Cluster Service SDK endpoint for cluster resources information
type clusterResourcesController struct {
	cooldownChecker       controllerutil.CooldownChecker
	clusterLister         listers.ClusterLister
	clustersServiceClient ocm.ClusterServiceClientSpec
	resourcesDBClient     database.ResourcesDBClient
	kubeApplierDBClients  database.KubeApplierDBClients
	billingDBClient       database.BillingDBClient
	passiveClock          utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*clusterResourcesController)(nil)

func NewClusterResourcesController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	billingDBClient database.BillingDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	clustersServiceClient ocm.ClusterServiceClientSpec,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &clusterResourcesController{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:         clusterLister,
		clustersServiceClient: clustersServiceClient,
		resourcesDBClient:     resourcesDBClient,
		kubeApplierDBClients:  kubeApplierDBClients,
		billingDBClient:       billingDBClient,
		passiveClock:          clock,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterResourcesControllerName,
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute, // Poll every 1 minutes
		syncer,
	)
}

func (c *clusterResourcesController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the controller has work to do for the given cluster.
// It should work on clusters that:
// - Are in a ready state (have a ClusterServiceID)
// - Are not being deleted
func (c *clusterResourcesController) NeedsWork(cluster *api.HCPOpenShiftCluster) bool {
	// Skip clusters being deleted
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	// Only work on clusters that have been created in the cluster service
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return false
	}

	return true
}

// SyncOnce polls the cluster resources endpoint and updates any relevant state.
func (c *clusterResourcesController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	defer utilruntime.HandleCrash()

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(cluster) {
		return nil
	}

	// Poll the cluster resources from the Cluster Service
	clusterServiceID := *cluster.ServiceProviderProperties.ClusterServiceID
	logger = logger.WithValues("clusterServiceID", clusterServiceID)
	ctx = utils.ContextWithLogger(ctx, logger)

	err = c.fetchAndProcessClusterResources(ctx, key, clusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to poll cluster resources: %w", err))
	}

	return nil
}

// pollClusterResources calls the Cluster Service SDK to get cluster resources information
// and processes the resources.
func (c *clusterResourcesController) fetchAndProcessClusterResources(ctx context.Context,
	key controllerutils.HCPClusterKey, clusterServiceID api.InternalID) error {
	// Get cluster resources from the Cluster Service SDK
	resources, err := c.clustersServiceClient.GetClusterResources(ctx, clusterServiceID)
	if err != nil {
		return err
	}

	if resources != nil {
		if err := c.processClusterResources(ctx, key, resources); err != nil {
			return utils.TrackError(fmt.Errorf("failed to process cluster resources: %w", err))
		}
	}

	return nil
}

// processClusterResources converts each resource to ApplyDesire documents
func (c *clusterResourcesController) processClusterResources(ctx context.Context, key controllerutils.HCPClusterKey,
	resources *arohcpv1alpha1.ClusterResources) error {
	logger := utils.LoggerFromContext(ctx)
	// Get ServiceProviderCluster to find the management cluster
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// Get the management cluster for this cluster
	managementCluster := spc.Status.ManagementClusterResourceID
	resourceMap := resources.Resources()
	for resourceKey, resourceValue := range resourceMap {
		// Marshal the resource to an unstructured object
		var unstructuredObj unstructured.Unstructured
		if err := json.Unmarshal([]byte(resourceValue), &unstructuredObj); err != nil {
			logger.Error(err, "failed to unmarshal resource, skipping", "resourceKey", resourceKey)
			continue
		}

		// Create ApplyDesire document from the resource
		err := c.createApplyDesireFromResource(ctx, key, managementCluster, &unstructuredObj, resourceKey)
		if err != nil {
			logger.Error(err, "failed to create ApplyDesire for resource", "resourceKey", resourceKey, "kind", unstructuredObj.GetKind())
			return err
		}

		logger.Info("created ApplyDesire for resource",
			"resourceKey", resourceKey,
			"kind", unstructuredObj.GetKind(),
			"name", unstructuredObj.GetName(),
			"namespace", unstructuredObj.GetNamespace())
	}

	return nil
}

// createApplyDesireFromResource creates an ApplyDesire document for a single resource
func (c *clusterResourcesController) createApplyDesireFromResource(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	managementCluster *azcorearm.ResourceID,
	resource *unstructured.Unstructured,
	resourceKey string,
) error {

	// Create the resource ID for the ApplyDesire using the cluster key
	resourceIDString := kubeapplier.ToClusterScopedApplyDesireResourceIDString(
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
		resourceKey,
	)

	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse resource ID: %w", err))
	}

	// Convert resource to JSON for KubeContent
	kubeContentBytes, err := json.Marshal(resource)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal resource to JSON: %w", err))
	}

	// Create the ApplyDesire
	applyDesire := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplier.ResourceReference{
				Group:     resource.GroupVersionKind().Group,
				Version:   resource.GroupVersionKind().Version,
				Resource:  strings.ToLower(resource.GetKind()) + "s",
				Name:      resource.GetName(),
				Namespace: resource.GetNamespace(),
			},
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: kubeContentBytes},
			},
		},
	}

	// Get the appropriate kube-applier database client for the management cluster
	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return utils.TrackError(fmt.Errorf("no kube-applier database client available for management cluster: %s", managementCluster))
	}

	kubeApplierCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier CRUD: %w", err))
	}

	_, err = kubeApplierCRUD.Replace(ctx, applyDesire, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create ApplyDesire: %w", err))
	}

	return nil
}
