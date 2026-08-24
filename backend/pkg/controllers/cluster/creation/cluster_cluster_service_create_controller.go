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

package creation

import (
	"context"
	"fmt"
	"strings"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterClusterServiceCreateSyncer struct {
	resourcesDBClient       corecosmosstorage.ResourcesDBClient
	clusterLister           corelisters.ClusterLister
	subscriptionLister      corelisters.SubscriptionLister
	managementClusterLister fleetlisters.ManagementClusterLister
	clustersServiceClient   ocm.ClusterServiceClientSpec
	// denyAssignmentsEnabled mirrors whether the ClusterDenyAssignment controller runs (i.e. a real
	// FPA is available). When false, cluster creation must not wait for deny assignments to be
	// created, because nothing creates them.
	denyAssignmentsEnabled bool
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceCreateSyncer)(nil)

func NewClusterClusterServiceCreateController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	managementClusterLister fleetlisters.ManagementClusterLister,
	backendInformers coreinformers.BackendInformers,
	denyAssignmentsEnabled bool,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	syncer := &clusterClusterServiceCreateSyncer{
		resourcesDBClient:       resourcesDBClient,
		clusterLister:           clusterLister,
		subscriptionLister:      subscriptionLister,
		managementClusterLister: managementClusterLister,
		clustersServiceClient:   clustersServiceClient,
		denyAssignmentsEnabled:  denyAssignmentsEnabled,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterClusterServiceCreate",
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterClusterServiceCreateSyncer) needsWork(cluster *coreapi.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.PendingClusterServiceID != nil &&
		(cluster.ServiceProviderProperties.ClusterServiceID == nil ||
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *clusterClusterServiceCreateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Quick cache lookup first to see if work is needed
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(cluster) {
		return nil
	}

	// Confirm against the live document to make sure the cluster hasn't been deleted or modified since we last checked
	cluster, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(cluster) {
		return nil
	}

	existingServiceProviderCluster, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, cluster.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	ready, err := c.createPreconditionDesiredVersionResolved(ctx, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if !ready {
		return nil
	}

	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenantId", key.SubscriptionID))
	}
	tenantID := *subscription.Properties.TenantId
	mrg := cluster.CustomerProperties.Platform.ManagedResourceGroup

	csCluster, err := c.findAROHCPClusterByAzureInfo(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, tenantID, mrg)
	if err != nil {
		return utils.TrackError(err)
	}

	if csCluster == nil {
		// Placement gate: the scheduler records its chosen management cluster on
		// ServiceProviderCluster.Spec.ManagementClusterResourceID, which
		// createClusterServiceCluster needs to pin the Cluster Service provision
		// shard. If placement has not been resolved yet, do not create the CS
		// cluster and do not return an error: erroring would re-enqueue and churn
		// the workqueue. Return nil instead — the ServiceProviderCluster update the
		// PlacementController makes once placement lands re-triggers this cluster.
		if existingServiceProviderCluster.Spec.ManagementClusterResourceID == nil {
			logger.Info("ServiceProviderCluster has no Spec.ManagementClusterResourceID yet; deferring Cluster Service cluster creation until placement is resolved")
			return nil
		}
		csCluster, err = c.createClusterServiceCluster(ctx, cluster, existingServiceProviderCluster, tenantID)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create cluster in CS: %w", err))
		}
	}

	csInternalID, err := metadataapi.NewInternalID(csCluster.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Storing ClusterServiceID on cluster document", "clusterServiceID", csInternalID.String())
	cluster.ServiceProviderProperties.PendingClusterServiceID = nil
	cluster.ServiceProviderProperties.ClusterServiceID = &csInternalID
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, cluster, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	return nil
}

// createPreconditionDesiredVersionResolved reports whether the ControlPlaneDesiredVersion
// controller has written the Cincinnati-resolved desired version to the ServiceProviderCluster.
// Returns (false, nil) when this controller should wait and retry.
func (c *clusterClusterServiceCreateSyncer) createPreconditionDesiredVersionResolved(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion != nil {
		return true, nil
	}
	logger.Info("DesiredVersion not yet set, waiting for ControlPlaneDesiredVersion controller")
	return false, nil
}

// findAROHCPClusterByAzureInfo returns the Cluster Service cluster whose Azure
// metadata matches the given subscription, resource group, ARM resource name,
// tenant ID, and managed resource group name (MRG).
// It returns (nil, nil) when no such cluster exists.
// An error is returned if more than one cluster is returned matching the Azure metadata, as it should be unique.
func (c *clusterClusterServiceCreateSyncer) findAROHCPClusterByAzureInfo(ctx context.Context, subscriptionID, resourceGroupName, resourceName, tenantID, managedResourceGroupName string) (*arohcpv1alpha1.Cluster, error) {
	// Subscription ID, resource group, and cluster name are lowercased when building the Cluster Service
	// cluster (see withImmutableAttributes in convert.go).
	wantSub := strings.ToLower(subscriptionID)
	wantRG := strings.ToLower(resourceGroupName)
	wantName := strings.ToLower(resourceName)
	// Tenant ID and managed resource group are not lowercased in the OCM CS
	// builder (see withImmutableAttributes in convert.go), we keep the casing as it is.
	wantTenant := tenantID
	wantMRG := managedResourceGroupName
	search := c.clustersServiceClusterByAzureInfoSearchString(wantSub, wantRG, wantName, wantTenant, wantMRG)
	matches, err := c.csClustersMatchingClusterByAzureInfo(ctx, c.clustersServiceClient.ListClusters(search), wantSub, wantRG, wantName, wantTenant, wantMRG)
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf(
			"cluster service returned %d clusters for one Azure resource (expected exactly 1): "+
				"subscription_id=%q resource_group=%q resource_name=%q tenant_id=%q managed_resource_group=%q",
			len(matches), wantSub, wantRG, wantName, wantTenant, wantMRG,
		)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return nil, nil
}

func (c *clusterClusterServiceCreateSyncer) clustersServiceClusterByAzureInfoSearchString(wantSub, wantRG, wantName, wantTenant, wantMRG string) string {
	return fmt.Sprintf(
		"azure.subscription_id = '%s' and azure.resource_group_name = '%s' and azure.resource_name = '%s' and "+
			"azure.tenant_id = '%s' and azure.managed_resource_group_name = '%s'",
		wantSub, wantRG, wantName, wantTenant, wantMRG,
	)
}

func (c *clusterClusterServiceCreateSyncer) csClustersMatchingClusterByAzureInfo(ctx context.Context, it ocm.ClusterListIterator, wantSub, wantRG, wantName, wantTenant, wantMRG string) ([]*arohcpv1alpha1.Cluster, error) {
	var res []*arohcpv1alpha1.Cluster
	for csCluster := range it.Items(ctx) {
		az := csCluster.Azure()
		if az == nil {
			continue
		}
		if az.SubscriptionID() != wantSub ||
			az.ResourceGroupName() != wantRG ||
			az.ResourceName() != wantName ||
			az.TenantID() != wantTenant ||
			az.ManagedResourceGroupName() != wantMRG {
			continue
		}
		res = append(res, csCluster)
	}
	if err := it.GetError(); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *clusterClusterServiceCreateSyncer) createClusterServiceCluster(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster, tenantID string) (*arohcpv1alpha1.Cluster, error) {
	logger := utils.LoggerFromContext(ctx)

	provisionShardID, err := c.provisionShardID(ctx, serviceProviderCluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	csClusterBuilder, err := ocm.BuildCSCluster(cluster.ID, tenantID, cluster, nil, nil, serviceProviderCluster)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}
	// Pin the CS provision shard for the scheduler-selected management cluster via
	// the SDK builder method.
	csClusterBuilder.ProvisionShardID(provisionShardID)
	clusterServiceUID := cluster.ServiceProviderProperties.PendingClusterServiceID.ClusterID()

	logger.Info("Creating cluster in Cluster Service", "version", serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion.String())
	result, err := c.clustersServiceClient.PostCluster(ctx, csClusterBuilder)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("PostCluster failed: %w", err))
	}

	if result.ID() != clusterServiceUID {
		return nil, fmt.Errorf("cluster-service did not use our ID: %q versus %q", result.ID(), clusterServiceUID)
	}

	return result, nil
}

