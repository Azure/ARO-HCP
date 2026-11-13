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

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
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

type nodePoolCreateDesiredVersionResolutionCheck struct {
	nodePoolLister                corelisters.NodePoolLister
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*nodePoolCreateDesiredVersionResolutionCheck)(nil)

func (c *nodePoolCreateDesiredVersionResolutionCheck) GetSourceName() string {
	return "nodePoolDesiredVersionResolution"
}

func (c *nodePoolCreateDesiredVersionResolutionCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}

	serviceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider node pool from cache: %w", err))
	}

	// Get the NodePoolVersion controller document to check IntentFailed condition
	controllerCRUD := c.resourcesDBClient.
		HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).
		NodePools(operation.ExternalID.Parent.Name).
		Controllers(operation.ExternalID.Name)
	controllerDoc, err := controllerCRUD.Get(ctx, nodepoolversion.NodepoolVersionControllerName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The document is written after the NodePoolVersion controller's first sync, which
		// races this one on the same informer event. Treat its absence as "not reported yet"
		// rather than an observation error, so a fresh create does not log and count a
		// controller error on every reconcile until that first sync lands.
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "Waiting for version to be accepted"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)

	// On create, the resulting desired version in the service provider node pool is not initially set.
	// The NodePoolVersion controller sets this based on the customer desired version upon successful validation.
	// If the resulting desired version is nil, it means the NodePoolVersion controller either hasn't completed
	// processing the version or the version validation failed.
	resultingDesiredVersion := serviceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
	if resultingDesiredVersion == nil {
		// Check if version validation failed
		if intentFailedCondition != nil && intentFailedCondition.Status == metav1.ConditionTrue &&
			intentFailedCondition.Reason == coreapi.VersionUpgradeNotAcceptedReason {
			return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent, intentFailedCondition.Message, nil), nil
		}
		// Otherwise, stay in an Accepted state while the NodePoolVersion controller is still processing the version.
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "Waiting for version to be accepted"), nil
	}

	// Sanity check: the resulting desired version should match the customer desired version
	customerDesiredVersion, err := semver.Parse(nodePool.Properties.Version.ID)
	if err != nil {
		return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent,
			fmt.Sprintf("Invalid version format %q: %s", nodePool.Properties.Version.ID, err.Error()), nil), nil
	}
	if !customerDesiredVersion.EQ(*resultingDesiredVersion) {
		logger.Error(fmt.Errorf("desired version mismatch on create"),
			"customerDesiredVersion", customerDesiredVersion.String(),
			"resultingDesiredVersion", resultingDesiredVersion.String(),
		)
		return operationbase.NewFailedOperationState(
			coreapi.CloudErrorCodeInternalServerError,
			fmt.Sprintf("Could not resolve version %s", customerDesiredVersion.String()),
			nil), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
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
