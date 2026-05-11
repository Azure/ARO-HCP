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

package operationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterUpdate struct {
	resourcesDBClient               database.ResourcesDBClient
	clusterServiceClient            ocm.ClusterServiceClientSpec
	notificationClient              *http.Client
	clock                           clock.PassiveClock
	desiredVersionMismatchFirstSeen *lru.Cache
}

// NewOperationClusterUpdateController returns a new Controller instance that
// follows an asynchronous cluster update operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: Update
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationClusterUpdateController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationClusterUpdate{
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		notificationClient:              notificationClient,
		clock:                           clock.RealClock{},
		desiredVersionMismatchFirstSeen: lru.New(100000),
	}

	controller := NewGenericOperationController(
		"OperationClusterUpdate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationClusterUpdate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}
	if len(operation.InternalID.String()) == 0 {
		// we cannot proceed: yet.
		// TODO when we update to make clusterserice creation async, we need to handle this correctly.
		return nil
	}

	operationalState, err := c.determineOperationState(ctx, operation)
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *arm.CloudErrorBody
	if operationalState.provisioningState == arm.ProvisioningStateFailed {
		persistErr = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: operationalState.message,
		}
	}

	logger.Info("updating status")
	if err := UpdateOperationStatus(ctx, c.resourcesDBClient, operation, operationalState.provisioningState, persistErr, postAsyncNotificationFn(c.notificationClient)); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *operationClusterUpdate) determineOperationState(ctx context.Context, operation *api.Operation) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)
	errs := []error{}
	operationStates := []*operationState{}

	if operationState, err := c.desiredVersionResolutionOperationState(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, operationState)
	}
	if operationState, csErr := c.clusterServiceUpdateOperationState(ctx, operation); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(operationStates) == 0 {
		return nil, errors.New("no operation states")
	}
	slices.SortStableFunc(operationStates, compareOperationState)
	if operationStates[0] == nil {
		return nil, errors.New("nil operation state")
	}
	logger.Info("determined cluster update operation status", "operationStates", operationStates)
	picked, err := pickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked cluster update operation status", "provisioningState", picked.provisioningState, "message", picked.message)
	return picked, nil
}

func (c *operationClusterUpdate) desiredVersionResolutionOperationState(ctx context.Context, operation *api.Operation) (*operationState, error) {
	existingCluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	existingServiceProviderCluster, err := c.resourcesDBClient.ServiceProviderClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	resultingDesiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if resultingDesiredVersion == nil {
		return nil, utils.TrackError(fmt.Errorf("service provider cluster has no desired version"))
	}

	customerDesiredVersion, err := semver.ParseTolerant(existingCluster.CustomerProperties.Version.ID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if customerDesiredVersion.Major == resultingDesiredVersion.Major &&
		customerDesiredVersion.Minor == resultingDesiredVersion.Minor {
		c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
		return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
	}
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    operation.ExternalID.SubscriptionID,
		ResourceGroupName: operation.ExternalID.ResourceGroupName,
		HCPClusterName:    operation.ExternalID.Name,
	}
	controllerDoc, getControllerErr := controllerutils.GetOrCreateController(
		ctx,
		c.resourcesDBClient,
		operation.ExternalID,
		"ControlPlaneDesiredVersion",
		clusterKey.InitialController,
	)
	if getControllerErr != nil {
		return nil, utils.TrackError(getControllerErr)
	}
	intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, api.ControllerConditionTypeIntentFailed)
	if intentFailedCondition == nil || intentFailedCondition.Status != metav1.ConditionTrue || intentFailedCondition.Reason != api.VersionUpgradeNotAcceptedReason {
		// Customer desired minor differs from the service provider resolved version, and the
		// ControlPlaneDesiredVersion controller has not yet set IntentFailed (VersionUpgradeNotAccepted).
		// Stay Accepted while resolution runs; fail once elapsed exceeds 29s from the first
		// time this process observed the mismatch for this operation, so a
		// controller restart does not immediately fail long-running operations.
		pending := newOperationState(arm.ProvisioningStateAccepted, "customer desired version does not match resolved desired version")
		firstSeen, ok := c.desiredVersionMismatchFirstSeen.Get(operation.ResourceID.String())
		if !ok {
			c.desiredVersionMismatchFirstSeen.Add(operation.ResourceID.String(), c.clock.Now())
			return pending, nil
		}
		if c.clock.Since(firstSeen.(time.Time)) <= 29*time.Second {
			return pending, nil
		}
		msg := fmt.Sprintf(
			"timed out after 29s waiting for resolution of desired version from '%s' cluster version",
			existingCluster.CustomerProperties.Version.ID,
		)
		c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
		return newOperationState(arm.ProvisioningStateFailed, msg), nil
	}
	c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
	return newOperationState(arm.ProvisioningStateFailed, intentFailedCondition.Message), nil
}

func (c *operationClusterUpdate) clusterServiceUpdateOperationState(ctx context.Context, operation *api.Operation) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)
	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, operation.InternalID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	newOperationStatus, opError, err := convertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	msg := ""
	if opError != nil {
		msg = opError.Message
	}
	return newOperationState(newOperationStatus, msg), nil
}
