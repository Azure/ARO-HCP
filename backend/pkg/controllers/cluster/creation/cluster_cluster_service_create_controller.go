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
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/roleassignments"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
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
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	clustersServiceClient        ocm.ClusterServiceClientSpec
	// denyAssignmentsEnabled mirrors whether the ClusterDenyAssignment controller runs (i.e. a real
	// FPA is available). When false, cluster creation must not wait for deny assignments to be
	// created, because nothing creates them.
	denyAssignmentsEnabled bool
	mockFPAPrincipalID     string
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceCreateSyncer)(nil)

const ClusterClusterServiceCreateControllerName = "ClusterClusterServiceCreate"

func NewClusterClusterServiceCreateController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	managementClusterLister fleetlisters.ManagementClusterLister,
	backendInformers coreinformers.BackendInformers,
	denyAssignmentsEnabled bool,
	mockFPAPrincipalID string,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	syncer := &clusterClusterServiceCreateSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		managementClusterLister:      managementClusterLister,
		clustersServiceClient:        clustersServiceClient,
		denyAssignmentsEnabled:       denyAssignmentsEnabled,
		mockFPAPrincipalID:           mockFPAPrincipalID,
	}

	controller := controllerutils.NewClusterWatchingController(
		ClusterClusterServiceCreateControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")
	controller.AddInterestingResource(fleetapi.ManagementClusterResourceType, fleetapi.ManagementClusterResourceName)

	return controller
}

