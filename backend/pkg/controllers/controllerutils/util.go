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
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"

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

func (k *OperationKey) AddLoggerValues(logger *slog.Logger) *slog.Logger {
	parentResourceID := k.GetParentResourceID()
	hcpClusterName := ""
	switch {
	case strings.EqualFold(parentResourceID.ResourceType.String(), api.ClusterResourceType.String()):
		hcpClusterName = parentResourceID.Name
	case strings.EqualFold(parentResourceID.ResourceType.String(), api.NodePoolResourceType.String()):
		hcpClusterName = parentResourceID.Parent.Name
	case strings.EqualFold(parentResourceID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		hcpClusterName = parentResourceID.Name
	}

	return logger.With(
		"subscription_id", k.SubscriptionID,
		"resource_group", parentResourceID.ResourceGroupName,
		"resource_name", parentResourceID.Name,
		"resource_id", k.ParentResourceID,
		"operation_id", k.OperationName,
		"hcp_cluster_name", hcpClusterName,
	)
}

func (k *OperationKey) InitialController(controllerName string) *api.Controller {
	// TODO, this structure only allows one status per operation controller even if there are multiple instances of the operation
	// TODO, this may or may not age well. Nesting is possible or we could actually separate controllers that way (probably useful).
	// TODO, leaving this as a thing open to change in the future.
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetParentResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: *resourceID,
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
	parts := []string{
		"/subscriptions",
		k.SubscriptionID,
		"resourceGroups",
		k.ResourceGroupName,
		"providers",
		api.ProviderNamespace,
		api.ClusterResourceType.Type,
		k.HCPClusterName,
	}

	return api.Must(azcorearm.ParseResourceID(path.Join(parts...)))
}

func (k *HCPClusterKey) AddLoggerValues(logger *slog.Logger) *slog.Logger {
	return logger.With(
		"subscription_id", k.SubscriptionID,
		"resource_group", k.ResourceGroupName,
		"resource_name", k.HCPClusterName,
		"resource_id", k.GetResourceID().String(),
		"hcp_cluster_name", k.HCPClusterName, // provides standard location for resources like nodes
	)
}

func (k *HCPClusterKey) InitialController(controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: *resourceID,
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
			setCondition(&controller.Status.Conditions, api.Condition{
				Type:    "Degraded",
				Status:  api.ConditionFalse,
				Reason:  "NoErrors",
				Message: "As expected.",
			})
			return
		}

		setCondition(&controller.Status.Conditions, api.Condition{
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
		// TODO we'd prefer a DeepCopy, but we don't have one yet.
		temp := *existingController
		desiredController = &temp
		desiredController.Status.Conditions = slices.Clone(existingController.Status.Conditions)
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
			logger.Error("failed to create", "error", createErr)
			return fmt.Errorf("failed to create existing controller state: %w", createErr)
		}
		return nil
	}

	_, replaceErr := controllerCRUD.Replace(ctx, desiredController, nil)
	if replaceErr != nil {
		logger.Error("failed to replace", "error", replaceErr)
		return fmt.Errorf("failed to replace existing controller state: %w", replaceErr)
	}
	return nil
}

func setCondition(conditions *[]api.Condition, toSet api.Condition) {
	existingCondition := GetCondition(*conditions, toSet.Type)
	if existingCondition == nil {
		toSet.LastTransitionTime = clock.Now()
		*conditions = append(*conditions, toSet)
		return
	}

	if existingCondition.Status != toSet.Status {
		existingCondition.LastTransitionTime = clock.Now()
	}
	existingCondition.Status = toSet.Status
	existingCondition.Reason = toSet.Reason
	existingCondition.Message = toSet.Message
}

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