// provisionShardID resolves the Cluster Service provision shard ID for the
// management cluster the scheduler pinned on
// ServiceProviderCluster.Spec.ManagementClusterResourceID. The caller sets it on
// the CS cluster via ClusterBuilder.ProvisionShardID so the new CS cluster is
// created on the correct provision shard.
func (c *clusterClusterServiceCreateSyncer) provisionShardID(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) (string, error) {
	managementClusterResourceID := serviceProviderCluster.Spec.ManagementClusterResourceID
	if managementClusterResourceID == nil {
		return "", fmt.Errorf("ServiceProviderCluster has no Spec.ManagementClusterResourceID; placement is not resolved")
	}
	// A management cluster is a singleton within a stamp, so its resource ID is
	// .../stamps/<stampIdentifier>/managementClusters/default and the lister is
	// keyed by the stamp identifier (the parent segment's name).
	if managementClusterResourceID.Parent == nil {
		return "", fmt.Errorf("management cluster resource ID %q has no parent stamp", managementClusterResourceID.String())
	}
	stampIdentifier := managementClusterResourceID.Parent.Name

	managementCluster, err := c.managementClusterLister.Get(ctx, stampIdentifier)
	if cosmosstorageutils.IsNotFoundError(err) {
		return "", fmt.Errorf("management cluster %q not found", managementClusterResourceID.String())
	}
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get management cluster %q: %w", managementClusterResourceID.String(), err))
	}

	if managementCluster.Status.ClusterServiceProvisionShardID == nil {
		return "", fmt.Errorf("management cluster %q has no ClusterServiceProvisionShardID", managementClusterResourceID.String())
	}

	return managementCluster.Status.ClusterServiceProvisionShardID.ID(), nil
}