func (c *clusterClusterServiceCreateSyncer) needsWork(ctx context.Context, cluster *coreapi.Cluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil ||
		cluster.ServiceProviderProperties.PendingClusterServiceID == nil ||
		(cluster.ServiceProviderProperties.ClusterServiceID != nil &&
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0) {
		return false
	}

	// Placement gate: creating the Cluster Service cluster needs the scheduler's
	// chosen management cluster (ServiceProviderCluster.Spec.ManagementClusterResourceID)
	// to pin the Cluster Service provision shard. Reading it from the
	// ServiceProviderCluster informer cache here keeps the Cluster Service
	// ListClusters lookup in SyncOnce from running until placement is resolved; the
	// ServiceProviderCluster update the PlacementController makes then re-triggers
	// this cluster.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if err != nil {
		// A missing ServiceProviderCluster just means placement is not resolved yet;
		// stay quiet and let the PlacementController's write re-trigger us. Any other
		// cache error is unexpected and would otherwise silently skip creation, so log
		// it while still holding the placement gate.
		if !cosmosstorageutils.IsNotFoundError(err) {
			utils.LoggerFromContext(ctx).Error(err, "failed to get ServiceProviderCluster from cache; treating placement as unresolved")
		}
		return false
	}
	return serviceProviderCluster.Spec.ManagementClusterResourceID != nil
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

	if !c.needsWork(ctx, cluster) {
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

	if !c.needsWork(ctx, cluster) {
		return nil
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	ready, err := c.createPreconditionDesiredVersionResolved(ctx, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if !ready {
		return nil
	}

	ready, err = c.createPreconditionDenyAssignmentsCreated(ctx, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if !ready {
		return nil
	}

	if !c.denyAssignmentsEnabled && !roleassignments.MockFPARoleAssignmentReady(existingServiceProviderCluster, c.mockFPAPrincipalID) {
		logger.Info("Mock FPA Owner assignment not yet confirmed, waiting for reconciliation")
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

	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSWriteTimeout)
	defer cancel()

	csCluster, err := ocm.FindClusterByAzureInfo(csCtx, c.clustersServiceClient, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, tenantID, mrg)
	if err != nil {
		return utils.TrackError(err)
	}

	if csCluster == nil {
		// Adoption must remain possible before DNS allocation/backfill. This
		// precondition gates only a new POST, unlike the prerequisites above.
		if !c.createPreconditionDNSAllocated(ctx, cluster) {
			return nil
		}
		csCluster, err = c.createClusterServiceCluster(csCtx, cluster, existingServiceProviderCluster, tenantID)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create cluster in CS: %w", err))
		}
	}

	csInternalID, err := metadataapi.NewInternalID(csCluster.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Storing ClusterServiceID on cluster document", "clusterServiceID", csInternalID.String())
	replacement := cluster.DeepCopy()
	replacement.ServiceProviderProperties.PendingClusterServiceID = nil
	replacement.ServiceProviderProperties.ClusterServiceID = &csInternalID
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	return nil
}

func (c *clusterClusterServiceCreateSyncer) createPreconditionDNSAllocated(ctx context.Context, cluster *coreapi.Cluster) bool {
	if len(cluster.ServiceProviderProperties.DNS.BaseDomain) == 0 || len(cluster.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
		utils.LoggerFromContext(ctx).Info("Waiting for DNS allocation before creating cluster in CS")
		return false
	}
	return true
}

// createPreconditionDesiredVersionResolved reports whether the fleet version
// rollout controllers have written the resolved desired version to the
// ServiceProviderCluster. Returns (false, nil) when this controller should wait
// and retry.
func (c *clusterClusterServiceCreateSyncer) createPreconditionDesiredVersionResolved(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion != nil {
		return true, nil
	}
	logger.Info("DesiredVersion not yet set, waiting for the fleet version rollout controllers")
	return false, nil
}

// createPreconditionDenyAssignmentsCreated reports whether the ClusterDenyAssignment
// controller has finished creating all deny assignments.
// Returns (false, nil) when this controller should wait and retry.
func (c *clusterClusterServiceCreateSyncer) createPreconditionDenyAssignmentsCreated(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if !c.denyAssignmentsEnabled {
		// Deny assignments require a real First Party Application (stage/prod). Where the FPA is not
		// available (dev/int, MI mock), the ClusterDenyAssignment controller is disabled, so there is
		// nothing to wait for and creation must not block on it.
		return true, nil
	}

	denyAssignments := serviceProviderCluster.Status.AzureResources.DenyAssignments
	if len(denyAssignments.PendingAzureResources) == 0 &&
		len(denyAssignments.AzureResources) > 0 &&
		denyAssignments.EarliestRecheckTime != nil {
		return true, nil
	}
	pendingTypes := make([]string, 0, len(denyAssignments.PendingAzureResources))
	for _, denyAssignmentReference := range denyAssignments.PendingAzureResources {
		pendingTypes = append(pendingTypes, denyAssignmentReference.DenyAssignmentType)
	}
	logger.Info("Deny assignments not yet created, waiting for ClusterDenyAssignment controller",
		"pendingDenyAssignmentTypes", pendingTypes)
	return false, nil
}

func (c *clusterClusterServiceCreateSyncer) createClusterServiceCluster(ctx context.Context, cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster, tenantID string) (*arohcpv1alpha1.Cluster, error) {
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
		return nil, utils.TrackError(fmt.Errorf("PostCluster failed: %w", ocm.StripVolatileErrorFields(err)))
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
		return "", utils.TrackError(fmt.Errorf("ServiceProviderCluster has no Spec.ManagementClusterResourceID; placement is not resolved"))
	}
	// A management cluster is a singleton within a stamp, so its resource ID is
	// .../stamps/<stampIdentifier>/managementClusters/default and the lister is
	// keyed by the stamp identifier (the parent segment's name).
	if managementClusterResourceID.Parent == nil {
		return "", utils.TrackError(fmt.Errorf("management cluster resource ID %q has no parent stamp", managementClusterResourceID.String()))
	}
	stampIdentifier := managementClusterResourceID.Parent.Name

	managementCluster, err := c.managementClusterLister.Get(ctx, stampIdentifier)
	if cosmosstorageutils.IsNotFoundError(err) {
		return "", utils.TrackError(fmt.Errorf("management cluster %q not found", managementClusterResourceID.String()))
	}
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get management cluster %q: %w", managementClusterResourceID.String(), err))
	}

	if managementCluster.Status.ClusterServiceProvisionShardID == nil {
		return "", utils.TrackError(fmt.Errorf("management cluster %q has no ClusterServiceProvisionShardID", managementClusterResourceID.String()))
	}

	return managementCluster.Status.ClusterServiceProvisionShardID.ID(), nil
}
