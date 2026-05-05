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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dispatchNodePoolCreate struct {
	cosmosClient          database.DBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewDispatchNodePoolCreateController(
	cosmosClient database.DBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchNodePoolCreate{
		cosmosClient:          cosmosClient,
		clustersServiceClient: clustersServiceClient,
	}

	return NewGenericOperationController(
		"DispatchNodePoolCreate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		cosmosClient,
	)
}

func (c *dispatchNodePoolCreate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return false
	}
	if len(operation.InternalID.String()) > 0 {
		return false
	}
	return true
}

func (c *dispatchNodePoolCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.cosmosClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil
	}

	ext := operation.ExternalID
	nodePool, err := c.cosmosClient.HCPClusters(ext.SubscriptionID, ext.ResourceGroupName).NodePools(ext.Parent.Name).Get(ctx, ext.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	if nodePool.ServiceProviderProperties.ActiveOperationID != "" &&
		nodePool.ServiceProviderProperties.ActiveOperationID != operation.OperationID.Name {
		logger.Info("skipping node pool create dispatch: active operation mismatch",
			"nodepool_active_operation_id", nodePool.ServiceProviderProperties.ActiveOperationID,
			"operation_name", operation.OperationID.Name)
		return nil
	}

	csIDFromNodePool := nodePool.ServiceProviderProperties.ClusterServiceID
	if csIDFromNodePool != nil && len(csIDFromNodePool.String()) > 0 {
		// Recovery: node pool document was updated with Cluster Service ID but the operation
		// write failed or lagged. Only patch the operation when it still has no InternalID.
		if len(operation.InternalID.String()) > 0 {
			if strings.EqualFold(operation.InternalID.String(), csIDFromNodePool.String()) {
				return nil
			}
			return fmt.Errorf("node pool create dispatch: operation internalId %q does not match node pool clusterServiceID %q",
				operation.InternalID.String(), csIDFromNodePool.String())
		}
		operation.InternalID = *csIDFromNodePool.DeepCopy() // DeepCopy() to avoid referencing the original pointer
		_, err = c.cosmosClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}

	cluster, err := c.cosmosClient.HCPClusters(ext.SubscriptionID, ext.ResourceGroupName).Get(ctx, ext.Parent.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return utils.TrackError(fmt.Errorf("cluster %s has no ClusterServiceID", ext.Parent.Name))
	}
	clusterCSID := *cluster.ServiceProviderProperties.ClusterServiceID

	// Adoption GET must target the same href POST would use: {clusterHref}/node_pools/{id} where id is
	// lowercased ARM name (see ocm.BuildCSNodePool). Build that InternalID here so the GET helper only talks CS.
	// TODO is calling clusterCSID.ID() correct or should we call clusterCSID.Path() or clusterCSID.ClusterID()?
	csNodePoolHREF := ocm.GenerateAROHCPNodePoolHREF(clusterCSID.ID(), strings.ToLower(ext.Name))
	nodePoolCSInternalID, err := api.NewInternalID(csNodePoolHREF)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build node pool internal ID for adoption lookup: %w", err))
	}

	existing, err := c.findCSNodePool(ctx, nodePoolCSInternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	if existing == nil {
		csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, false)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info("dispatching POST node pool to Cluster Service", "cs_node_pool_href", csNodePoolHREF, "node_pool_resource_id", nodePool.ID.String())
		_, err = c.clustersServiceClient.PostNodePool(ctx, clusterCSID, csNodePoolBuilder)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	nodePool.ServiceProviderProperties.ClusterServiceID = nodePoolCSInternalID.DeepCopy() // DeepCopy() to avoid referencing the original pointer
	_, err = c.cosmosClient.HCPClusters(ext.SubscriptionID, ext.ResourceGroupName).NodePools(ext.Parent.Name).Replace(ctx, nodePool, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = nodePoolCSInternalID
	_, err = c.cosmosClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// findCSNodePool performs GetNodePool for the given Cluster Service node pool InternalID.
// It returns (nil, nil) when CS responds with 404.
func (c *dispatchNodePoolCreate) findCSNodePool(ctx context.Context, nodePoolInternalID api.InternalID) (*arohcpv1alpha1.NodePool, error) {
	np, err := c.clustersServiceClient.GetNodePool(ctx, nodePoolInternalID)
	if err != nil {
		var ocmErr *ocmerrors.Error
		if errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return np, nil
}
