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
	"runtime/debug"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

type Controller interface {
	SyncOnce(ctx context.Context, keyObj any) error
	Run(ctx context.Context, threadiness int)
}

type LoggableKey interface {
	AddLoggerValues(logger logr.Logger) logr.Logger
}

func AddLoggerValues(logger logr.Logger, key any) logr.Logger {
	switch castKey := key.(type) {
	case LoggableKey:
		return castKey.AddLoggerValues(logger)
	default:
		logger = logger.WithValues("controllerKey", key)
		return logger
	}
}

// OperationKey is for driving workqueues keyed for operations
type OperationKey struct {
	SubscriptionID   string `json:"subscriptionID"`
	OperationName    string `json:"operationName"`
	ParentResourceID string `json:"parentResourceID"`
}

func (k OperationKey) GetParentResourceID() *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(k.ParentResourceID))
}

func (k OperationKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetParentResourceID()).
			AddOperationID(k.OperationName)...)
}

func (k OperationKey) InitialController(controllerName string) *api.Controller {
	// TODO, this structure only allows one status per operation controller even if there are multiple instances of the operation
	// TODO, this may or may not age well. Nesting is possible or we could actually separate controllers that way (probably useful).
	// TODO, leaving this as a thing open to change in the future.
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetParentResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ExternalID: k.GetParentResourceID(),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// HCPClusterKey is for driving workqueues keyed for clusters
type HCPClusterKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
}

func (k HCPClusterKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(api.ToClusterResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName))
}

func (k HCPClusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k HCPClusterKey) InitialController(controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ExternalID: k.GetResourceID(),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// HCPNodePoolKey is for driving workqueus keyed for nodepools
type HCPNodePoolKey struct {
	SubscriptionID    string `json:"subscriptionID"`
	ResourceGroupName string `json:"resourceGroupName"`
	HCPClusterName    string `json:"hcpClusterName"`
	HCPNodePoolName   string `json:"hcpNodePoolName"`
}

func (k HCPNodePoolKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(api.ToNodePoolResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName, k.HCPNodePoolName))
}

