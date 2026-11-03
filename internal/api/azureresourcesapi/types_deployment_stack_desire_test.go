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

package azureresourcesapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestDeploymentStackDesireSDKJSONPreservesReadonlyStatus(t *testing.T) {
	const response = `{"id":"/stack","name":"stack","type":"Microsoft.Resources/deploymentStacks","systemData":{"createdBy":"creator"},"properties":{"correlationId":"correlation","deploymentId":"/deployment","duration":"PT1S","provisioningState":"Succeeded","outputs":{"result":{"value":"value"}},"error":{"code":"Error"}}}`
	var stack armdeploymentstacks.DeploymentStack
	require.NoError(t, json.Unmarshal([]byte(response), &stack))
	data, err := json.Marshal(stack)
	require.NoError(t, err)
	require.JSONEq(t, response, string(data), "SDK marshaling must retain readonly response fields")
	status := &DeploymentStackDesireStatus{Stack: &stack}
	require.Equal(t, status, status.DeepCopy(), "readonly response fields must survive deepcopy")
}

func TestDeploymentStackDesireRuntimeAndDeepCopy(t *testing.T) {
	id, err := azcorearm.ParseResourceID("/subscriptions/Sub/resourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/Cluster/azureDeploymentStackDesires/Desire")
	require.NoError(t, err)
	target, err := azcorearm.ParseResourceID("/subscriptions/sub/providers/Microsoft.Resources/deploymentStacks/stack")
	require.NoError(t, err)
	now := metav1.Now()
	desire := &DeploymentStackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, InstanceVersion: 42},
		Spec: DeploymentStackDesireSpec{
			TargetResourceID: target,
			Authentication:   DeploymentStackDesireAuthentication{SMIResourceID: target},
			Stack: &armdeploymentstacks.DeploymentStack{
				Tags: map[string]*string{"tag": ptr.To("value")},
				Properties: &armdeploymentstacks.DeploymentStackProperties{
					Template:                  map[string]any{"resources": []any{map[string]any{"name": "resource"}}},
					Parameters:                map[string]*armdeploymentstacks.DeploymentParameter{"parameter": {Value: map[string]any{"key": "value"}}},
					ActionOnUnmanage:          &armdeploymentstacks.ActionOnUnmanage{Resources: ptr.To(armdeploymentstacks.UnmanageActionResourceModeDelete)},
					BypassStackOutOfSyncError: ptr.To(true),
				},
			},
		},
		Status: DeploymentStackDesireStatus{
			ObservedSpecHash: "hash",
			ObservedAt:       &now,
			Stack: &armdeploymentstacks.DeploymentStack{
				ID:   ptr.To(target.String()),
				Tags: map[string]*string{"tag": ptr.To("value")},
				Properties: &armdeploymentstacks.DeploymentStackProperties{
					CorrelationID:     ptr.To("correlation"),
					ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded),
					Template:          map[string]any{"resources": []any{map[string]any{"name": "resource"}}},
					Outputs:           map[string]any{"output": map[string]any{"value": []any{"result"}}},
					Parameters:        map[string]*armdeploymentstacks.DeploymentParameter{"parameter": {Value: map[string]any{"key": "value"}}},
					Error:             &armdeploymentstacks.ErrorDetail{Code: ptr.To("StackError")},
				},
			},
			Error: &armdeploymentstacks.ErrorDetail{
				Code:           ptr.To("Error"),
				Details:        []*armdeploymentstacks.ErrorDetail{{Message: ptr.To("detail")}},
				AdditionalInfo: []*armdeploymentstacks.ErrorAdditionalInfo{{Info: map[string]any{"nested": []any{"info"}}}},
			},
			Conditions: []metav1.Condition{{Type: DeploymentStackDesireConditionSuccessfullyApplied}},
		},
	}
	metadata, err := meta.Accessor(desire)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(id.String()), metadata.GetName())
	require.Equal(t, "42", metadata.GetResourceVersion())
	key, err := cache.MetaNamespaceKeyFunc(desire)
	require.NoError(t, err)
	require.Equal(t, metadata.GetName(), key)
	require.Equal(t, schema.EmptyObjectKind, desire.GetObjectKind())
	require.Empty(t, (&DeploymentStackDesire{}).GetObjectMeta().GetName())
	copy := desire.DeepCopyObject().(*DeploymentStackDesire)
	require.Equal(t, desire, copy)
	require.NotSame(t, desire.ResourceID.Parent, copy.ResourceID.Parent)
	require.NotSame(t, desire.Spec.TargetResourceID, copy.Spec.TargetResourceID)
	require.NotSame(t, desire.Spec.Authentication.SMIResourceID, copy.Spec.Authentication.SMIResourceID)
	*copy.Spec.Stack.Tags["tag"] = "changed"
	copy.Spec.Stack.Properties.Template.(map[string]any)["resources"].([]any)[0].(map[string]any)["name"] = "changed"
	copy.Spec.Stack.Properties.Parameters["parameter"].Value.(map[string]any)["key"] = "changed"
	*copy.Spec.Stack.Properties.ActionOnUnmanage.Resources = armdeploymentstacks.UnmanageActionResourceModeDetach
	*copy.Spec.Stack.Properties.BypassStackOutOfSyncError = false
	*copy.Status.Stack.ID = "changed"
	*copy.Status.Stack.Tags["tag"] = "changed"
	*copy.Status.Stack.Properties.CorrelationID = "changed"
	*copy.Status.Stack.Properties.ProvisioningState = armdeploymentstacks.DeploymentStackProvisioningStateFailed
	copy.Status.Stack.Properties.Template.(map[string]any)["resources"].([]any)[0].(map[string]any)["name"] = "changed"
	copy.Status.Stack.Properties.Outputs.(map[string]any)["output"].(map[string]any)["value"].([]any)[0] = "changed"
	copy.Status.Stack.Properties.Parameters["parameter"].Value.(map[string]any)["key"] = "changed"
	*copy.Status.Stack.Properties.Error.Code = "changed"
	*copy.Status.Error.Details[0].Message = "changed"
	copy.Status.Error.AdditionalInfo[0].Info.(map[string]any)["nested"].([]any)[0] = "changed"
	copy.Status.ObservedAt.Time = time.Time{}
	copy.Status.Conditions[0].Type = "Changed"
	require.Equal(t, "value", *desire.Spec.Stack.Tags["tag"])
	require.Equal(t, "resource", desire.Spec.Stack.Properties.Template.(map[string]any)["resources"].([]any)[0].(map[string]any)["name"])
	require.Equal(t, "value", desire.Spec.Stack.Properties.Parameters["parameter"].Value.(map[string]any)["key"])
	require.Equal(t, armdeploymentstacks.UnmanageActionResourceModeDelete, *desire.Spec.Stack.Properties.ActionOnUnmanage.Resources)
	require.True(t, *desire.Spec.Stack.Properties.BypassStackOutOfSyncError)
	require.Equal(t, target.String(), *desire.Status.Stack.ID)
	require.Equal(t, "value", *desire.Status.Stack.Tags["tag"])
	require.Equal(t, "correlation", *desire.Status.Stack.Properties.CorrelationID)
	require.Equal(t, armdeploymentstacks.DeploymentStackProvisioningStateSucceeded, *desire.Status.Stack.Properties.ProvisioningState)
	require.Equal(t, "resource", desire.Status.Stack.Properties.Template.(map[string]any)["resources"].([]any)[0].(map[string]any)["name"])
	require.Equal(t, "result", desire.Status.Stack.Properties.Outputs.(map[string]any)["output"].(map[string]any)["value"].([]any)[0])
	require.Equal(t, "value", desire.Status.Stack.Properties.Parameters["parameter"].Value.(map[string]any)["key"])
	require.Equal(t, "StackError", *desire.Status.Stack.Properties.Error.Code)
	require.Equal(t, "detail", *desire.Status.Error.Details[0].Message)
	require.Equal(t, "info", desire.Status.Error.AdditionalInfo[0].Info.(map[string]any)["nested"].([]any)[0])
	require.False(t, desire.Status.ObservedAt.IsZero())
	require.Equal(t, DeploymentStackDesireConditionSuccessfullyApplied, desire.Status.Conditions[0].Type)
	list := &DeploymentStackDesireList{Items: []DeploymentStackDesire{*desire}}
	var object runtime.Object = list
	items, err := meta.ExtractList(object.DeepCopyObject())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, desire, items[0])
	require.NotSame(t, desire.Spec.TargetResourceID, items[0].(*DeploymentStackDesire).Spec.TargetResourceID)
	require.Equal(t, &DeploymentStackDesireStatus{}, (&DeploymentStackDesireStatus{}).DeepCopy(), "absent outcomes must remain nil")
	require.Equal(t, &DeploymentStackDesireSpec{}, (&DeploymentStackDesireSpec{}).DeepCopy(), "absent spec fields must remain nil")
	require.Panics(t, func() {
		(&DeploymentStackDesireSpec{Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Template: make(chan int)}}}).DeepCopy()
	}, "non-JSON SDK data cannot be persisted or deep-copied")
	require.Panics(t, func() {
		(&DeploymentStackDesireStatus{Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Template: make(chan int)}}}).DeepCopy()
	}, "non-JSON SDK data cannot be persisted or deep-copied")
}
