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

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"
)

type dispatchNodePoolDelete struct {
	cosmosClient          database.DBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewDispatchNodePoolDeleteController(
	cosmosClient database.DBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchNodePoolDelete{
		cosmosClient:          cosmosClient,
		clustersServiceClient: clustersServiceClient,
	}

	return NewGenericOperationController(
		"DispatchNodePoolDelete",
		syncer,
		10*time.Second,
		activeOperationInformer,
		cosmosClient,
	)
}

func (c *dispatchNodePoolDelete) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
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

func (c *dispatchNodePoolDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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
		logger.Info("skipping node pool delete dispatch: active operation mismatch",
			"nodepool_active_operation_id", nodePool.ServiceProviderProperties.ActiveOperationID,
			"operation_name", operation.OperationID.Name)
		return nil // TODO should this be return error or nil?
	}

	csIDFromNodePool := nodePool.ServiceProviderProperties.ClusterServiceID
	if len(csIDFromNodePool.String()) == 0 {
		return utils.TrackError(fmt.Errorf("node pool %s has no ClusterServiceID", ext.Name)) // TODO should this be return error or nil?
	}

	// If the CS nodepool delete is reexecuted and the nodepool is already in CS uninstalling state, the delete call will be successful.
	// If the CS nodepool's parent cluster is being deleted, the delete call will return an error.
	logger.Info("dispatching DELETE node pool to Cluster Service", "cs_node_pool_href", csIDFromNodePool.String(), "node_pool_resource_id", nodePool.ID.String())
	err = c.clustersServiceClient.DeleteNodePool(ctx, csIDFromNodePool)
	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
		err = nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = csIDFromNodePool
	_, err = c.cosmosClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
