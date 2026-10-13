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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const clusterValidationFailureTimeout = 10 * time.Minute

// validationProbeFreshnessWindow bounds how old a validation's last completed
// check may be while its False condition still counts toward terminally
// failing the operation. The validation controllers requeue failing checks
// every 60-90s, so a probe older than this window means the check has not
// actually run recently (stalled controller, workqueue backlog, or Azure RBAC
// propagation lag) and the stored failure is stale evidence, not a freshly
// confirmed one.
const validationProbeFreshnessWindow = 5 * time.Minute

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
// failed validation and the operation itself at least ten minutes before failing.
// Unknown conditions remain pending and do not imply an invalid resource.
// A failed validation only fails the operation terminally when its last
// completed check is fresh: the conditions' LastTransitionTime only advances
// when the outcome changes, so a condition can look old simply because the
// validation controller has not re-checked it lately (stalled requeue,
// workqueue backlog, or Azure RBAC propagation lag after role assignments
// were created post-create). When the probe is stale the operation stays
// Provisioning and the validation controllers' retry cadence, with the
// CreateOperationCompletionDeadline as backstop, gets another chance.
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
		if validation.Status == metav1.ConditionFalse {
			state.WithCloudErrorCode(coreapi.CloudErrorCodeInvalidResource)
			// When no probe time was recorded (e.g. the condition predates
			// probe recording), fall back to the age-only check so the
			// terminal-failure behavior is unchanged during rollout.
			probeIsFresh := true
			if probeTime, ok := serviceProviderCluster.Status.ValidationProbeTimes[validation.Type]; ok {
				probeIsFresh = !probeTime.IsZero() && now.Sub(probeTime.Time) <= validationProbeFreshnessWindow
			}
			if operationOldEnough && !validation.LastTransitionTime.IsZero() && now.Sub(validation.LastTransitionTime.Time) >= clusterValidationFailureTimeout && probeIsFresh {
				state.ProvisioningState = coreapi.ProvisioningStateFailed
			}
		}
	}
	slices.Sort(messages)
	state.Message = strings.Join(messages, "; ")
	if state.ProvisioningState == coreapi.ProvisioningStateFailed {
		return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidResource, state.Message, &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeInvalidResource,
			Message: state.Message,
		})
	}
	return state
}
