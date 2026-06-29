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

package integrationutils

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// SimulateBackendClusterServiceCreate creates a cluster in the Cluster Service mock and
// persists the returned HREF on the cluster Cosmos document. This mirrors backend
// PostCluster + ClusterServiceID persistence without waiting for ControlPlaneDesiredVersion.
func SimulateBackendClusterServiceCreate(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceMock *ClusterServiceMock,
	clusterResourceIDString string,
) error {
	if clusterServiceMock == nil {
		return nil
	}

	clusterResourceID, err := azcorearm.ParseResourceID(clusterResourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}
	if !strings.EqualFold(clusterResourceID.ResourceType.String(), api.ClusterResourceType.String()) {
		return utils.TrackError(fmt.Errorf("resource %s is not a cluster", clusterResourceIDString))
	}

	cluster, err := resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).
		Get(ctx, clusterResourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
		return nil
	}

	subscription, err := resourcesDBClient.Subscriptions().Get(ctx, clusterResourceID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	tenantID := api.TestTenantID
	if subscription.Properties != nil && subscription.Properties.TenantId != nil {
		tenantID = *subscription.Properties.TenantId
	}

	serviceProviderClustersDBClient := resourcesDBClient.ServiceProviderClusters(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	)
	serviceProviderCluster, err := serviceProviderClustersDBClient.Get(ctx, api.ServiceProviderClusterResourceName)
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(cluster.ID, tenantID, cluster, nil, nil, serviceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}

	result, err := clusterServiceMock.MockClusterServiceClient.PostCluster(ctx, csClusterBuilder, csAutoscalerBuilder)
	if err != nil {
		return utils.TrackError(fmt.Errorf("PostCluster failed: %w", err))
	}

	csInternalID, err := api.NewInternalID(result.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	cluster.ServiceProviderProperties.ClusterServiceID = &csInternalID
	_, err = resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).
		Replace(ctx, cluster, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	return utils.TrackError(err)
}

// EnsureParentClusterServiceID simulates backend cluster CS provisioning when a child
// resource create requires the parent cluster to have a ClusterServiceID.
func EnsureParentClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceMock *ClusterServiceMock,
	childResourceIDString string,
) error {
	if clusterServiceMock == nil {
		return nil
	}

	childResourceID, err := azcorearm.ParseResourceID(childResourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch {
	case strings.EqualFold(childResourceID.ResourceType.String(), api.NodePoolResourceType.String()),
		strings.EqualFold(childResourceID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		if childResourceID.Parent == nil {
			return utils.TrackError(fmt.Errorf("resource %s has no parent cluster", childResourceIDString))
		}
		cluster, err := resourcesDBClient.HCPClusters(childResourceID.SubscriptionID, childResourceID.ResourceGroupName).
			Get(ctx, childResourceID.Parent.Name)
		if database.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(err)
		}
		if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
			return nil
		}
		return SimulateBackendClusterServiceCreate(ctx, resourcesDBClient, clusterServiceMock, childResourceID.Parent.String())
	default:
		return nil
	}
}