func (k HCPNodePoolKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k HCPNodePoolKey) InitialController(controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ExternalID: k.GetResourceID(),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// SubscriptionKey is for driving workqueues keyed for subscriptions
type SubscriptionKey struct {
	SubscriptionID string `json:"subscriptionID"`
}

func (k SubscriptionKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(arm.ToSubscriptionResourceID(k.SubscriptionID))
}

func (k SubscriptionKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

type HCPExternalAuthKey struct {
	SubscriptionID      string `json:"subscriptionID"`
	ResourceGroupName   string `json:"resourceGroupName"`
	HCPClusterName      string `json:"hcpClusterName"`
	HCPExternalAuthName string `json:"hcpExternalAuthName"`
}

func (k *HCPExternalAuthKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(api.ToExternalAuthResourceID(k.SubscriptionID, k.ResourceGroupName, k.HCPClusterName, k.HCPExternalAuthName))
}

func (k *HCPExternalAuthKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k *HCPExternalAuthKey) InitialController(controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ExternalID: k.GetResourceID(),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// controllerMutationFunc is called when trying to write a controller. It gives a spot for computation of a value.
// It should only perform short calls, not long lookups.  It must not fail. Think of it as a way to write information
// that you have already precomputed.
type controllerMutationFunc func(controller *api.Controller)

func ReportSyncError(syncErr error) controllerMutationFunc {
	return func(controller *api.Controller) {
		if syncErr == nil {
			meta.SetStatusCondition(&controller.Status.Conditions, metav1.Condition{
				Type:    "Degraded",
				Status:  metav1.ConditionFalse,
				Reason:  "NoErrors",
				Message: "As expected.",
			})
			return
		}

		meta.SetStatusCondition(&controller.Status.Conditions, metav1.Condition{
			Type:    "Degraded",
			Status:  metav1.ConditionTrue,
			Reason:  "Failed",
			Message: fmt.Sprintf("Had an error while syncing: %s", syncErr.Error()),
		})
	}
}

// InitialControllerFunc builds a new api.Controller for the given logical controller name
// (for example HCPClusterKey.InitialController).
type InitialControllerFunc func(controllerName string) *api.Controller

func DegradedControllerPanicHandler(ctx context.Context, controllerCRUD database.ResourceCRUD[api.Controller], controllerName string, initialControllerFn InitialControllerFunc) func(interface{}) {
	return func(panicVal interface{}) {
		stack := debug.Stack()
		err := WriteController(ctx, controllerCRUD, controllerName, initialControllerFn, ReportSyncError(fmt.Errorf("panic caught:\n%v\n\n%s", panicVal, stack)))
		if err != nil {
			logger := utils.LoggerFromContext(ctx)
			logger.Error(err, "failed to write controller after panic")
		}
	}
}

func controllerCRUDForParent(resourcesDBClient database.ResourcesDBClient, parentResourceID *azcorearm.ResourceID) (database.ResourceCRUD[api.Controller], error) {
	subscriptionID := parentResourceID.SubscriptionID
	resourceGroupName := parentResourceID.ResourceGroupName
	hcp := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)

	switch {
	case armhelpers.ResourceTypeEqual(parentResourceID.ResourceType, api.ClusterResourceType):
		return hcp.Controllers(parentResourceID.Name), nil
	case armhelpers.ResourceTypeEqual(parentResourceID.ResourceType, api.NodePoolResourceType):
		if parentResourceID.Parent == nil {
			return nil, fmt.Errorf("node pool resource ID is missing parent cluster ID")
		}
		clusterName := parentResourceID.Parent.Name
		return hcp.NodePools(clusterName).Controllers(parentResourceID.Name), nil
	case armhelpers.ResourceTypeEqual(parentResourceID.ResourceType, api.ExternalAuthResourceType):
		if parentResourceID.Parent == nil {
			return nil, fmt.Errorf("external auth resource ID is missing parent cluster ID")
		}
		clusterName := parentResourceID.Parent.Name
		return hcp.ExternalAuth(clusterName).Controllers(parentResourceID.Name), nil
	default:
		return nil, fmt.Errorf("unsupported parent resource type for controllers: %s", parentResourceID.ResourceType)
	}
}

// getOrCreateControllerDocument returns the controller document from Cosmos, creating it with
// initialControllerFn if missing. On create conflict (HTTP 409), it re-reads and returns the
// existing document (same pattern as database.GetOrCreateServiceProviderCluster).
func getOrCreateControllerDocument(
	ctx context.Context,
	controllerCRUD database.ResourceCRUD[api.Controller],
	controllerName string,
	initialControllerFn InitialControllerFunc,
) (*api.Controller, error) {
	if initialControllerFn == nil {
		return nil, fmt.Errorf("initialControllerFn is required")
	}

	existingController, err := controllerCRUD.Get(ctx, controllerName)
	if err == nil && existingController != nil {
		return existingController, nil
	}
	if err == nil {
		err = database.NewNotFoundError()
	}

	if !database.IsNotFoundError(err) {
		return nil, fmt.Errorf("failed to get existing controller state: %w", err)
	}

	existingController, err = controllerCRUD.Create(ctx, initialControllerFn(controllerName), nil)
	if err == nil {
		if existingController == nil {
			return nil, fmt.Errorf("create returned success with nil controller")
		}
		return existingController, nil
	}

	if !database.IsConflictError(err) {
		return nil, fmt.Errorf("failed to create existing controller state: %w", err)
	}

	existingController, err = controllerCRUD.Get(ctx, controllerName)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing controller state after create conflict: %w", err)
	}
	if existingController == nil {
		return nil, fmt.Errorf("failed to get existing controller state after create conflict: document missing")
	}

	return existingController, nil
}

// GetOrCreateController gets the named Controller document under the given parent resource
// (cluster, node pool, or external auth). If it does not exist, it creates one using initialControllerFn.
// On create conflict (HTTP 409), it re-reads and returns the existing document (same pattern as
// database.GetOrCreateServiceProviderCluster).
func GetOrCreateController(
	ctx context.Context, resourcesDBClient database.ResourcesDBClient, parentResourceID *azcorearm.ResourceID,
	controllerName string, initialControllerFn InitialControllerFunc,
) (*api.Controller, error) {
	controllerCRUD, err := controllerCRUDForParent(resourcesDBClient, parentResourceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	doc, err := getOrCreateControllerDocument(ctx, controllerCRUD, controllerName, initialControllerFn)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return doc, nil
}

// WriteController will read the existing value, call the mutations in order, then write the result.  It only tries *once*.
// If it fails, then the an error is returned.  This detail is important, it doesn't even retry conflicts.  This is so that
// if a failure happens the control-loop will re-run and restablish the information it was trying to write as valid.
// This prevents accidental recreation of controller instances in cosmos during a delete.
func WriteController(ctx context.Context, controllerCRUD database.ResourceCRUD[api.Controller], controllerName string, initialControllerFn InitialControllerFunc, mutationFns ...controllerMutationFunc) error {
	logger := utils.LoggerFromContext(ctx)

	existingController, err := getOrCreateControllerDocument(ctx, controllerCRUD, controllerName, initialControllerFn)
	if err != nil {
		return err
	}

	desiredController := existingController.DeepCopy()
	for _, mutationFn := range mutationFns {
		mutationFn(desiredController)
	}

	if equality.Semantic.DeepEqual(existingController, desiredController) {
		// nothing to report.
		return nil
	}

	_, replaceErr := controllerCRUD.Replace(ctx, desiredController, nil)
	if replaceErr != nil {
		logger.Error(replaceErr, "failed to replace")
		return fmt.Errorf("failed to replace existing controller state: %w", replaceErr)
	}
	return nil
}
