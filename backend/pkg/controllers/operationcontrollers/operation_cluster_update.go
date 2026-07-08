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
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterUpdateOperationTimeout is the maximum time a cluster update operation
// may remain in a non-terminal state before it is marked Failed. Elapsed time
// is measured from Operation.StartTime, which is set when the frontend creates
// the operation document.
const clusterUpdateOperationTimeout = 1 * time.Hour

type operationClusterUpdate struct {
	clock                           utilsclock.PassiveClock
	resourcesDBClient               database.ResourcesDBClient
	clusterServiceClient            ocm.ClusterServiceClientSpec
	clusterLister                   listers.ClusterLister
	serviceProviderClusterLister    listers.ServiceProviderClusterLister
	readDesireLister                dblisters.ReadDesireLister
	activeOperationsLister          listers.ActiveOperationLister
	notificationClient              *http.Client
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
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister dblisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	syncer := &operationClusterUpdate{
		clock:                           clock,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		clusterLister:                   clusterLister,
		serviceProviderClusterLister:    serviceProviderClusterLister,
		readDesireLister:                readDesireLister,
		activeOperationsLister:          activeOperationsLister,
		notificationClient:              notificationClient,
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

func (c *operationClusterUpdate) shouldReconcileOperationAndResourceStatus(cluster *api.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationClusterUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.activeOperationsLister.Get(ctx, key.SubscriptionID, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	existingCluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if database.IsNotFoundError(err) {
		logger.Info("cluster not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	if operation.ResourceID.Name != existingCluster.ServiceProviderProperties.ActiveOperationID {
		logger.Info("cluster active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "clusterActiveOperationID", existingCluster.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(existingCluster) {
		return nil // no work to do
	}

	operationalState, err := c.determineOperationState(ctx, operation, existingCluster)
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *arm.CloudErrorBody
	if operationalState.ProvisioningState == arm.ProvisioningStateFailed {
		persistErr = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: operationalState.Message,
		}
	}

	logger.Info("updating status")
	err = UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, postAsyncNotificationFn(c.notificationClient))
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *operationClusterUpdate) determineOperationState(ctx context.Context, operation *api.Operation, existingCluster *api.HCPOpenShiftCluster) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	// Fail fast on operation timeout before calling downstream checks. If the operation has timed out, we
	// return the timeout state instead of the other states.
	timeoutState, err := c.operationTimeoutOperationState(operation)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if timeoutState != nil {
		logger.Info("cluster update operation timed out", "startTime", operation.StartTime, "timeout", clusterUpdateOperationTimeout)
		return timeoutState.withSource("operationTimeout"), nil
	}

	clusterCSID := existingCluster.ServiceProviderProperties.ClusterServiceID
	existingCSCluster, err := c.clusterServiceClient.GetCluster(ctx, *clusterCSID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cluster service: %w", err))
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider cluster from cache: %w", err))
	}

	errs := []error{}
	operationStates := []*operationState{}

	if operationState, err := c.desiredVersionResolutionOperationState(ctx, operation, existingCluster, existingServiceProviderCluster); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, operationState.withSource("controlPlaneDesiredVersionResolution"))
	}
	if operationState, csErr := c.clusterServiceClusterStatusOperationState(ctx, operation, existingCSCluster.Status()); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("clusterServiceClusterStatus"))
	}
	if operationState, csErr := c.clusterServiceClusterSpecOperationState(existingCluster, existingCSCluster); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("clusterServiceClusterSpec"))
	}

	if operationState, hsErr := c.hypershiftHostedClusterOperationState(ctx, existingCluster, existingServiceProviderCluster); hsErr != nil {
		errs = append(errs, utils.TrackError(hsErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("hypershiftHostedCluster"))
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
	logger.Info("picked cluster update operation status", "picked", picked)
	return picked, nil
}

// operationTimeoutOperationState checks whether the cluster update operation
// has exceeded clusterUpdateOperationTimeout. Returns (nil, nil) while the
// operation is still within the deadline, (Failed, nil) once timed out, or an
// error if StartTime is unset. Called before downstream status checks so a
// timed-out operation is marked as Failed and not evaluated further in that case.
func (c *operationClusterUpdate) operationTimeoutOperationState(operation *api.Operation) (*operationState, error) {
	if operation.StartTime.IsZero() {
		return nil, fmt.Errorf("operation %q has no start time", operation.ResourceID.Name)
	}
	if c.clock.Since(operation.StartTime) <= clusterUpdateOperationTimeout {
		return nil, nil
	}
	return newOperationState(
		arm.ProvisioningStateFailed,
		fmt.Sprintf("timed out after %s waiting for cluster update to complete", clusterUpdateOperationTimeout),
	), nil
}

func (c *operationClusterUpdate) desiredVersionResolutionOperationState(ctx context.Context, operation *api.Operation, existingCluster *api.HCPOpenShiftCluster, existingServiceProviderCluster *api.ServiceProviderCluster) (*operationState, error) {
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

func (c *operationClusterUpdate) clusterServiceClusterStatusOperationState(ctx context.Context, operation *api.Operation, existingCSClusterStatus *arohcpv1alpha1.ClusterStatus) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	newOperationStatus, opError, err := convertClusterStatus(ctx, c.clusterServiceClient, operation, existingCSClusterStatus)
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
