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

package controllerutils

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/equality"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type Controller interface {
	SyncOnce(ctx context.Context, keyObj any) error
	Run(ctx context.Context, threadiness int)
}

// OperationKey is for driving workqueues keyed for operations
type OperationKey struct {
	SubscriptionID   string `json:"subscriptionID"`
	OperationName    string `json:"operationName"`
	ParentResourceID string `json:"parentResourceID"`
}

func (k *OperationKey) GetParentResourceID() *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(k.ParentResourceID))
}

func (k *OperationKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetParentResourceID()).
			AddOperationID(k.OperationName)...)
}

func (k *OperationKey) InitialController(controllerName string) *api.Controller {
	// TODO, this structure only allows one status per operation controller even if there are multiple instances of the operation
	// TODO, this may or may not age well. Nesting is possible or we could actually separate controllers that way (probably useful).
	// TODO, leaving this as a thing open to change in the future.
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetParentResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID, ExternalID: k.GetParentResourceID(),
		Status: api.ControllerStatus{
			Conditions: []api.Condition{},
		},
	}
}

// HCPClusterKey is for driving workqueues keyed for clusters
type HCPClusterKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
}

func (k *HCPClusterKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(api.ToClusterResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName))
}

func (k *HCPClusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k *HCPClusterKey) InitialController(controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		ExternalID: k.GetResourceID(),
		Status: api.ControllerStatus{
			Conditions: []api.Condition{},
		},
	}
}

// clock is used by helper functions for setting last transition time.  It is injectable for unit testing.
var clock utilsclock.Clock = utilsclock.RealClock{}

// controllerMutationFunc is called when trying to write a controller. It gives a spot for computation of a value.
// It should only perform short calls, not long lookups.  It must not fail. Think of it as a way to write information
// that you have already precomputed.
type controllerMutationFunc func(controller *api.Controller)

func ReportSyncError(syncErr error) controllerMutationFunc {
	return func(controller *api.Controller) {
		if syncErr == nil {
			SetCondition(&controller.Status.Conditions, api.Condition{
				Type:    "Degraded",
				Status:  api.ConditionFalse,
				Reason:  "NoErrors",
				Message: "As expected.",
			})
			return
		}

		SetCondition(&controller.Status.Conditions, api.Condition{
			Type:    "Degraded",
			Status:  api.ConditionTrue,
			Reason:  "Failed",
			Message: fmt.Sprintf("Had an error while syncing: %s", syncErr.Error()),
		})
	}
}

type initialControllerFunc func(controllerName string) *api.Controller

// WriteController will read the existing value, call the mutations in order, then write the result.  It only tries *once*.
// If it fails, then the an error is returned.  This detail is important, it doesn't even retry conflicts.  This is so that
// if a failure happens the control-loop will re-run and restablish the information it was trying to write as valid.
// This prevents accidental recreation of controller instances in cosmos during a delete.
func WriteController(ctx context.Context, controllerCRUD database.ResourceCRUD[api.Controller], controllerName string, initialControllerFn initialControllerFunc, mutationFns ...controllerMutationFunc) error {
	logger := utils.LoggerFromContext(ctx)

	existingController, err := controllerCRUD.Get(ctx, controllerName)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return fmt.Errorf("failed to get existing controller state: %w", err)
	}

	var desiredController *api.Controller
	if existingController == nil { // fill in for conveniently avoiding NPEs
		desiredController = initialControllerFn(controllerName)
	} else {
		desiredController = existingController.DeepCopy()
	}
	for _, mutationFn := range mutationFns {
		mutationFn(desiredController)
	}

	if equality.Semantic.DeepEqual(existingController, desiredController) {
		// nothing to report.
		return nil
	}

	if existingController == nil {
		_, createErr := controllerCRUD.Create(ctx, desiredController, nil)
		if createErr != nil {
			logger.Error(createErr, "failed to create")
			return fmt.Errorf("failed to create existing controller state: %w", createErr)
		}
		return nil
	}

	_, replaceErr := controllerCRUD.Replace(ctx, desiredController, nil)
	if replaceErr != nil {
		logger.Error(replaceErr, "failed to replace")
		return fmt.Errorf("failed to replace existing controller state: %w", replaceErr)
	}
	return nil
}

// SetCondition sets the condition with the given type in the list of conditions.
// If the condition with condition type conditionType is not found, it is added.
// If the condition with condition type conditionType is found, it is updated.
// When there's a transition in the condition's status, the last transition time
// is updated to the current time. lastTranitionTime in toSet is always ignored.
func SetCondition(conditions *[]api.Condition, toSet api.Condition) {
	existingCondition := GetCondition(*conditions, toSet.Type)
	if existingCondition == nil {
		toSet.LastTransitionTime = clock.Now()
		*conditions = append(*conditions, toSet)
		return
	}

	newCondition := existingCondition.DeepCopy()
	if newCondition.Status != toSet.Status {
		newCondition.LastTransitionTime = clock.Now()
	}
	newCondition.Status = toSet.Status
	newCondition.Reason = toSet.Reason
	newCondition.Message = toSet.Message

	for i := range *conditions {
		if (*conditions)[i].Type == toSet.Type {
			(*conditions)[i] = *newCondition
			return
		}
	}
}

// GetCondition returns a copy to the condition with the given type from the list of conditions.
// It returns a pointer for a clear indication of "not found", it doesn't return a reference intended for mutation
// of the original list.
// If the list of conditions is nil, returns nil.
// If the condition with condition type conditionType is not found, returns nil.
// If there are multiple conditions with condition type conditionType the first
// one is returned.
func GetCondition(conditions []api.Condition, conditionType string) *api.Condition {
	if conditions == nil {
		return nil
	}
	for _, currentCondition := range conditions {
		if currentCondition.Type == conditionType {
			return &currentCondition
		}
	}
	return nil
}

// IsConditionTrue returns true if the condition with condition type
// conditionType is found and its status is True.
// If the condition is not found or its status is not True, returns false.
func IsConditionTrue(conditions []api.Condition, conditionType string) bool {
	condition := GetCondition(conditions, conditionType)
	return condition != nil && condition.Status == api.ConditionTrue
}
