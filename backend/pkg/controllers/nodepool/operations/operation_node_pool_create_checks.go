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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
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

	// The Cluster Service resource is created asynchronously. Until its ID is
	// populated there is nothing to query, so report the operation as still
	// provisioning rather than dereferencing a nil ClusterServiceID.
	if nodePool.ServiceProviderProperties.ClusterServiceID == nil || len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "node pool cluster service id is not set"), nil
	}

	logger := utils.LoggerFromContext(ctx)
	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()

	csNodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(csCtx, *nodePool.ServiceProviderProperties.ClusterServiceID)
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

type nodePoolCreateValidationCheck struct {
	clock                         utilsclock.PassiveClock
	nodePoolLister                corelisters.NodePoolLister
	subscriptionLister            corelisters.SubscriptionLister
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*nodePoolCreateValidationCheck)(nil)

func (c *nodePoolCreateValidationCheck) GetSourceName() string {
	return "diskEncryptionSetPermissionsValidation"
}

func (c *nodePoolCreateValidationCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	// Check applicability before consuming any stored condition. The validation controller
	// removes a condition asynchronously when the feature is disabled, so a stale stored
	// failure must not be allowed to fail the operation once the validation no longer applies.
	applies, err := c.diskEncryptionSetValidationApplies(ctx, operation)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !applies {
		return nodePoolValidationOperationState(nil, operation.StartTime, c.clock.Now()), nil
	}

	serviceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServiceProviderNodePool not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	// Only the disk encryption set permission validation gates node pool creation; other node pool validations stay diagnostic (surfaced via RequirementsValid) and must not block creation.
	if condition := apimeta.FindStatusCondition(serviceProviderNodePool.Status.Validations, validationutils.AzureNodePoolDiskEncryptionSetPermissionsValidationName); condition != nil {
		return nodePoolValidationOperationState([]metav1.Condition{*condition}, operation.StartTime, c.clock.Now()), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "disk encryption set permission validation has not reported a result yet"), nil
}

func (c *nodePoolCreateValidationCheck) diskEncryptionSetValidationApplies(ctx context.Context, operation *coreapi.Operation) (bool, error) {
	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return false, fmt.Errorf("failed to get node pool from cache: %w", err)
	}
	subscription, err := c.subscriptionLister.Get(ctx, operation.ExternalID.SubscriptionID)
	if cosmosstorageutils.IsNotFoundError(err) {
		subscription = nil
	} else if err != nil {
		return false, fmt.Errorf("failed to get subscription from cache: %w", err)
	}
	return validationutils.AzureNodePoolDiskEncryptionSetPermissionsValidationApplies(subscription, nodePool), nil
}
