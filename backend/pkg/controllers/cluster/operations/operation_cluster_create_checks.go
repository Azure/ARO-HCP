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
	"encoding/json"
	"fmt"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	utilsclock "k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterCreateValidationCheck struct {
	clock                        utilsclock.PassiveClock
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreateValidationCheck)(nil)

func (c *clusterCreateValidationCheck) GetSourceName() string {
	return "clusterValidation"
}

func (c *clusterCreateValidationCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "ServiceProviderCluster not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider cluster from cache: %w", err))
	}
	return clusterValidationOperationState(serviceProviderCluster, operation.StartTime, c.clock.Now()), nil
}

type clusterCreateHostedClusterCheck struct {
	readDesireLister kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreateHostedClusterCheck)(nil)

func (c *clusterCreateHostedClusterCheck) GetSourceName() string {
	return "hypershiftHostedCluster"
}

func (c *clusterCreateHostedClusterCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	// Pull the HostedCluster directly from the per-cluster ReadDesire via
	// the union lister. The union lister hides per-MC routing so callers
	// don't need to know which management cluster the HostedCluster is on.
	readDesire, err := c.readDesireLister.GetForCluster(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "hosted cluster state not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful) {
		message := "ReadDesire has not yet successfully observed the target"
		if successfulCondition := meta.FindStatusCondition(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful); successfulCondition != nil {
			message = fmt.Sprintf("ReadDesire is not successful: %s: %s", successfulCondition.Reason, successfulCondition.Message)
		}
		logger.Info("ReadDesire is not successful", "readDesire.Status.Conditions", readDesire.Status.Conditions)
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ReadDesire has no kube content"), nil
	}

	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, hostedCluster); err != nil {
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
			return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix(message, hostedCluster)), nil
		}

		if !anyVersionInstalled {
			// can only check this when the success condition works, because this is unreliable otherwise
			message := describeVersionHistory(hostedCluster.Status.ControlPlaneVersion.History)
			logger.Info("hosted cluster control plane version not yet completed", "message", message, "hostedCluster.Status.ControlPlaneVersion.History", hostedCluster.Status.ControlPlaneVersion.History)
			return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix(message, hostedCluster)), nil
		}
	}

	if len(hostedCluster.Status.ControlPlaneEndpoint.Host) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix("hosted cluster has no control plane endpoint host", hostedCluster)), nil
	}
	if hostedCluster.Status.ControlPlaneEndpoint.Port == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix("hosted cluster has no control plane endpoint port", hostedCluster)), nil
	}

	// if we got here,
	// 1. the hosted cluster is available via condition
	// 2. the hosted cluster has successfully installed at least one version
	// 3. the hosted cluster has a control plane endpoint host and port
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type clusterCreateResourceCheck struct {
	clusterLister corelisters.ClusterLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreateResourceCheck)(nil)

func (c *clusterCreateResourceCheck) GetSourceName() string {
	return "cosmosCluster"
}

func (c *clusterCreateResourceCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		// if the cache doesn't have the cosmos cluster yet, we'll eventually recheck when we resync. Currently 10s for
		// active operations.  No need to fail and trigger an extra check.
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster state not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if len(cluster.ServiceProviderProperties.API.URL) == 0 {
		message := ".api.url is empty"
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type clusterCreateClusterServiceCheck struct {
	clusterLister        corelisters.ClusterLister
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreateClusterServiceCheck)(nil)

func (c *clusterCreateClusterServiceCheck) GetSourceName() string {
	return "clusterServiceClusterStatus"
}

func (c *clusterCreateClusterServiceCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster state not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	logger := utils.LoggerFromContext(ctx)

	// The Cluster Service resource is created asynchronously; until its ID is
	// populated there is nothing to query, so report the operation as still
	// provisioning rather than dereferencing a nil ClusterServiceID.
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster service has not been successfully created"), nil
	}

	clusterServiceID := *cluster.ServiceProviderProperties.ClusterServiceID

	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	newOperationStatus, opError, err := operationbase.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	state := operationbase.NewOperationState(newOperationStatus, operationbase.ClusterServiceOperationMessage(clusterStatus, opError))
	if opError != nil {
		state.WithCloudErrorCode(opError.Code)
	}
	return state, nil
}

type clusterCreatePlacementCheck struct {
	clusterLister                corelisters.ClusterLister
	clock                        utilsclock.PassiveClock
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreatePlacementCheck)(nil)

func (c *clusterCreatePlacementCheck) GetSourceName() string {
	return "placement"
}

func (c *clusterCreatePlacementCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return nil, utils.TrackError(err)
	}
	if serviceProviderCluster != nil && serviceProviderCluster.Spec.ManagementClusterResourceID != nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	message := "waiting for management cluster placement"
	if serviceProviderCluster == nil {
		message = "ServiceProviderCluster not cached yet"
	}
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster state not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	deadline := cluster.ServiceProviderProperties.CreateOperationCompletionDeadline
	if deadline == nil || c.clock.Now().Before(deadline.Time) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	message = "cluster placement did not complete before the deadline"
	operationError := &coreapi.CloudErrorBody{
		Code:    coreapi.CloudErrorCodeInternalServerError,
		Message: message,
	}
	if serviceProviderCluster != nil && serviceProviderCluster.Status.Placement != nil {
		if meta.IsStatusConditionFalse(serviceProviderCluster.Status.Placement.Conditions, coreapi.CapacityAvailableConditionType) {
			operationError.Code = coreapi.CloudErrorCodeCapacityHeavyUse
			// Placement diagnostics contain internal information; do not expose them.
			operationError.Message = "ARO HCP is currently experiencing capacity constraints. Try again later."
		}
	}
	return operationbase.NewFailedOperationState(operationError.Code, message, operationError), nil
}

type clusterCreateServingCACheck struct {
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreateServingCACheck)(nil)

func (c *clusterCreateServingCACheck) GetSourceName() string {
	return "servingCABundle"
}

func (c *clusterCreateServingCACheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	// The control-plane serving CA is mirrored into the service cluster (and
	// thus ServiceProviderCluster.Status.ServingCABundle is populated) for every
	// cluster that has a control-plane namespace, regardless of OpenShift
	// version. The create operation blocks until that bundle has been populated.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServiceProviderCluster not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(serviceProviderCluster.Status.ServingCABundle) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServingCABundle not yet populated"), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type clusterCreateRoleAssignmentsCheck struct {
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ operationbase.OperationStatusCalculator[struct{}] = (*clusterCreateRoleAssignmentsCheck)(nil)

func (c *clusterCreateRoleAssignmentsCheck) GetSourceName() string {
	return "roleAssignments"
}

func (c *clusterCreateRoleAssignmentsCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, _ struct{}) (*operationbase.OperationState, error) {
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServiceProviderCluster not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	roleAssignments := serviceProviderCluster.Status.AzureResources.RoleAssignments
	if len(roleAssignments.AzureResources) == 0 || len(roleAssignments.PendingAzureResources) != 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "role assignments not yet confirmed"), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}
