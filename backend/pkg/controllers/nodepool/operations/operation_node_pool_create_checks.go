// Copyright 2025 Microsoft Corporation
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

package operations

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolCreateClusterServiceCheck struct {
	nodePoolLister       corelisters.NodePoolLister
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*nodePoolCreateClusterServiceCheck)(nil)

func (c *nodePoolCreateClusterServiceCheck) GetSourceName() string {
	return "clusterServiceNodePoolStatus"
}

func (c *nodePoolCreateClusterServiceCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	logger := utils.LoggerFromContext(ctx)
	csNodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, *nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := operationbase.ConvertNodePoolStatus(operation, csNodePoolStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", newOperationError)
	state := operationbase.NewOperationState(newOperationStatus, operationbase.NodePoolServiceOperationMessage(csNodePoolStatus, newOperationError))
	if newOperationError != nil {
		state.WithCloudErrorCode(newOperationError.Code)
	}
	return state, nil
}

type nodePoolCreateHypershiftCheck struct {
	nodePoolLister   corelisters.NodePoolLister
	readDesireLister kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*nodePoolCreateHypershiftCheck)(nil)

func (c *nodePoolCreateHypershiftCheck) GetSourceName() string {
	return "hypershiftNodePool"
}

func (c *nodePoolCreateHypershiftCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	hypershiftNodePool, err := kubeapplierhelpers.GetCachedNodePoolForNodePool(
		ctx,
		c.readDesireLister,
		nodePool.ID.SubscriptionID,
		nodePool.ID.ResourceGroupName,
		nodePool.ID.Parent.Name,
		nodePool.ID.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hypershiftNodePool == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "hypershift node pool not cached yet"), nil
	}

	if matches, message := c.hypershiftNodePoolSpecMatchesDesired(nodePool, hypershiftNodePool.Spec); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	if matches, message := c.hypershiftNodePoolStatusMatchesDesired(nodePool, hypershiftNodePool.Status); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}
