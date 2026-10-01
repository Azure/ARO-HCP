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
	"time"

	"github.com/blang/semver/v4"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type clusterUpdateValidationCheck struct {
	clock utilsclock.PassiveClock
}

var _ operationbase.OperationStatusCalculator[clusterUpdateOperationStatusInput] = (*clusterUpdateValidationCheck)(nil)

func (c *clusterUpdateValidationCheck) GetSourceName() string {
	return "clusterValidation"
}

func (c *clusterUpdateValidationCheck) CalculateOperationStatus(_ context.Context, operation *coreapi.Operation, input clusterUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	serviceProviderCluster := input.ServiceProviderCluster
	return clusterValidationOperationState(serviceProviderCluster, operation.StartTime, c.clock.Now()), nil
}

type clusterUpdateDesiredVersionCheck struct {
	clusterLister                   corelisters.ClusterLister
	clock                           utilsclock.PassiveClock
	desiredVersionMismatchFirstSeen *lru.Cache
}

var _ operationbase.OperationStatusCalculator[clusterUpdateOperationStatusInput] = (*clusterUpdateDesiredVersionCheck)(nil)

func (c *clusterUpdateDesiredVersionCheck) GetSourceName() string {
	return "controlPlaneDesiredVersionResolution"
}

func (c *clusterUpdateDesiredVersionCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input clusterUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	existingCluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	spc := input.ServiceProviderCluster
	customerDesiredVersion, err := semver.ParseTolerant(existingCluster.CustomerProperties.Version.ID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// Forced assignment takes precedence over initial, minor, and normal rollout
	// assignment. Report an incompatible override directly instead of waiting
	// for a resolution that the normal controllers intentionally will not make.
	forced := spc.Spec.PinnedVersion.ExactVersion
	if forced == nil {
		forced = existingCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion
	}
	if forced != nil && (customerDesiredVersion.Major != forced.Major || customerDesiredVersion.Minor != forced.Minor) {
		c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
		return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent,
			fmt.Sprintf("requested cluster version %s conflicts with forced control plane version %s", existingCluster.CustomerProperties.Version.ID, forced), nil), nil
	}

	resultingDesiredVersion := spc.Spec.ControlPlaneVersion.DesiredVersion
	if resultingDesiredVersion != nil &&
		customerDesiredVersion.Major == resultingDesiredVersion.Major &&
		customerDesiredVersion.Minor == resultingDesiredVersion.Minor {
		c.desiredVersionMismatchFirstSeen.Remove(operation.ResourceID.String())
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	// Initial and minor rollout assignment resolve the version on the SPC.
	// The removed ControlPlaneDesiredVersion controller no longer produces
	// IntentFailed; reading or creating its status document cannot report
	// progress. Bound the wait for the current assignment controllers instead.
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
	return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInternalServerError, msg, nil), nil
}

type clusterUpdateClusterServiceStatusCheck struct {
	clusterLister        corelisters.ClusterLister
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ operationbase.OperationStatusCalculator[clusterUpdateOperationStatusInput] = (*clusterUpdateClusterServiceStatusCheck)(nil)

func (c *clusterUpdateClusterServiceStatusCheck) GetSourceName() string {
	return "clusterServiceClusterStatus"
}

func (c *clusterUpdateClusterServiceStatusCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input clusterUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	existingCSClusterStatus, clusterServiceID := input.ClusterServiceCluster.Status(), *cluster.ServiceProviderProperties.ClusterServiceID
	logger := utils.LoggerFromContext(ctx)

	newOperationStatus, opError, err := operationbase.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, existingCSClusterStatus, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	state := operationbase.NewOperationState(newOperationStatus, operationbase.ClusterServiceOperationMessage(existingCSClusterStatus, opError))
	if opError != nil {
		state.Message = opError.Message
		state.WithCloudErrorCode(opError.Code)
	}

	return state, nil
}

type clusterUpdateClusterServiceSpecCheck struct {
	clusterLister corelisters.ClusterLister
}

var _ operationbase.OperationStatusCalculator[clusterUpdateOperationStatusInput] = (*clusterUpdateClusterServiceSpecCheck)(nil)

func (c *clusterUpdateClusterServiceSpecCheck) GetSourceName() string {
	return "clusterServiceClusterSpec"
}

func (c *clusterUpdateClusterServiceSpecCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input clusterUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	csCluster := input.ClusterServiceCluster
	if matches, message := c.clusterServiceClusterSpecMatchesDesired(cluster, csCluster); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type clusterUpdateHostedClusterCheck struct {
	clusterLister    corelisters.ClusterLister
	readDesireLister kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[clusterUpdateOperationStatusInput] = (*clusterUpdateHostedClusterCheck)(nil)

func (c *clusterUpdateHostedClusterCheck) GetSourceName() string {
	return "hypershiftHostedCluster"
}

func (c *clusterUpdateHostedClusterCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input clusterUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	spc := input.ServiceProviderCluster
	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		cluster.ID.SubscriptionID,
		cluster.ID.ResourceGroupName,
		cluster.ID.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "Hypershift HostedCluster has not been observed yet"), nil
	}

	if matches, message := c.hypershiftHostedClusterSpecMatchesDesired(cluster, spc, hostedCluster); !matches {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}

	// TODO: add hypershiftHostedClusterStatusMatchesDesired to perform checks against Hypershift's HostedCluster status.

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

type clusterUpdateAutoscalerCheck struct {
	readDesireLister kubeapplierlisters.ReadDesireLister
}

var _ operationbase.OperationStatusCalculator[clusterUpdateOperationStatusInput] = (*clusterUpdateAutoscalerCheck)(nil)

func (c *clusterUpdateAutoscalerCheck) GetSourceName() string {
	return "hypershiftControlPlaneClusterAutoscaler"
}

func (c *clusterUpdateAutoscalerCheck) CalculateOperationStatus(ctx context.Context, operation *coreapi.Operation, input clusterUpdateOperationStatusInput) (*operationbase.OperationState, error) {
	spc := input.ServiceProviderCluster
	logger := utils.LoggerFromContext(ctx)

	lowest, _ := apihelpers.FindLowestAndHighestClusterVersion(spc.Status.ControlPlaneVersion.ActiveVersions)
	if lowest == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "control plane active versions not yet reported"), nil
	}
	// Compare major.minor only so pre-release builds (e.g. nightlies like
	// 4.20.0-0.nightly-...) still satisfy the 4.20+ autoscaler gate.
	lowestMajorMinor := semver.Version{Major: lowest.Major, Minor: lowest.Minor}
	if !lowestMajorMinor.GTE(semver.Version{Major: 4, Minor: 20}) {
		msg := fmt.Sprintf(
			`lowest active control plane version %q does not support ControlPlaneComponent cluster-autoscaler (requires 4.20+)`,
			lowest.String(),
		)
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, msg), nil
	}

	controlPlaneComponent, err := kubeapplierhelpers.GetCachedControlPlaneClusterAutoscalerForCluster(
		ctx, c.readDesireLister,
		operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if controlPlaneComponent == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, "cluster autoscaler state not cached yet"), nil
	}
	if !c.isControlPlaneClusterAutoscalerReady(controlPlaneComponent) {
		message := c.controlPlaneClusterAutoscalerNotReadyMessage(controlPlaneComponent)
		logger.Info("cluster autoscaler ControlPlaneComponent is not ready", "message", message)
		return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}
