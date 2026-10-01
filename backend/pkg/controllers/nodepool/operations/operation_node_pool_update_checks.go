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
	"strings"
	"time"

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolUpdateDesiredVersionCheck struct {
	nodePoolLister                  corelisters.NodePoolLister
	serviceProviderNodePoolLister   corelisters.ServiceProviderNodePoolLister
	clock                           utilsclock.PassiveClock
	resourcesDBClient               corecosmosstorage.ResourcesDBClient
	desiredVersionMismatchFirstSeen *lru.Cache
}

var _ operationbase.OperationStatusCalculator[*arohcpv1alpha1.NodePool] = (*nodePoolUpdateDesiredVersionCheck)(nil)

func (c *nodePoolUpdateDesiredVersionCheck) GetSourceName() string {
	return "nodePoolDesiredVersionResolution"
}

func (c *nodePoolUpdateDesiredVersionCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ *arohcpv1alpha1.NodePool) (*operationbase.OperationState, error) {
	existingNodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	existingServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider node pool from cache: %w", err))
	}
	resultingDesiredVersion := existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
	if resultingDesiredVersion == nil {
		return nil, utils.TrackError(fmt.Errorf("service provider node pool has no desired version"))
	}

	customerDesiredVersion := semver.MustParse(existingNodePool.Properties.Version.ID)

	operationID := strings.ToLower(operation.ResourceID.String())
	// If the operation is cancelled, its desiredVersionMismatchFirstSeen entry is never
	// explicitly removed. This is safe because operation.ResourceID is unique per operation,
	// so stale entries won't cause false matches for newer operations and will eventually
	// be evicted by the LRU.
	if customerDesiredVersion.EQ(*resultingDesiredVersion) {
		c.desiredVersionMismatchFirstSeen.Remove(operationID)
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	nodePoolKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    operation.ExternalID.SubscriptionID,
		ResourceGroupName: operation.ExternalID.ResourceGroupName,
		HCPClusterName:    operation.ExternalID.Parent.Name,
		HCPNodePoolName:   operation.ExternalID.Name,
	}

	controllerCRUD := c.resourcesDBClient.HCPClusters(nodePoolKey.SubscriptionID, nodePoolKey.ResourceGroupName).NodePools(nodePoolKey.HCPClusterName).Controllers(nodePoolKey.HCPNodePoolName)
	controllerDoc, getControllerErr := controllerCRUD.Get(ctx, nodepoolversion.NodepoolVersionControllerName)
	if getControllerErr != nil {
		return nil, utils.TrackError(getControllerErr)
	}

	intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)

	if intentFailedCondition == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "customer desired version not yet calculated"), nil
	}
	// Customer desired version differs from the service provider resolved version, and the
	// NodePoolVersion controller has not yet set IntentFailed (VersionUpgradeNotAccepted)
	// for this version. Stay Accepted while resolution runs; fail once elapsed exceeds
	// 129s from the first time this process observed the mismatch for this operation.
	// This avoids immediately failing long-running operations after controller restarts
	// and is double the relistDuration of the nodepool and serviceProviderNodePool coreinformers.
	// This will not solve all the edge cases, but it will give enough time to the other controllers to act.
	if intentFailedCondition.Status != metav1.ConditionTrue || intentFailedCondition.Reason != coreapi.VersionUpgradeNotAcceptedReason {
		pending := operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "customer desired version does not match resolved desired version")
		firstSeen, ok := c.desiredVersionMismatchFirstSeen.Get(operationID)
		if !ok {
			c.desiredVersionMismatchFirstSeen.Add(operationID, c.clock.Now())
			return pending, nil
		}
		if c.clock.Since(firstSeen.(time.Time)) <= 129*time.Second {
			return pending, nil
		}
		msg := fmt.Sprintf(
			"timed out after 129s waiting for resolution of desired version from '%s' node pool version",
			existingNodePool.Properties.Version.ID,
		)
		c.desiredVersionMismatchFirstSeen.Remove(operationID)
		return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent, msg, nil), nil
	}
	c.desiredVersionMismatchFirstSeen.Remove(operationID)
	return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent, intentFailedCondition.Message, nil), nil
}

type nodePoolUpdateClusterServiceStatusCheck struct{}

var _ operationbase.OperationStatusCalculator[*arohcpv1alpha1.NodePool] = (*nodePoolUpdateClusterServiceStatusCheck)(nil)

func (c *nodePoolUpdateClusterServiceStatusCheck) GetSourceName() string {
	return "clusterServiceNodePoolStatus"
}

func (c *nodePoolUpdateClusterServiceStatusCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, csNodePool *arohcpv1alpha1.NodePool) (*operationbase.OperationState, error) {
	existingCSNodePoolStatus := csNodePool.Status()
	logger := utils.LoggerFromContext(ctx)
	newOperationStatus, opError, err := operationbase.ConvertNodePoolStatus(operation, existingCSNodePoolStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	state := operationbase.NewOperationState(newOperationStatus, operationbase.NodePoolServiceOperationMessage(existingCSNodePoolStatus, opError))
	if opError != nil {
		state.Message = opError.Message
		state.WithCloudErrorCode(opError.Code)
	}
	return state, nil
}

type nodePoolUpdateClusterServiceSpecCheck struct {
	nodePoolLister corelisters.NodePoolLister
}

var _ operationbase.OperationStatusCalculator[*arohcpv1alpha1.NodePool] = (*nodePoolUpdateClusterServiceSpecCheck)(nil)

func (c *nodePoolUpdateClusterServiceSpecCheck) GetSourceName() string {
	return "clusterServiceNodePoolSpec"
}

func (c *nodePoolUpdateClusterServiceSpecCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, csNodePool *arohcpv1alpha1.NodePool) (*operationbase.OperationState, error) {
	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if matches, message := c.clusterServiceNodePoolSpecMatchesDesired(nodePool, csNodePool); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type nodePoolUpdateHypershiftCheck struct {
	nodePoolLister   corelisters.NodePoolLister
	readDesireLister kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[*arohcpv1alpha1.NodePool] = (*nodePoolUpdateHypershiftCheck)(nil)

func (c *nodePoolUpdateHypershiftCheck) GetSourceName() string {
	return "hypershiftNodePool"
}

func (c *nodePoolUpdateHypershiftCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, csNodePool *arohcpv1alpha1.NodePool) (*operationbase.OperationState, error) {
	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	logger := utils.LoggerFromContext(ctx)

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
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "Hypershift NodePool has not been observed yet"), nil
	}

	if matches, message := c.hypershiftNodePoolSpecMatchesDesired(nodePool, csNodePool, hypershiftNodePool); !matches {
		logger.Info("hypershift NodePool spec does not match desired configuration", "message", message)
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}

	if matches, message := c.hypershiftNodePoolStatusMatchesDesired(nodePool, hypershiftNodePool.Status); !matches {
		logger.Info("hypershift NodePool status does not match desired configuration", "message", message)
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}
