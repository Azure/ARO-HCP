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

package databasemutationhelpers

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

type ResourceCRUDTestSpecializer[InternalAPIType any] interface {
	ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[InternalAPIType]
	InstanceEquals(expected, actual *InternalAPIType) bool
	NameFromInstance(*InternalAPIType) string
	WriteCosmosID(newObj, oldObj *InternalAPIType)
}

type ControllerCRUDSpecializer struct {
}

var _ ResourceCRUDTestSpecializer[api.Controller] = &ControllerCRUDSpecializer{}

func (ControllerCRUDSpecializer) ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[api.Controller] {
	parentResourceID, err := azcorearm.ParseResourceID(key.ParentResourceID)
	require.NoError(t, err)
	controllerResourceType, err := azcorearm.ParseResourceType(filepath.Join(parentResourceID.ResourceType.String(), api.ControllerResourceTypeName))
	require.NoError(t, err)

	return database.NewControllerCRUD(cosmosContainer, parentResourceID, controllerResourceType)
}

func (ControllerCRUDSpecializer) InstanceEquals(expected, actual *api.Controller) bool {
	// clear the fields that don't compare
	shallowExpected := *expected
	shallowActual := *actual
	shallowExpected.CosmosUID = ""
	shallowActual.CosmosUID = ""
	return equality.Semantic.DeepEqual(shallowExpected, shallowActual)
}

func (ControllerCRUDSpecializer) NameFromInstance(obj *api.Controller) string {
	return obj.ControllerName
}

func (ControllerCRUDSpecializer) WriteCosmosID(newObj, oldObj *api.Controller) {
	newObj.CosmosUID = oldObj.CosmosUID
}

type OperationCRUDSpecializer struct {
}

var _ ResourceCRUDTestSpecializer[api.Operation] = &OperationCRUDSpecializer{}

func (OperationCRUDSpecializer) ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[api.Operation] {
	parentResourceID, err := azcorearm.ParseResourceID(key.ParentResourceID)
	require.NoError(t, err)

	return database.NewCosmosResourceCRUD[api.Operation, database.Operation](cosmosContainer, parentResourceID, api.OperationStatusResourceType)
}

func (OperationCRUDSpecializer) InstanceEquals(expected, actual *api.Operation) bool {
	// clear the fields that don't compare
	shallowExpected := *expected
	shallowActual := *actual
	return equality.Semantic.DeepEqual(shallowExpected, shallowActual)
}

func (OperationCRUDSpecializer) NameFromInstance(obj *api.Operation) string {
	return obj.OperationID.Name
}

func (OperationCRUDSpecializer) WriteCosmosID(newObj, oldObj *api.Operation) {
	// the cosmosID is derived from the operationID
}

type UntypedCRUDSpecializer struct {
}

var _ ResourceCRUDTestSpecializer[database.TypedDocument] = &UntypedCRUDSpecializer{}

func (UntypedCRUDSpecializer) ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[database.TypedDocument] {
	panic("unsupported")
}

func (UntypedCRUDSpecializer) InstanceEquals(expected, actual *database.TypedDocument) bool {
	// clear the fields that don't compare
	shallowExpected := *expected
	shallowActual := *actual
	shallowExpected.ID = ""
	shallowExpected.CosmosResourceID = ""
	shallowExpected.CosmosSelf = ""
	shallowExpected.CosmosETag = ""
	shallowExpected.CosmosAttachments = ""
	shallowExpected.CosmosTimestamp = 0
	shallowActual.ID = ""
	shallowActual.CosmosResourceID = ""
	shallowActual.CosmosSelf = ""
	shallowActual.CosmosETag = ""
	shallowActual.CosmosAttachments = ""
	shallowActual.CosmosTimestamp = 0

	expectedProperties := map[string]any{}
	actualProperties := map[string]any{}
	if err := json.Unmarshal(shallowExpected.Properties, &expectedProperties); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(shallowActual.Properties, &actualProperties); err != nil {
		panic(err)
	}
	shallowExpected.Properties = nil
	shallowActual.Properties = nil

	if !equality.Semantic.DeepEqual(shallowExpected, shallowActual) {
		return false
	}

	// clear some per-type details
	switch strings.ToLower(actual.ResourceType) {
	case strings.ToLower(api.ClusterControllerResourceType.String()),
		strings.ToLower(api.NodePoolControllerResourceType.String()),
		strings.ToLower(api.ExternalAuthControllerResourceType.String()):

		expectedConditions, found, err := unstructured.NestedSlice(expectedProperties, "internalState", "status", "conditions")
		if found && err == nil {
			for i := range expectedConditions {
				delete(expectedConditions[i].(map[string]any), "lastTransitionTime")
			}
			if err := unstructured.SetNestedSlice(expectedProperties, expectedConditions, "internalState", "status", "conditions"); err != nil {
				panic(err)
			}
		}

		actualConditions, found, err := unstructured.NestedSlice(actualProperties, "internalState", "status", "conditions")
		if found && err == nil {
			for i := range actualConditions {
				delete(actualConditions[i].(map[string]any), "lastTransitionTime")
			}
			if err := unstructured.SetNestedSlice(actualProperties, actualConditions, "internalState", "status", "conditions"); err != nil {
				panic(err)
			}
		}
	}

	return equality.Semantic.DeepEqual(expectedProperties, actualProperties)
}

func (UntypedCRUDSpecializer) NameFromInstance(obj *database.TypedDocument) string {
	return obj.ID
}

func (UntypedCRUDSpecializer) WriteCosmosID(newObj, oldObj *database.TypedDocument) {
	newObj.ID = oldObj.ID
}

type SubscriptionCRUDSpecializer struct {
}

var _ ResourceCRUDTestSpecializer[arm.Subscription] = &SubscriptionCRUDSpecializer{}

func (SubscriptionCRUDSpecializer) ResourceCRUDFromKey(t *testing.T, cosmosContainer *azcosmos.ContainerClient, key CosmosCRUDKey) database.ResourceCRUD[arm.Subscription] {
	return database.NewSubscriptionCRUD(cosmosContainer)
}

func (SubscriptionCRUDSpecializer) InstanceEquals(expected, actual *arm.Subscription) bool {
	// clear the fields that don't compare
	shallowExpected := *expected
	shallowActual := *actual
	shallowExpected.LastUpdated = 0
	shallowActual.LastUpdated = 0
	return equality.Semantic.DeepEqual(shallowExpected, shallowActual)
}

func (SubscriptionCRUDSpecializer) NameFromInstance(obj *arm.Subscription) string {
	return obj.ResourceID.Name
}

func (SubscriptionCRUDSpecializer) WriteCosmosID(newObj, oldObj *arm.Subscription) {
	newObj.ResourceID = oldObj.ResourceID
}
