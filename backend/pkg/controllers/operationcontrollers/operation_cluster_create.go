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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterCreate struct {
	clusterLister                         listers.ClusterLister
	clusterManagementClusterContentLister listers.ManagementClusterContentLister
	resourcesDBClient                     database.ResourcesDBClient
	clusterServiceClient                  ocm.ClusterServiceClientSpec
	notificationClient                    *http.Client
}

// NewOperationClusterCreateController returns a new Controller instance that
// follows an asynchronous cluster creation operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: Create
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationClusterCreateController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, clusterManagementClusterContentLister := informers.ManagementClusterContents()
	syncer := &operationClusterCreate{
		clusterLister:                         clusterLister,
		clusterManagementClusterContentLister: clusterManagementClusterContentLister,
		resourcesDBClient:                     resourcesDBClient,
		clusterServiceClient:                  clusterServiceClient,
		notificationClient:                    notificationClient,
	}

	controller := NewGenericOperationController(
		"OperationClusterCreate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationClusterCreate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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
		// TODO when we update to make clusterserice creation async, we need https://github.com/Azure/ARO-HCP/pull/4695 or similar
		// and we need to wire up a fail-safe where if we have no ID and we time out, we report the best failure we can.
		return nil
	}
	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	cosmosNewOperationState, err := c.determineOperationStatus(ctx, operation)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status via cosmos", "newStatus", cosmosNewOperationState.provisioningState, "newOperationMessage", cosmosNewOperationState.message)

	newOperationStatus, opError, err := convertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)

	if newOperationStatus == arm.ProvisioningStateSucceeded && cosmosNewOperationState.provisioningState != arm.ProvisioningStateSucceeded {
		// we want to require that the cosmos view of cluster creation is also complete before we mark it.  This ensures (among other things)
		// that our ability to read maestro is successful.
		// Once we have confidence in our ability to determine that cluster is functional, we'll stop checking cluster-service at all.
		return fmt.Errorf("cosmos operation status is %q, but cluster-service operation status is %q", cosmosNewOperationState.provisioningState, newOperationStatus)
	}

	logger.Info("updating status")
	err = UpdateOperationStatus(ctx, c.resourcesDBClient, operation, newOperationStatus, opError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationClusterCreate) determineOperationStatus(ctx context.Context, operation *api.Operation) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	errs := []error{}
	operationStates := []*operationState{}

	if currState, err := c.hostedClusterOperationStatus(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState)
	}
	if currState, err := c.clusterOperationStatus(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	// cheap and easy backup check for potential accidents in future code.
	if len(operationStates) == 0 {
		return nil, errors.New("no operation states")
	}
	slices.SortStableFunc(operationStates, compareOperationState)

	if operationStates[0] == nil {
		return nil, errors.New("nil operation state")
	}
	logger.Info("determined cluster create operation status", "operationStates", operationStates)

	picked, err := pickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked cluster create operation status", "provisioningState", picked.provisioningState, "message", picked.message)
	return picked, nil
}

