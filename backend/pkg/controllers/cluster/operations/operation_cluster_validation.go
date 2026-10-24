// Copyright 2026 Microsoft Corporation
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
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterValidationFailureTimeout is how long a validation must have been continuously failing,
// on an operation at least as old, before the operation is failed terminally.
//
// It encodes "if this has not resolved itself by now, it never will, so fail and let the customer
// act". Ten minutes keeps that promise quick: a failed create or update is cheap to retry
// relative to holding an operation open against an eventually-consistent Azure control plane.
// Raising it further is not the right fix for a slow dependency — #6495 and #7218 both widened
// this window blindly and were each re-defeated by a longer tail. Diagnose the specific cause and
// report it (see missingPermissionsPendingPropagationReason) rather than waiting longer.
const clusterValidationFailureTimeout = 10 * time.Minute

func (c *operationClusterCreate) clusterValidation(ctx context.Context, operation *coreapi.Operation) (*operationbase.OperationState, error) {
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "ServiceProviderCluster not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider cluster from cache: %w", err))
	}
	return clusterValidationOperationState(serviceProviderCluster, operation.StartTime, c.clock.Now()), nil
}

func (c *operationClusterUpdate) clusterValidation(operation *coreapi.Operation, serviceProviderCluster *coreapi.ServiceProviderCluster) *operationbase.OperationState {
	return clusterValidationOperationState(serviceProviderCluster, operation.StartTime, c.clock.Now())
}

// clusterValidationOperationState waits for validations to pass, allowing each
// non-passing validation (failed or unknown) and the operation itself at least
// clusterValidationFailureTimeout before failing with InvalidResource.
func clusterValidationOperationState(serviceProviderCluster *coreapi.ServiceProviderCluster, operationStartTime, now time.Time) *operationbase.OperationState {
	state := operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, "")
	operationOldEnough := !operationStartTime.IsZero() && now.Sub(operationStartTime) >= clusterValidationFailureTimeout
	var messages []string
	for _, validation := range serviceProviderCluster.Status.Validations {
		if validation.Status == metav1.ConditionTrue {
			continue
		}
		if state.ProvisioningState == coreapi.ProvisioningStateSucceeded {
			state = operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "")
		}
		message := fmt.Sprintf("%s: %s", validation.Type, validation.Reason)
		if validation.Message != "" {
			message += ": " + validation.Message
		}
		messages = append(messages, message)
		state.WithCloudErrorCode(coreapi.CloudErrorCodeInvalidResource)
		validationOldEnough := operationOldEnough && !validation.LastTransitionTime.IsZero() && now.Sub(validation.LastTransitionTime.Time) >= clusterValidationFailureTimeout
		if validationOldEnough && !isPropagationPending(validation) {
			state.ProvisioningState = coreapi.ProvisioningStateFailed
		}
	}
	slices.Sort(messages)
	state.Message = strings.Join(messages, "; ")
	if state.ProvisioningState == coreapi.ProvisioningStateFailed {
		return operationbase.NewFailedOperationState(state.CloudErrorCode, state.Message, &coreapi.CloudErrorBody{
			Code:    state.CloudErrorCode,
			Message: state.Message,
		})
	}
	return state
}

// isPropagationPending reports whether a non-passing validation condition represents a known
// Azure RBAC propagation delay rather than a customer-actionable problem. These conditions are
// Unknown (not False) and carry the RoleAssignmentPropagationPending reason; they hold the
// operation in Provisioning but do not terminally fail it, because the role assignments the
// customer already created will eventually take effect.
func isPropagationPending(validation metav1.Condition) bool {
	return validationutils.IsPropagationPending(validation)
}
