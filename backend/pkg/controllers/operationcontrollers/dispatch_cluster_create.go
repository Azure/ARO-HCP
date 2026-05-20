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

package operationcontrollers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dispatchClusterCreate struct {
	resourcesDBClient     database.ResourcesDBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewDispatchClusterCreateController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchClusterCreate{
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
	}

	return NewGenericOperationController(
		"DispatchClusterCreate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *dispatchClusterCreate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	if len(operation.InternalID.String()) > 0 {
		return false
	}
	return true
}

func (c *dispatchClusterCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil
	}

	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	if cluster.ServiceProviderProperties.ActiveOperationID != "" &&
		cluster.ServiceProviderProperties.ActiveOperationID != operation.OperationID.Name {
		logger.Info("skipping cluster create dispatch: active operation mismatch",
			"cluster_active_operation_id", cluster.ServiceProviderProperties.ActiveOperationID,
			"operation_name", operation.OperationID.Name)
		return nil
	}

	csInternalIDFromCluster := cluster.ServiceProviderProperties.ClusterServiceID
	if csInternalIDFromCluster != nil && len(csInternalIDFromCluster.String()) > 0 {
		// Recovery: cluster document was updated with ClusterServiceID but the operation
		// write failed or lagged. Only patch the operation when it still has no InternalID.
		if len(operation.InternalID.String()) > 0 {
			if strings.EqualFold(operation.InternalID.String(), csInternalIDFromCluster.String()) {
				return nil
			}
			return fmt.Errorf("cluster create dispatch: operation internalId %q does not match cluster clusterServiceID %q",
				operation.InternalID.String(), csInternalIDFromCluster.String())
		}
		operation.InternalID = *csInternalIDFromCluster
		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, cluster.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion == nil {
		logger.Info("DesiredVersion not yet set, waiting for desiredversion controller")
		return nil
	}

	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, operation.ExternalID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || *subscription.Properties.TenantId == "" {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenant id", operation.ExternalID.SubscriptionID))
	}
	tenantID := *subscription.Properties.TenantId

	mrg := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if mrg == "" {
		return utils.TrackError(fmt.Errorf("cluster %s has no managed resource group", cluster.Name))
	}

	existingClusterServiceCluster, err := c.findAROHCPClusterByAzureInfo(ctx,
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
		tenantID,
		mrg,
	)
	if err != nil {
		return utils.TrackError(err)
	}

	var csCluster *arohcpv1alpha1.Cluster
	if existingClusterServiceCluster != nil {
		csCluster = existingClusterServiceCluster
		logger.Info("adopting existing Cluster Service cluster for Azure resource")
	} else {
		csCluster, err = c.createClusterServiceCluster(ctx, cluster, existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create cluster in CS: %w", err))
		}
	}

	csInternalID, err := api.NewInternalID(csCluster.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Storing ClusterServiceID on cluster document", "clusterServiceID", csInternalID.String())
	cluster.ServiceProviderProperties.ClusterServiceID = &csInternalID
	_, err = c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Replace(ctx, cluster, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Storing ClusterServiceID on operation document", "clusterServiceID", csInternalID.String())
	operation.InternalID = csInternalID
	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// findAROHCPClusterByAzureInfo returns the Cluster Service cluster whose Azure
// metadata matches the given subscription, resource group, ARM resource name,
// tenant ID, and managed resource group name (MRG).
// It returns (nil, nil) when no such cluster exists.
// An error is returned if more than one cluster is returned matching the azure metadata, as it should be unique.
func (c *dispatchClusterCreate) findAROHCPClusterByAzureInfo(ctx context.Context, subscriptionID, resourceGroupName, resourceName, tenantID, managedResourceGroupName string) (*arohcpv1alpha1.Cluster, error) {
	// Subscription ID, resource group, and cluster name are lowercased when building the Cluster Service
	// cluster (see withImmutableAttributes in convert.go).
	wantSub := strings.ToLower(subscriptionID)
	wantRG := strings.ToLower(resourceGroupName)
	wantName := strings.ToLower(resourceName)
	// Tenant ID and managed resource group are not lowercased in the OCM CS
	// builder (see withImmutableAttributes in convert.go)), we keep the casing as it is.
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

func (c *dispatchClusterCreate) clustersServiceClusterByAzureInfoSearchString(wantSub, wantRG, wantName, wantTenant, wantMRG string) string {
	return fmt.Sprintf(
		"azure.subscription_id = '%s' and azure.resource_group_name = '%s' and azure.resource_name = '%s' and "+
			"azure.tenant_id = '%s' and azure.managed_resource_group_name = '%s'",
		wantSub, wantRG, wantName, wantTenant, wantMRG,
	)
}

func (c *dispatchClusterCreate) csClustersMatchingClusterByAzureInfo(ctx context.Context, it ocm.ClusterListIterator, wantSub, wantRG, wantName, wantTenant, wantMRG string) ([]*arohcpv1alpha1.Cluster, error) {
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

func (c *dispatchClusterCreate) createClusterServiceCluster(ctx context.Context, cluster *api.HCPOpenShiftCluster, desiredVersion *semver.Version) (*arohcpv1alpha1.Cluster, error) {
	logger := utils.LoggerFromContext(ctx)

	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get subscription: %w", err))
	}

	var tenantID string
	if subscription.Properties != nil && subscription.Properties.TenantId != nil {
		tenantID = *subscription.Properties.TenantId
	}

	// Use the Cincinnati-resolved desired version instead of the
	// customer's minor version so CS gets the exact patch release.
	clusterCopy := *cluster
	clusterCopy.CustomerProperties.Version.ID = desiredVersion.String()

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(
		clusterCopy.ID, tenantID, &clusterCopy, nil, nil,
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}

	logger.Info("Creating cluster in Cluster Service", "version", desiredVersion.String())
	result, err := c.clustersServiceClient.PostCluster(ctx, csClusterBuilder, csAutoscalerBuilder)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("PostCluster failed: %w", err))
	}

	return result, nil
}