func (c *operationClusterCreate) clusterOperationStatus(ctx context.Context, operation *api.Operation) (*operationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if len(cluster.ServiceProviderProperties.API.URL) == 0 {
		message := ".api.url is empty"
		return newOperationState(arm.ProvisioningStateProvisioning, message), nil
	}

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// minVersionsWithValidSuccessCondition maps from <major>.<micro> to the first z-stream version that includes the fix for
// control plane validation success.
var minVersionsWithValidSuccessCondition = map[string]semver.Version{
	"4.19": api.Must(semver.Parse("4.19.999")),
	"4.20": api.Must(semver.Parse("4.20.999")),
	"4.21": api.Must(semver.Parse("4.21.999")),
	"4.22": api.Must(semver.Parse("4.22.999")),
}

func (c *operationClusterCreate) hostedClusterOperationStatus(ctx context.Context, operation *api.Operation) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	hostedClusterContent, err := c.clusterManagementClusterContentLister.GetForCluster(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	if database.IsNotFoundError(err) {
		return newOperationState(arm.ProvisioningStateProvisioning, ""), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !meta.IsStatusConditionFalse(hostedClusterContent.Status.Conditions, "Degraded") {
		message := "maestro bundle is degraded, degraded condition missing"
		if degradedCondition := meta.FindStatusCondition(hostedClusterContent.Status.Conditions, "Degraded"); degradedCondition != nil {
			message = fmt.Sprintf("maestro bundle is degraded: %s: %s", degradedCondition.Reason, degradedCondition.Message)
		}
		logger.Info("maestro bundle is degraded", "hostedClusterContent.Status.Conditions", hostedClusterContent.Status.Conditions)
		return newOperationState(arm.ProvisioningStateProvisioning, message), nil
	}

	if hostedClusterContent.Status.KubeContent == nil {
		return newOperationState(arm.ProvisioningStateProvisioning, "maestro bundle has no kube content"), nil
	}
	if len(hostedClusterContent.Status.KubeContent.Items) == 0 {
		return newOperationState(arm.ProvisioningStateProvisioning, "maestro bundle has no items in kube content"), nil
	}
	if len(hostedClusterContent.Status.KubeContent.Items) > 1 {
		return nil, utils.TrackError(fmt.Errorf("unexpected number of kube content items: %d", len(hostedClusterContent.Status.KubeContent.Items)))
	}

	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(hostedClusterContent.Status.KubeContent.Items[0].Raw, hostedCluster); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to decode HostedCluster: %w", err))
	}

	anyVersionInstalled := false
	anyVersionWithValidSuccessCondition := false
	for _, historicalVersion := range hostedCluster.Status.ControlPlaneVersion.History {
		if historicalVersion.State == configv1.CompletedUpdate {
			anyVersionInstalled = true
		}

		currVersion, err := semver.Parse(historicalVersion.Version)
		if err != nil {
			logger.Info("failed to parse version", "version", historicalVersion.Version, "error", err)
			continue
		}
		currMajorMinor := fmt.Sprintf("%d.%d", currVersion.Major, currVersion.Minor)
		if minVersion, ok := minVersionsWithValidSuccessCondition[currMajorMinor]; ok && currVersion.LT(minVersion) {
			// if the current version is less than the min version where this takes effect.
			continue
		}
		anyVersionWithValidSuccessCondition = true
	}

	if anyVersionWithValidSuccessCondition {
		// can only check this when the success condition works, because this is unreliable otherwise
		if !meta.IsStatusConditionTrue(hostedCluster.Status.Conditions, string(v1beta1.HostedClusterAvailable)) {
			message := "hosted cluster is not available, condition missing"
			if availableCondition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(v1beta1.HostedClusterAvailable)); availableCondition != nil {
				message = fmt.Sprintf("hosted cluster is not available: %s: %s", availableCondition.Reason, availableCondition.Message)
			}
			logger.Info("hosted cluster is not available", "hostedCluster.Status.Conditions", hostedCluster.Status.Conditions)
			return newOperationState(arm.ProvisioningStateProvisioning, message), nil
		}

		if !anyVersionInstalled {
			// can only check this when the success condition works, because this is unreliable otherwise
			logger.Info("hosted cluster has no installed version", "hostedCluster.Status.ControlPlaneVersion.History", hostedCluster.Status.ControlPlaneVersion.History)
			return newOperationState(arm.ProvisioningStateProvisioning, "hosted cluster has no installed version"), nil
		}
	}

	if len(hostedCluster.Status.ControlPlaneEndpoint.Host) == 0 {
		return newOperationState(arm.ProvisioningStateProvisioning, "hosted cluster has no control plane endpoint host"), nil
	}
	if hostedCluster.Status.ControlPlaneEndpoint.Port == 0 {
		return newOperationState(arm.ProvisioningStateProvisioning, "hosted cluster has no control plane endpoint port"), nil
	}

	// if we got here,
	// 1. the hosted cluster is available via condition
	// 2. the hosted cluster has successfully installed at least one version
	// 3. the hosted cluster has a control plane endpoint host and port
	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}
