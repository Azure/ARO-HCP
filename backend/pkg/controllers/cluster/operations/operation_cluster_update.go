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

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterUpdate struct {
	clock                           utilsclock.PassiveClock
	resourcesDBClient               corecosmosstorage.ResourcesDBClient
	clusterServiceClient            ocm.ClusterServiceClientSpec
	clusterLister                   corelisters.ClusterLister
	serviceProviderClusterLister    corelisters.ServiceProviderClusterLister
	readDesireLister                kubeapplierlisters.ReadDesireLister
	activeOperationsLister          corelisters.ActiveOperationLister
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
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers coreinformers.BackendInformers,
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

	controller := controllerutils.NewGenericOperationController(
		"OperationClusterUpdate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationClusterUpdate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterUpdate) shouldReconcileOperationAndResourceStatus(cluster *coreapi.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationClusterUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.activeOperationsLister.Get(ctx, key.SubscriptionID, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	existingCluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
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

	var persistErr *coreapi.CloudErrorBody
	if operationalState.ProvisioningState == coreapi.ProvisioningStateFailed {
		persistErr = &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeForMessage(operationalState.Message, coreapi.CloudErrorCodeInvalidRequestContent),
			Message: operationalState.Message,
		}
	}

	logger.Info("updating status")
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *operationClusterUpdate) determineOperationState(ctx context.Context, operation *coreapi.Operation, existingCluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

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
	operationStates := []*operationbase.OperationState{}

	if operationState, err := c.desiredVersionResolutionOperationState(ctx, operation, existingCluster, existingServiceProviderCluster); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, operationState.WithSource("controlPlaneDesiredVersionResolution"))
	}
	if operationState, csErr := c.clusterServiceClusterStatusOperationState(ctx, operation, existingCSCluster.Status(), *clusterCSID); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.WithSource("clusterServiceClusterStatus"))
	}
	if operationState, csErr := c.clusterServiceClusterSpecOperationState(existingCluster, existingCSCluster); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.WithSource("clusterServiceClusterSpec"))
	}

	if operationState, hsErr := c.hypershiftHostedClusterOperationState(ctx, existingCluster, existingServiceProviderCluster); hsErr != nil {
		errs = append(errs, utils.TrackError(hsErr))
	} else {
		operationStates = append(operationStates, operationState.WithSource("hypershiftHostedCluster"))
	}
	if operationState, autoscalerErr := c.hypershiftControlPlaneClusterAutoscalerState(ctx, existingCluster, existingServiceProviderCluster); autoscalerErr != nil {
		errs = append(errs, utils.TrackError(autoscalerErr))
	} else {
		operationStates = append(operationStates, operationState.WithSource("hypershiftControlPlaneClusterAutoscaler"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(operationStates) == 0 {
		return nil, errors.New("no operation states")
	}
	slices.SortStableFunc(operationStates, operationbase.CompareOperationState)
	if operationStates[0] == nil {
		return nil, errors.New("nil operation state")
	}
	logger.Info("determined cluster update operation status", "operationStates", operationStates)
	picked, err := operationbase.PickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked cluster update operation status", "picked", picked)
	return picked, nil
}

func (c *operationClusterUpdate) desiredVersionResolutionOperationState(ctx context.Context, operation *coreapi.Operation, existingCluster *coreapi.HCPOpenShiftCluster, spc *coreapi.ServiceProviderCluster) (*operationbase.OperationState, error) {
	resultingDesiredVersion := spc.Spec.ControlPlaneVersion.DesiredVersion
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
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
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
	intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)
	if intentFailedCondition == nil || intentFailedCondition.Status != metav1.ConditionTrue || intentFailedCondition.Reason != coreapi.VersionUpgradeNotAcceptedReason {
		// Customer desired minor differs from the service provider resolved version, and the
		// ControlPlaneDesiredVersion controller has not yet set IntentFailed (VersionUpgradeNotAccepted).
		// Stay Accepted while resolution runs; fail once elapsed exceeds 129s from the first
		// time this process observed the mismatch for this operation, so a
		// controller restart does not immediately fail long-running operations.
		pending := operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "customer desired version does not match resolved desired version")
		firstSeen, ok := c.desiredVersionMismatchFirstSeen.Get(operation.ResourceID.String())
		if !ok {
			c.desiredVersionMismatchFirstSeen.Add(operation.ResourceID.String(), c.clock.Now())
			return pending, nil
		}
		if c.clock.Since(firstSeen.(time.Time)) <= 129*time.Second {
			return pending, nil
		}
		msg := fmt.Sprintf(
			"timed out after 129s waiting for resolution of desired version from '%s' cluster version",
			existingCluster.CustomerProperties.Version.ID,
		)
		c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
		return operationbase.NewOperationState(coreapi.ProvisioningStateFailed, msg), nil
	}
	c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
	return operationbase.NewOperationState(coreapi.ProvisioningStateFailed, intentFailedCondition.Message), nil
}

func (c *operationClusterUpdate) clusterServiceClusterStatusOperationState(ctx context.Context, operation *coreapi.Operation, existingCSClusterStatus *arohcpv1alpha1.ClusterStatus, clusterServiceID metadataapi.InternalID) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	newOperationStatus, opError, err := operationbase.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, existingCSClusterStatus, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	msg := ""
	if opError != nil {
		msg = opError.Message
	}

	return operationbase.NewOperationState(newOperationStatus, msg), nil
}
