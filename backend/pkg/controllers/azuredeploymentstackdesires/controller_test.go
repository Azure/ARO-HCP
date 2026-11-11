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

package azuredeploymentstackdesires

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
)

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const testParentResourceID = "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster"

var testKey = Key{ParentResourceID: testParentResourceID, Name: "desire"}

func newDesire() *azureresourcesapi.DeploymentStackDesire {
	target, _ := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Resources/deploymentStacks/stack")
	return &azureresourcesapi.DeploymentStackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{CosmosETag: "original"},
		Spec: azureresourcesapi.DeploymentStackDesireSpec{
			TargetResourceID: target,
			Authentication: azureresourcesapi.DeploymentStackDesireAuthentication{
				Type:                  azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA,
				FirstPartyApplication: &azureresourcesapi.DeploymentStackDesireAuthenticationFirstPartyApplication{TenantID: "tenant"},
			},
			Action: azureresourcesapi.DeploymentStackDesireActionTypeApply,
			Stack:  &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Template: map[string]any{"unknown": float64(123)}}},
		}}
}

func observedDeploymentStack(state, correlation string) *armdeploymentstacks.DeploymentStack {
	return &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{
		ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningState(state)), CorrelationID: ptr.To(correlation),
	}}
}

func withApplySubmission(t *testing.T, desire *azureresourcesapi.DeploymentStackDesire, stack *armdeploymentstacks.DeploymentStack, now time.Time) *azureresourcesapi.DeploymentStackDesire {
	t.Helper()
	hash, hashErr := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	status, submissionErr := deploymentStackDesireStatusForAssociatedApply(hash, stack)
	if status == nil {
		t.Fatalf("expected submission status: %v", submissionErr)
	}
	updated := desire.DeepCopy()
	updated.Status = *normalizeStatusTimes(desire.Status, *status, now).DeepCopy()
	updated.Status.ObservedAt = ptr.To(metav1.NewTime(now))
	return updated
}

func checkedDecide(t *testing.T, desire *azureresourcesapi.DeploymentStackDesire, stack *armdeploymentstacks.DeploymentStack, getErr error, now time.Time, wantCalls int) decision {
	t.Helper()
	before := desire.DeepCopy()
	stackBefore, err := json.Marshal(stack)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result := decideDeploymentStackDesire(context.Background(), desire, now, func() (*armdeploymentstacks.DeploymentStack, error) { calls++; return stack, getErr })
	result.validate()
	stackAfter, err := json.Marshal(stack)
	if err != nil {
		t.Fatal(err)
	}
	if calls != wantCalls || !reflect.DeepEqual(before, desire) || string(stackBefore) != string(stackAfter) {
		t.Fatalf("GET calls = %d, want %d, or input mutated", calls, wantCalls)
	}
	if result.status != nil && cmp.Equal(*result.status, desire.Status) {
		t.Fatal("unchanged observation must not be written")
	}
	return result
}

func TestDecisionValidate(t *testing.T) {
	status := &azureresourcesapi.DeploymentStackDesireStatus{}
	retryErr := errors.New("retry")
	for _, test := range []struct {
		name    string
		result  decision
		invalid bool
	}{
		{name: "zero", invalid: true},
		{name: "no op", result: decision{kind: decisionKindNoOp}},
		{name: "status", result: decision{kind: decisionKindUpdateDesireStatus, status: status}},
		{name: "status and error", result: decision{kind: decisionKindUpdateDesireStatus, status: status, err: retryErr}},
		{name: "apply", result: decision{kind: decisionKindApplyStack}},
		{name: "delete", result: decision{kind: decisionKindDeleteStack}},
		{name: "poll", result: decision{kind: decisionKindNoOp, requeue: pendingInterval}},
		{name: "error", result: decision{kind: decisionKindNoOp, err: retryErr}},
		{name: "poll without decision kind", result: decision{requeue: pendingInterval}, invalid: true},
		{name: "error without decision kind", result: decision{err: retryErr}, invalid: true},
		{name: "no op with status", result: decision{kind: decisionKindNoOp, status: status}, invalid: true},
		{name: "missing status", result: decision{kind: decisionKindUpdateDesireStatus}, invalid: true},
		{name: "missing decision kind", result: decision{status: status}, invalid: true},
		{name: "status with poll", result: decision{kind: decisionKindUpdateDesireStatus, status: status, requeue: pendingInterval}, invalid: true},
		{name: "status with apply", result: decision{kind: decisionKindApplyStack, status: status}, invalid: true},
		{name: "status with delete", result: decision{kind: decisionKindDeleteStack, status: status}, invalid: true},
		{name: "apply with error", result: decision{kind: decisionKindApplyStack, err: retryErr}, invalid: true},
		{name: "delete with error", result: decision{kind: decisionKindDeleteStack, err: retryErr}, invalid: true},
		{name: "apply with poll", result: decision{kind: decisionKindApplyStack, requeue: pendingInterval}, invalid: true},
		{name: "delete with poll", result: decision{kind: decisionKindDeleteStack, requeue: pendingInterval}, invalid: true},
		{name: "error with poll", result: decision{kind: decisionKindNoOp, err: retryErr, requeue: pendingInterval}, invalid: true},
		{name: "negative poll", result: decision{kind: decisionKindNoOp, requeue: -time.Second}, invalid: true},
		{name: "unknown decision kind", result: decision{kind: "ObserveStack"}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if panicked := recover() != nil; panicked != test.invalid {
					t.Fatalf("validate panicked = %v, want %v", panicked, test.invalid)
				}
			}()
			test.result.validate()
		})
	}
}

func TestDecideDeploymentStackDesire(t *testing.T) {
	initialTime := metav1.NewTime(testNow)
	now := testNow.Add(time.Minute)
	observationTime := metav1.NewTime(now)
	apply := newDesire()
	applyHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(apply.Spec)
	if err != nil {
		t.Fatal(err)
	}
	deleteDesire := newDesire()
	deleteDesire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	deleteHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(deleteDesire.Spec)
	if err != nil {
		t.Fatal(err)
	}
	pending := apply.DeepCopy()
	pending.Status = azureresourcesapi.DeploymentStackDesireStatus{
		Stack: observedDeploymentStack("deploying", "ours"), ObservedSpecHash: applyHash, ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
		},
	}
	succeeded := apply.DeepCopy()
	succeeded.Status = azureresourcesapi.DeploymentStackDesireStatus{
		Stack: observedDeploymentStack("succeeded", "ours"), ObservedSpecHash: applyHash, ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue, Reason: "Succeeded", Message: "Azure completed the stack operation", LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
		},
	}
	failed := apply.DeepCopy()
	failed.Status = azureresourcesapi.DeploymentStackDesireStatus{
		Stack: observedDeploymentStack("failed", "ours"), ObservedSpecHash: applyHash, ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: initialTime},
		},
	}
	invalidStack := observedDeploymentStack("failed", "ours")
	invalidStack.Properties.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidTemplate")}
	invalid := apply.DeepCopy()
	invalid.Status = azureresourcesapi.DeploymentStackDesireStatus{
		Stack: invalidStack, ObservedSpecHash: applyHash, ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: `{"code":"InvalidTemplate"}`, LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: `{"code":"InvalidTemplate"}`, LastTransitionTime: initialTime},
		},
	}
	observationFailed := apply.DeepCopy()
	observationFailed.Status = azureresourcesapi.DeploymentStackDesireStatus{
		Stack: observedDeploymentStack("deploying", "ours"), ObservedSpecHash: applyHash, ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "RequestFailed", Message: "context deadline exceeded", LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "RequestFailed", Message: "context deadline exceeded", LastTransitionTime: initialTime},
		},
	}
	supersededPending := pending.DeepCopy()
	supersededPending.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	supersededSuccess := succeeded.DeepCopy()
	supersededSuccess.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	supersededFailure := failed.DeepCopy()
	supersededFailure.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	unknownPending := apply.DeepCopy()
	unknownPending.Status = azureresourcesapi.DeploymentStackDesireStatus{
		Stack: observedDeploymentStack("deploying", "external"), ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
		},
	}
	deleted := deleteDesire.DeepCopy()
	deleted.Status = azureresourcesapi.DeploymentStackDesireStatus{
		ObservedSpecHash: deleteHash, ObservedAt: &initialTime,
		Conditions: []metav1.Condition{
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue, Reason: "StackAbsent", Message: "The deployment stack is absent", LastTransitionTime: initialTime},
			{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
		},
	}

	for _, test := range []struct {
		name      string
		desire    *azureresourcesapi.DeploymentStackDesire
		stack     *armdeploymentstacks.DeploymentStack
		getErr    error
		now       time.Time
		want      decision
		wantCalls int
	}{
		{
			name: "absent Apply submits immediately", desire: apply, now: now, wantCalls: 1,
			want: decision{kind: decisionKindApplyStack},
		},
		{
			name: "absent Delete succeeds", desire: deleteDesire, now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				ObservedSpecHash: deleteHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue, Reason: "StackAbsent", Message: "The deployment stack is absent", LastTransitionTime: observationTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "known Apply pending unchanged polls", desire: pending, stack: observedDeploymentStack("deploying", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindNoOp, requeue: 30 * time.Second},
		},
		{
			name: "known Apply new pending observation", desire: pending, stack: observedDeploymentStack("initializing", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("initializing", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "known Apply succeeds", desire: pending, stack: observedDeploymentStack("succeeded", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("succeeded", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue, Reason: "Succeeded", Message: "Azure completed the stack operation", LastTransitionTime: observationTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "associated unknown state remains pending", desire: pending, stack: observedDeploymentStack("unknown", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("unknown", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "associated empty state remains pending", desire: pending, stack: observedDeploymentStack("", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "known Apply fails retryably", desire: pending, stack: observedDeploymentStack("failed", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, err: errExecutionFailed, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("failed", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "known Apply canceled is retryable", desire: pending, stack: observedDeploymentStack("canceled", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, err: errExecutionFailed, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("canceled", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "known Apply narrow invalid intent", desire: pending, stack: invalidStack, now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: invalidStack, ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: `{"code":"InvalidTemplate"}`, LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: `{"code":"InvalidTemplate"}`, LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "recorded failure retries immediately", desire: failed, stack: observedDeploymentStack("failed", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindApplyStack},
		},
		{
			name: "superseded Apply pending still polls", desire: supersededPending, stack: observedDeploymentStack("deploying", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindNoOp, requeue: 30 * time.Second},
		},
		{
			name: "superseded Apply pending preserves old intent", desire: supersededPending, stack: observedDeploymentStack("deletingResources", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("deletingResources", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "superseded Apply success does not prove Delete", desire: supersededPending, stack: observedDeploymentStack("succeeded", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("succeeded", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue, Reason: "Succeeded", Message: "Azure completed the stack operation", LastTransitionTime: observationTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "superseded Apply failure suppresses old error", desire: supersededPending, stack: observedDeploymentStack("failed", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("failed", "ours"), ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "ExecutionFailed", Message: "Azure stack operation failed without error details", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "changed intent bypasses successful Apply cooldown", desire: supersededSuccess, stack: observedDeploymentStack("succeeded", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindDeleteStack},
		},
		{
			name: "superseded invalid Apply retains its old hash", desire: supersededPending, stack: invalidStack, now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: invalidStack, ObservedSpecHash: applyHash, ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: `{"code":"InvalidTemplate"}`, LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "InvalidIntent", Message: `{"code":"InvalidTemplate"}`, LastTransitionTime: initialTime},
				},
			}},
		},
		{
			name: "recorded superseded failure submits Delete", desire: supersededFailure, stack: observedDeploymentStack("failed", "ours"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindDeleteStack},
		},
		{
			name: "unknown pending operation gains no hash", desire: apply, stack: observedDeploymentStack("deploying", "external"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("deploying", "external"), ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: observationTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "unknown pending unchanged polls", desire: unknownPending, stack: observedDeploymentStack("deploying", "external"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindNoOp, requeue: 30 * time.Second},
		},
		{
			name: "unrecognized state waits before Delete", desire: deleteDesire, stack: observedDeploymentStack("unknown", "external"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("unknown", "external"), ObservedAt: &observationTime,
				Conditions: []metav1.Condition{
					{Type: "SuccessfullyDeleted", Status: metav1.ConditionFalse, Reason: "WaitingForCompletion", Message: "Waiting for the Azure stack operation", LastTransitionTime: observationTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionFalse, Reason: "AsExpected", Message: "No stack execution error", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "unknown completed operation submits Apply", desire: apply, stack: observedDeploymentStack("succeeded", "external"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindApplyStack},
		},
		{
			name: "unknown canceled operation submits Delete", desire: deleteDesire, stack: observedDeploymentStack("canceled", "external"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindDeleteStack},
		},
		{
			name: "unknown canceled operation submits Apply", desire: apply, stack: observedDeploymentStack("canceled", "external"), now: now, wantCalls: 1,
			want: decision{kind: decisionKindApplyStack},
		},
		{
			name: "success cooldown skips GET", desire: succeeded, getErr: context.DeadlineExceeded, now: testNow.Add(6*time.Hour - time.Nanosecond),
			want: decision{kind: decisionKindNoOp},
		},
		{
			name: "success cooldown expires", desire: succeeded, stack: observedDeploymentStack("succeeded", "ours"), now: testNow.Add(6 * time.Hour), wantCalls: 1,
			want: decision{kind: decisionKindApplyStack},
		},
		{
			name: "invalid intent cooldown skips GET", desire: invalid, getErr: context.DeadlineExceeded, now: testNow.Add(6*time.Hour - time.Nanosecond),
			want: decision{kind: decisionKindNoOp},
		},
		{
			name: "invalid intent cooldown expires", desire: invalid, stack: invalidStack, now: testNow.Add(6 * time.Hour), wantCalls: 1,
			want: decision{kind: decisionKindApplyStack},
		},
		{
			name: "confirmed Delete cooldown skips GET", desire: deleted, getErr: context.DeadlineExceeded, now: now,
			want: decision{kind: decisionKindNoOp},
		},
		{
			name: "confirmed absence unchanged after cooldown", desire: deleted, now: testNow.Add(6 * time.Hour), wantCalls: 1,
			want: decision{kind: decisionKindNoOp},
		},
		{
			name: "GET failure surfaces Degraded and retries", desire: pending, getErr: context.DeadlineExceeded, now: now, wantCalls: 1,
			want: decision{kind: decisionKindUpdateDesireStatus, err: context.DeadlineExceeded, status: &azureresourcesapi.DeploymentStackDesireStatus{
				Stack: observedDeploymentStack("deploying", "ours"), ObservedSpecHash: applyHash, ObservedAt: &initialTime,
				Conditions: []metav1.Condition{
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionFalse, Reason: "RequestFailed", Message: "context deadline exceeded", LastTransitionTime: initialTime},
					{Type: azureresourcesapi.DeploymentStackDesireConditionTypeDegraded, Status: metav1.ConditionTrue, Reason: "RequestFailed", Message: "context deadline exceeded", LastTransitionTime: observationTime},
				},
			}},
		},
		{
			name: "GET failure unchanged Degraded only retries", desire: observationFailed, getErr: context.DeadlineExceeded, now: now, wantCalls: 1,
			want: decision{kind: decisionKindNoOp, err: context.DeadlineExceeded},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := test.desire.DeepCopy()
			stackBefore, err := json.Marshal(test.stack)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			actual := decideDeploymentStackDesire(context.Background(), test.desire, test.now, func() (*armdeploymentstacks.DeploymentStack, error) {
				calls++
				return test.stack, test.getErr
			})
			if calls != test.wantCalls {
				t.Errorf("GET calls = %d, want %d", calls, test.wantCalls)
			}
			if !reflect.DeepEqual(before, test.desire) {
				t.Error("decision mutated input desire")
			}
			stackAfter, err := json.Marshal(test.stack)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(string(stackBefore), string(stackAfter)); diff != "" {
				t.Errorf("decision mutated observed stack (-before +after):\n%s", diff)
			}
			// submissionMessage is solely diagnostic; all behavioral output fields are compared.
			if diff := cmp.Diff(test.want, actual, cmp.AllowUnexported(decision{}), cmpopts.EquateErrors(), cmpopts.IgnoreFields(decision{}, "submissionMessage")); diff != "" {
				t.Errorf("decision (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCooldownAndIdenticalRejections(t *testing.T) {
	for _, kind := range []string{"success", "sync invalid", "async invalid", "delete success", "delete invalid"} {
		for _, interval := range []time.Duration{0, -time.Second, 3 * time.Minute} {
			t.Run(fmt.Sprintf("%s/%s", kind, interval), func(t *testing.T) {
				desire := newDesire()
				desire.Spec.ReconcileInterval.Duration = interval
				if strings.HasPrefix(kind, "delete") {
					desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
				}
				effectiveInterval := interval
				if effectiveInterval <= 0 {
					effectiveInterval = defaultInterval
				}
				now := testNow
				for range 3 {
					hash, _ := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
					stack := observedDeploymentStack("succeeded", "ours")
					var status *azureresourcesapi.DeploymentStackDesireStatus
					var err error
					switch kind {
					case "success":
						status, err = deploymentStackDesireStatusForAssociatedApply(hash, stack)
					case "sync invalid":
						status, err = deploymentStackDesireStatusForRejectedSubmission(hash, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, &azcore.ResponseError{StatusCode: 400, ErrorCode: "InvalidTemplate"})
					case "async invalid":
						desire = withApplySubmission(t, desire, observedDeploymentStack("deploying", "ours"), now)
						stack = observedDeploymentStack("failed", "ours")
						stack.Properties.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidRequestContent")}
						result := checkedDecide(t, desire, stack, nil, now, 1)
						status, err = result.status, result.err
					case "delete invalid":
						status, err = deploymentStackDesireStatusForDeleteSubmission(hash, &azcore.ResponseError{StatusCode: 400, ErrorCode: "InvalidRequestContent"})
					case "delete success":
						stack = nil
						desire.Status = azureresourcesapi.DeploymentStackDesireStatus{}
						result := checkedDecide(t, desire, nil, nil, now, 1)
						status, err = result.status, result.err
					}
					if status == nil || err != nil {
						t.Fatalf("status: %+v, error: %v", status, err)
					}
					if kind != "async invalid" && kind != "delete success" {
						status = normalizeStatusTimes(desire.Status, *status, now)
						status.ObservedAt = ptr.To(metav1.NewTime(now))
					}
					if !status.ObservedAt.Time.Equal(now) {
						t.Fatalf("new attempt did not refresh clock: %+v", status)
					}
					desire.Status = *status.DeepCopy()
					for _, elapsed := range []time.Duration{0, time.Second, effectiveInterval - time.Nanosecond} {
						cooling := checkedDecide(t, desire, nil, errors.New("must not GET"), now.Add(elapsed), 0)
						if cooling.kind != decisionKindNoOp || cooling.err != nil || cooling.requeue != 0 {
							t.Fatalf("cooldown: %+v", cooling)
						}
					}
					due := checkedDecide(t, desire, stack, nil, now.Add(effectiveInterval), 1)
					want := decisionKindApplyStack
					switch kind {
					case "delete invalid":
						want = decisionKindDeleteStack
					case "delete success":
						want = decisionKindNoOp
					}
					if due.kind != want {
						t.Fatalf("cooldown not bounded: %+v", due)
					}
					changed := desire.DeepCopy()
					changed.Spec.Stack.Location = ptr.To("changed intent")
					bypass := checkedDecide(t, changed, stack, nil, now.Add(time.Second), 1)
					if bypass.kind == decisionKindNoOp {
						t.Fatal("changed intent did not bypass cooldown")
					}
					now = now.Add(effectiveInterval)
				}
			})
		}
	}
}

func TestChangedIntentAndCorrelation(t *testing.T) {
	for _, completion := range []string{"succeeded", "failed", "canceled"} {
		t.Run(completion, func(t *testing.T) {
			desire := withApplySubmission(t, newDesire(), observedDeploymentStack("deploying", "ours"), testNow)
			oldHash := desire.Status.ObservedSpecHash
			desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
			pending := checkedDecide(t, desire, observedDeploymentStack("deploying", "ours"), nil, testNow.Add(time.Second), 1)
			if pending.requeue != pendingInterval {
				t.Fatalf("changed intent interrupted in-flight Apply: %+v", pending)
			}
			stack := observedDeploymentStack(completion, "ours")
			result := checkedDecide(t, desire, stack, nil, testNow.Add(time.Minute), 1)
			if result.kind != decisionKindUpdateDesireStatus || result.err != nil || result.status.ObservedSpecHash != oldHash ||
				meta.FindStatusCondition(result.status.Conditions, "SuccessfullyDeleted") != nil {
				t.Fatalf("old Apply proved Delete or retried old intent: %+v", result)
			}
			desire.Status = *result.status
			if next := checkedDecide(t, desire, stack, nil, testNow.Add(time.Minute), 1); next.kind != decisionKindDeleteStack {
				t.Fatalf("next reconcile must Delete: %+v", next)
			}
		})
	}
	desire := withApplySubmission(t, newDesire(), observedDeploymentStack("succeeded", "A"), testNow)
	original := desire.Spec.Stack
	desire.Spec.Stack = &armdeploymentstacks.DeploymentStack{Location: ptr.To("B")}
	if result := checkedDecide(t, desire, observedDeploymentStack("succeeded", "A"), nil, testNow.Add(time.Minute), 1); result.kind != decisionKindApplyStack {
		t.Fatalf("B not submitted: %+v", result)
	}
	// B's submission status is lost, then the producer returns to A. Accept the
	// previously recorded A outcome only until the original bounded cooldown ends.
	desire.Spec.Stack = original
	if result := checkedDecide(t, desire, nil, nil, testNow.Add(time.Minute), 0); result.kind != decisionKindNoOp {
		t.Fatalf("bounded A-B-A behavior changed: %+v", result)
	}
	if result := checkedDecide(t, desire, observedDeploymentStack("succeeded", "B"), nil, testNow.Add(defaultInterval), 1); result.kind != decisionKindApplyStack {
		t.Fatalf("lost association must replay immediately when due: %+v", result)
	}
}

func TestPrecheckDoesNotChangeApplyProvenance(t *testing.T) {
	desire := withApplySubmission(t, newDesire(), observedDeploymentStack("deploying", "ours"), testNow)
	originalSpec := desire.Spec
	originalStatus := desire.Status.DeepCopy()
	desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	desire.Spec.Authentication.Type = azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI
	precheck := checkedDecide(t, desire, nil, nil, testNow.Add(time.Second), 0)
	if precheck.status == nil || precheck.status.Conditions[0].Type != "SuccessfullyDeleted" || precheck.status.Conditions[0].Reason != "PreCheckFailed" {
		t.Fatalf("expected invalid Delete precheck: %+v", precheck)
	}
	if precheck.status.ObservedSpecHash != originalStatus.ObservedSpecHash || !cmp.Equal(precheck.status.Stack, originalStatus.Stack) {
		t.Fatal("precheck discarded pending Apply association")
	}
	desire.Status = *precheck.status
	desire.Spec = originalSpec
	completed := checkedDecide(t, desire, observedDeploymentStack("succeeded", "ours"), nil, testNow.Add(time.Minute), 1)
	if completed.status == nil || completed.err != nil || completed.status.ObservedSpecHash != originalStatus.ObservedSpecHash ||
		!meta.IsStatusConditionTrue(completed.status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) || meta.FindStatusCondition(completed.status.Conditions, "SuccessfullyDeleted") != nil {
		t.Fatalf("precheck changed old Apply provenance: %+v", completed)
	}
	desire.Status = *completed.status
	if next := checkedDecide(t, desire, nil, nil, testNow.Add(2*time.Minute), 0); next.kind != decisionKindNoOp {
		t.Fatalf("restored Apply resubmitted instead of cooling down: %+v", next)
	}
}

func TestDeleteToApplyProgression(t *testing.T) {
	desire := newDesire()
	desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	hash, _ := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	accepted, err := deploymentStackDesireStatusForDeleteSubmission(hash, nil)
	if err != nil || accepted.Stack != nil {
		t.Fatalf("Delete acceptance manufactured a correlated stack: %+v, %v", accepted, err)
	}
	desire.Status = *accepted
	deleting := observedDeploymentStack("deletingResources", "delete-body-id")
	observed := checkedDecide(t, desire, deleting, nil, testNow, 1)
	if observed.status == nil || observed.status.ObservedSpecHash != "" {
		t.Fatalf("Delete GET acquired an association: %+v", observed)
	}
	desire.Status = *observed.status
	desire.Spec.Action = "Apply"
	waiting := checkedDecide(t, desire, deleting, nil, testNow.Add(time.Second), 1)
	if waiting.status == nil || waiting.status.ObservedSpecHash != "" || waiting.status.Conditions[0].Type != azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied || waiting.status.Conditions[0].Status != metav1.ConditionFalse {
		t.Fatalf("changed intent failed to wait for deletion: %+v", waiting)
	}
	desire.Status = *waiting.status
	if poll := checkedDecide(t, desire, deleting, nil, testNow.Add(2*time.Second), 1); poll.requeue != pendingInterval || poll.kind != decisionKindNoOp {
		t.Fatalf("Apply interrupted pending Delete: %+v", poll)
	}
	if absent := checkedDecide(t, desire, nil, nil, testNow.Add(time.Minute), 1); absent.kind != decisionKindApplyStack {
		t.Fatalf("Apply must create immediately after deletion: %+v", absent)
	}
	// A failed unassociated Delete does not establish an Apply outcome or an
	// invalid-intent cooldown, even if Azure reports a narrow template error.
	failed := observedDeploymentStack("failed", "delete-body-id")
	failed.Properties.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidTemplate")}
	if retry := checkedDecide(t, desire, failed, nil, testNow.Add(time.Minute), 1); retry.kind != decisionKindApplyStack || retry.err != nil {
		t.Fatalf("failed Delete must retry current Apply intent: %+v", retry)
	}
	desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	if retry := checkedDecide(t, desire, failed, nil, testNow.Add(time.Minute), 1); retry.kind != decisionKindDeleteStack || retry.err != nil {
		t.Fatalf("failed Delete must retry current Delete intent: %+v", retry)
	}
}

func TestAssociationAndCooldownPrerequisites(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*azureresourcesapi.DeploymentStackDesire)
		stack  *armdeploymentstacks.DeploymentStack
		want   decisionKind
	}{
		{name: "different body correlation", stack: observedDeploymentStack("succeeded", "external"), want: decisionKindApplyStack},
		{name: "missing body correlation", stack: observedDeploymentStack("succeeded", ""), want: decisionKindApplyStack},
		{name: "missing recorded hash", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Status.ObservedSpecHash = "" }, stack: observedDeploymentStack("succeeded", "ours"), want: decisionKindApplyStack},
		{name: "missing recorded stack", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Status.Stack = nil }, stack: observedDeploymentStack("succeeded", "ours"), want: decisionKindApplyStack},
	} {
		t.Run(test.name, func(t *testing.T) {
			desire := withApplySubmission(t, newDesire(), observedDeploymentStack("deploying", "ours"), testNow)
			if test.change != nil {
				test.change(desire)
			}
			result := checkedDecide(t, desire, test.stack, nil, testNow.Add(time.Second), 1)
			if result.kind != test.want {
				t.Fatalf("association decision: %+v", result)
			}
		})
	}
	for _, test := range []struct {
		name   string
		change func(*azureresourcesapi.DeploymentStackDesire)
	}{
		{name: "wrong action condition", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Status.Conditions[0].Type = "SuccessfullyDeleted"
		}},
		{name: "wrong hash", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Status.ObservedSpecHash = "older intent" }},
		{name: "missing attempt time", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Status.ObservedAt = nil }},
		{name: "missing action condition", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Status.Conditions = desire.Status.Conditions[1:]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			desire := withApplySubmission(t, newDesire(), observedDeploymentStack("succeeded", "ours"), testNow)
			test.change(desire)
			result := checkedDecide(t, desire, nil, context.DeadlineExceeded, testNow.Add(time.Second), 1)
			if !errors.Is(result.err, context.DeadlineExceeded) {
				t.Fatalf("GET error must propagate so the workqueue retries: %+v", result)
			}
			if result.status == nil {
				t.Fatalf("GET error must surface a status: %+v", result)
			}
			degraded := meta.FindStatusCondition(result.status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeDegraded)
			if degraded == nil || degraded.Status != metav1.ConditionTrue || degraded.Reason != "RequestFailed" {
				t.Fatalf("GET error must surface a Degraded condition: %+v", result)
			}
		})
	}
}

func TestLocalValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*azureresourcesapi.DeploymentStackDesire)
		message string
	}{
		{name: "nil target", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Spec.TargetResourceID = nil }, message: "stack target"},
		{name: "wrong target", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
			desire.Spec.TargetResourceID, _ = azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/stack")
		}, message: "Microsoft.Resources/deploymentStacks"},
		{name: "unsupported scope", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.TargetResourceID, _ = azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm/providers/Microsoft.Resources/deploymentStacks/stack")
		}, message: "unsupported stack scope"},
		{name: "action", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Spec.Action = "unknown" }, message: "unsupported stack action"},
		{name: "authentication", change: func(desire *azureresourcesapi.DeploymentStackDesire) { desire.Spec.Authentication.Type = "unknown" }, message: "unsupported stack authentication"},
		{name: "nil SMI", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{Type: azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI}
		}, message: "SMI authentication requires serviceManagedIdentity"},
		{name: "nil SMI resource ID", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{
				Type:                   azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI,
				ServiceManagedIdentity: &azureresourcesapi.DeploymentStackDesireAuthenticationServiceManagedIdentity{ClusterIdentityURL: "https://identity.example/cluster"},
			}
		}, message: "SMI authentication requires serviceManagedIdentityResourceID"},
		{name: "SMI resource ID wrong type", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			identity, err := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm")
			if err != nil {
				t.Fatal(err)
			}
			desire.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{
				Type:                   azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI,
				ServiceManagedIdentity: &azureresourcesapi.DeploymentStackDesireAuthenticationServiceManagedIdentity{ServiceManagedIdentityResourceID: identity, ClusterIdentityURL: "https://identity.example/cluster"},
			}
		}, message: "SMI authentication requires a serviceManagedIdentityResourceID with userAssignedIdentity type"},
		{name: "nil SMI cluster identity URL", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			identity, err := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi")
			if err != nil {
				t.Fatal(err)
			}
			desire.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{
				Type:                   azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI,
				ServiceManagedIdentity: &azureresourcesapi.DeploymentStackDesireAuthenticationServiceManagedIdentity{ServiceManagedIdentityResourceID: identity},
			}
		}, message: "SMI authentication requires clusterIdentityURL"},
		{name: "nil FPA", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{Type: azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA}
		}, message: "FPA authentication requires firstPartyApplication"},
		{name: "nil FPA tenant", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{
				Type:                  azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA,
				FirstPartyApplication: &azureresourcesapi.DeploymentStackDesireAuthenticationFirstPartyApplication{},
			}
		}, message: "FPA authentication requires tenantID"},
		{name: "unhashable", change: func(desire *azureresourcesapi.DeploymentStackDesire) {
			desire.Spec.Stack.Properties.Template = make(chan int)
		}, message: "marshal deployment stack desire spec"},
	} {
		t.Run(test.name, func(t *testing.T) {
			desire := withApplySubmission(t, newDesire(), observedDeploymentStack("succeeded", "ours"), testNow)
			previous := desire.Status.DeepCopy()
			test.change(desire)
			lazyGET := func() (*armdeploymentstacks.DeploymentStack, error) {
				t.Fatal("local validation reached Azure")
				return nil, nil
			}
			result := decideDeploymentStackDesire(context.Background(), desire, testNow.Add(time.Minute), lazyGET)
			result.validate()
			if result.kind != decisionKindUpdateDesireStatus || result.err != nil || !cmp.Equal(*previous, desire.Status) {
				t.Fatalf("local validation: %+v", result)
			}
			for _, condition := range result.status.Conditions {
				if condition.Status != metav1.ConditionFalse || condition.Reason != "PreCheckFailed" || !strings.Contains(condition.Message, test.message) {
					t.Fatalf("validation condition: %+v", condition)
				}
			}
			if test.name == "action" && (len(result.status.Conditions) != 1 || result.status.Conditions[0].Type != azureresourcesapi.DeploymentStackDesireConditionTypeDegraded) {
				t.Fatal("unknown action must not default to an Apply or Delete condition")
			}
			if !cmp.Equal(previous.ObservedAt, result.status.ObservedAt) || !cmp.Equal(previous.Stack, result.status.Stack) {
				t.Fatal("local validation changed Azure observation")
			}
			desire.Status = *result.status
			if next := decideDeploymentStackDesire(context.Background(), desire, testNow.Add(time.Hour), lazyGET); next.kind != decisionKindNoOp || next.err != nil {
				t.Fatalf("unchanged validation wrote again: %+v", next)
			}
		})
	}
	for _, stack := range []*armdeploymentstacks.DeploymentStack{nil, {}, {Properties: &armdeploymentstacks.DeploymentStackProperties{Template: "Azure validates this"}}} {
		desire := newDesire()
		desire.Spec.Stack = stack
		if result := checkedDecide(t, desire, nil, nil, testNow, 1); result.kind != decisionKindApplyStack {
			t.Fatalf("controller performed Azure template validation: %+v", result)
		}
	}
}

func TestMetadataAndConditionAge(t *testing.T) {
	for _, state := range []string{"succeeded", "failed"} {
		t.Run(state, func(t *testing.T) {
			stack := observedDeploymentStack(state, "ours")
			if state == "failed" {
				stack.Properties.Error = &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidTemplate"), Message: ptr.To("original")}
			}
			desire := withApplySubmission(t, newDesire(), stack, testNow)
			for index := range 3 {
				stack.Tags = map[string]*string{"metadata": ptr.To(fmt.Sprint(index))}
				if state == "failed" {
					stack.Properties.Error.Message = ptr.To(fmt.Sprintf("diagnostics %d", index))
				}
				now := testNow.Add(defaultInterval + time.Duration(index)*time.Minute)
				result := checkedDecide(t, desire, stack, nil, now, 1)
				if result.status == nil || !cmp.Equal(result.status.ObservedAt, desire.Status.ObservedAt) || !cmp.Equal(stack, result.status.Stack) {
					t.Fatalf("metadata renewed cooldown or lost diagnostics: %+v", result)
				}
				for conditionIndex := range result.status.Conditions {
					if !result.status.Conditions[conditionIndex].LastTransitionTime.Equal(&desire.Status.Conditions[conditionIndex].LastTransitionTime) {
						t.Fatal("diagnostic change renewed condition age")
					}
				}
				desire.Status = *result.status.DeepCopy()
				if next := checkedDecide(t, desire, stack, nil, now, 1); next.kind != decisionKindApplyStack {
					t.Fatalf("metadata self-event not eligible: %+v", next)
				}
			}
			now := testNow.Add(2 * defaultInterval)
			desire = withApplySubmission(t, desire, observedDeploymentStack("deploying", "next"), now)
			stack.Properties.CorrelationID = ptr.To("next")
			result := checkedDecide(t, desire, stack, nil, now.Add(time.Minute), 1)
			if result.status == nil || !result.status.ObservedAt.Time.Equal(now.Add(time.Minute)) {
				t.Fatalf("new completion failed to refresh clock: %+v", result)
			}
		})
	}
}

func TestConservativeClassification(t *testing.T) {
	for _, test := range []struct {
		name    string
		detail  *armdeploymentstacks.ErrorDetail
		invalid bool
	}{
		{name: "no detail"},
		{name: "direct template", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidTemplate")}, invalid: true},
		{name: "direct content", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidRequestContent")}, invalid: true},
		{name: "permission ambiguity", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("InvalidTemplateDeployment")}},
		{name: "empty wrapper", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed")}},
		{name: "all narrow children", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed"), Details: []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("InvalidTemplate")}, {Code: ptr.To("InvalidRequestContent")}}}, invalid: true},
		{name: "mixed children", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed"), Details: []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("InvalidTemplate")}, {Code: ptr.To("InvalidTemplateDeployment")}}}},
		{name: "nil child", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed"), Details: []*armdeploymentstacks.ErrorDetail{nil}}},
		{name: "no generic wrapper recursion", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentFailed"), Details: []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("InvalidTemplate")}}}},
		{name: "no nested wrapper recursion", detail: &armdeploymentstacks.ErrorDetail{Code: ptr.To("DeploymentStackDeploymentFailed"), Details: []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("DeploymentFailed"), Details: []*armdeploymentstacks.ErrorDetail{{Code: ptr.To("InvalidTemplate")}}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, invalid := classifyError(test.detail)
			if message == "" || invalid != test.invalid {
				t.Fatalf("classification = %q, %v", message, invalid)
			}
		})
	}
}

func TestAzureErrorFixtures(t *testing.T) {
	for _, test := range []struct {
		file    string
		invalid bool
	}{
		{"01-malformed-template.json", true}, {"02-template-validation.json", true},
		{"03-missing-content-permission.json", false}, {"04-same-intent-retry.json", false},
		{"05-resource-validation.json", false}, {"06-output-evaluation.json", false},
		{"07-provider-failure.json", false}, {"08-direct-deployment-permission.json", false},
		{"09-nested-deployment-failure.json", false},
	} {
		t.Run(test.file, func(t *testing.T) {
			data, err := os.ReadFile("testdata/azure-errors/" + test.file)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Responses []struct {
					Case   string
					Status int
					Body   json.RawMessage
				}
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			checked := 0
			for _, response := range fixture.Responses {
				if strings.HasSuffix(response.Case, "/submit") && response.Status == 400 {
					client := sdkClient(t, "sub", func(request *http.Request) (*http.Response, error) {
						return httpResponse(request, response.Status, string(response.Body)), nil
					})
					_, err := submitApply(t.Context(), client, newDesire().Spec)
					status, err := deploymentStackDesireStatusForRejectedSubmission("hash", azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, err)
					if err == nil || status == nil || status.Conditions[0].Reason != "RequestFailed" || !strings.Contains(status.Conditions[0].Message, "Authorization failed") {
						t.Fatalf("real HTTP 400 permission failure treated as invalid: %+v, %v", status, err)
					}
					checked++
				}
				if !strings.HasSuffix(response.Case, "/get") || response.Status != 200 {
					continue
				}
				client := sdkClient(t, "sub", func(request *http.Request) (*http.Response, error) {
					return httpResponse(request, response.Status, string(response.Body)), nil
				})
				stack, err := getDeploymentStack(t.Context(), client, newDesire().Spec.TargetResourceID)
				if err != nil {
					t.Fatal(err)
				}
				properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
				state := ptr.Deref(properties.ProvisioningState, "")
				correlation := ptr.Deref(properties.CorrelationID, "")
				if state != "failed" && state != "succeeded" {
					continue
				}
				desire := withApplySubmission(t, newDesire(), observedDeploymentStack("initializing", correlation), testNow)
				result := checkedDecide(t, desire, stack, nil, testNow.Add(time.Minute), 1)
				if result.status == nil || !cmp.Equal(result.status.Stack, stack) {
					t.Fatalf("lost typed GET diagnostics: %+v", result)
				}
				if state == "succeeded" {
					if !meta.IsStatusConditionTrue(result.status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
						t.Fatal("same-intent permission retry did not succeed")
					}
				} else {
					if (result.status.Conditions[0].Reason == "InvalidIntent") != test.invalid || (result.err == nil) != test.invalid {
						t.Fatalf("fixture classification: %+v", result)
					}
					payload, _ := json.Marshal(stack.Properties.Error)
					if result.status.Conditions[0].Message != string(payload) {
						t.Fatal("condition message lost nested diagnostics")
					}
				}
				checked++
			}
			if checked != 1 {
				t.Fatalf("checked %d terminal outcomes, want 1", checked)
			}
		})
	}
}

func TestAzurePermissionRetryUsesIdenticalIntent(t *testing.T) {
	var failedRequest, retryRequest struct {
		Responses []struct {
			URL           string
			RequestBody   json.RawMessage
			RequestSHA256 string
		}
	}
	for filename, fixture := range map[string]any{
		"03-missing-content-permission.json": &failedRequest,
		"04-same-intent-retry.json":          &retryRequest,
	} {
		data, err := os.ReadFile("testdata/azure-errors/" + filename)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, fixture); err != nil {
			t.Fatal(err)
		}
	}
	if len(failedRequest.Responses) == 0 || len(retryRequest.Responses) == 0 {
		t.Fatal("missing recorded requests")
	}
	failed, retried := failedRequest.Responses[0], retryRequest.Responses[0]
	if failed.RequestSHA256 == "" || failed.RequestSHA256 != retried.RequestSHA256 || failed.URL != retried.URL || string(failed.RequestBody) != string(retried.RequestBody) {
		t.Fatal("permission-only recovery evidence must use identical target and body")
	}
}

type stackTransport func(*http.Request) (*http.Response, error)

func (transport stackTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type stackCredential struct{ err error }

func (credential stackCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if credential.err != nil {
		return azcore.AccessToken{}, credential.err
	}
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func sdkClient(t *testing.T, subscription string, transport stackTransport) *armdeploymentstacks.Client {
	t.Helper()
	client, err := armdeploymentstacks.NewClient(subscription, stackCredential{}, &azcorearm.ClientOptions{DisableRPRegistration: true, ClientOptions: policy.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func httpResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Request: request, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"60"}, "Azure-Asyncoperation": {"https://management.azure.com/operations/do-not-poll"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestSDKScopesAndDeletePolicy(t *testing.T) {
	for _, scope := range []string{"/subscriptions/sub/resourceGroups/rg", "/subscriptions/sub", "/providers/Microsoft.Management/managementGroups/mg"} {
		t.Run(scope, func(t *testing.T) {
			desire := newDesire()
			desire.Spec.TargetResourceID, _ = azcorearm.ParseResourceID(scope + "/providers/Microsoft.Resources/deploymentStacks/stack")
			desire.Spec.Stack.Properties.ActionOnUnmanage = &armdeploymentstacks.ActionOnUnmanage{Resources: ptr.To(armdeploymentstacks.UnmanageActionResourceModeDelete), ResourceGroups: ptr.To(armdeploymentstacks.UnmanageActionResourceGroupModeDetach), ManagementGroups: ptr.To(armdeploymentstacks.UnmanageActionManagementGroupModeDelete)}
			desire.Spec.Stack.Properties.BypassStackOutOfSyncError = ptr.To(true)
			calls := 0
			client := sdkClient(t, desire.Spec.TargetResourceID.SubscriptionID, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Path != desire.Spec.TargetResourceID.String() || request.URL.Query().Get("api-version") != "2025-07-01" || request.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatalf("bad route/auth: %s", request.URL)
				}
				switch calls {
				case 1:
					if request.Method != http.MethodGet {
						t.Fatal(request.Method)
					}
					return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
				case 2:
					if request.Method != http.MethodPut {
						t.Fatal(request.Method)
					}
					var actual armdeploymentstacks.DeploymentStack
					if err := json.NewDecoder(request.Body).Decode(&actual); err != nil {
						t.Fatal(err)
					}
					if !cmp.Equal(actual, *desire.Spec.Stack) {
						t.Fatalf("typed PUT changed: %+v", actual)
					}
					response := httpResponse(request, 201, `{"properties":{"provisioningState":"deploying","correlationId":"body-id"}}`)
					response.Header.Set("x-ms-correlation-request-id", "not-the-body-id")
					return response, nil
				case 3:
					if request.Method != http.MethodGet {
						t.Fatal(request.Method)
					}
					return httpResponse(request, 200, `{"properties":{"provisioningState":"succeeded","correlationId":"body-id"}}`), nil
				case 4:
					if request.Method != http.MethodDelete || request.Body != nil || request.URL.Query().Get("unmanageAction.Resources") != "delete" || request.URL.Query().Get("unmanageAction.ResourceGroups") != "detach" || request.URL.Query().Get("unmanageAction.ManagementGroups") != "delete" || request.URL.Query().Get("bypassStackOutOfSyncError") != "true" {
						t.Fatalf("typed DELETE: %s %s", request.Method, request.URL)
					}
					return httpResponse(request, 202, ""), nil
				default:
					t.Fatal("unexpected poll request")
					return nil, errors.New("unexpected request")
				}
			})
			before := desire.DeepCopy()
			if result := checkedDecide(t, desire, nil, nil, testNow, 1); result.kind != decisionKindApplyStack {
				t.Fatalf("supported scope rejected: %+v", result)
			}
			if stack, err := getDeploymentStack(t.Context(), client, desire.Spec.TargetResourceID); err != nil || stack != nil {
				t.Fatalf("GET absence: %+v, %v", stack, err)
			}
			stack, err := submitApply(t.Context(), client, desire.Spec)
			if err != nil {
				t.Fatal(err)
			}
			properties := ptr.Deref(stack.Properties, armdeploymentstacks.DeploymentStackProperties{})
			if ptr.Deref(properties.ProvisioningState, "") != "deploying" || ptr.Deref(properties.CorrelationID, "") != "body-id" {
				t.Fatalf("lost initial stack body: %+v", stack)
			}
			if _, err := getDeploymentStack(t.Context(), client, desire.Spec.TargetResourceID); err != nil {
				t.Fatal(err)
			}
			if err := submitDelete(t.Context(), client, desire.Spec); err != nil {
				t.Fatal(err)
			}
			if calls != 4 || !reflect.DeepEqual(before, desire) {
				t.Fatal("execution polled or mutated intent")
			}
		})
	}
}

func TestSDKSubmissionOutcomes(t *testing.T) {
	for _, test := range []struct {
		name    string
		code    int
		body    string
		wantErr bool
		reason  string
	}{
		{name: "synchronous success", code: 200, body: `{"properties":{"provisioningState":"succeeded","correlationId":"ours"}}`, reason: "Succeeded"},
		{name: "initializing", code: 201, body: `{"properties":{"provisioningState":"initializing","correlationId":"ours"}}`, reason: "WaitingForCompletion"},
		{name: "nonterminal HTTP 200", code: 200, body: `{"properties":{"provisioningState":"initializing","correlationId":"ours"}}`, reason: "WaitingForCompletion"},
		{name: "invalid template", code: 400, body: `{"error":{"code":"InvalidTemplate","message":"bad template"}}`, reason: "InvalidIntent"},
		{name: "ambiguous HTTP 400", code: 400, body: `{"error":{"code":"InvalidTemplateDeployment","message":"permission missing"}}`, reason: "RequestFailed", wantErr: true},
		{name: "malformed HTTP 400", code: 400, body: `not JSON`, reason: "RequestFailed", wantErr: true},
		{name: "denied", code: 403, body: `{"error":{"code":"Denied","message":"diagnostics"}}`, reason: "RequestFailed", wantErr: true},
		{name: "busy", code: 409, body: `{"error":{"code":"Busy"}}`, reason: "RequestFailed", wantErr: true},
		{name: "precondition", code: 412, body: `{"error":{"code":"Conflict"}}`, reason: "RequestFailed", wantErr: true},
		{name: "throttled", code: 429, body: `{"error":{"code":"Throttled"}}`, reason: "RequestFailed", wantErr: true},
		{name: "server failure", code: 500, body: `{"error":{"code":"InternalError"}}`, reason: "RequestFailed", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := sdkClient(t, "sub", func(request *http.Request) (*http.Response, error) {
				calls++
				response := httpResponse(request, test.code, test.body)
				if test.name == "synchronous success" {
					response.Header.Del("Azure-Asyncoperation")
				}
				return response, nil
			})
			stack, err := submitApply(t.Context(), client, newDesire().Spec)
			var status *azureresourcesapi.DeploymentStackDesireStatus
			var submissionErr error
			if err != nil {
				status, submissionErr = deploymentStackDesireStatusForRejectedSubmission("hash", azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, err)
			} else {
				status, submissionErr = deploymentStackDesireStatusForAssociatedApply("hash", stack)
			}
			if calls != 1 || status == nil || (submissionErr != nil) != test.wantErr || status.Conditions[0].Reason != test.reason {
				t.Fatalf("submission status: %+v, error %v, calls %d", status, submissionErr, calls)
			}
			if test.code >= 400 {
				if status.Error == nil || ptr.Deref(status.Error.Message, "") != err.Error() || status.Conditions[0].Message != err.Error() {
					t.Fatal("lost SDK error diagnostics")
				}
			}
		})
	}
	for _, code := range []int{200, 202, 204} {
		client := sdkClient(t, "sub", func(request *http.Request) (*http.Response, error) { return httpResponse(request, code, ""), nil })
		status, err := deploymentStackDesireStatusForDeleteSubmission("hash", submitDelete(t.Context(), client, newDesire().Spec))
		if err != nil || status == nil || status.Conditions[0].Reason != "WaitingForDeletion" || status.Conditions[0].Status != metav1.ConditionFalse {
			t.Fatalf("Delete %d acceptance proved absence: %+v, %v", code, status, err)
		}
	}
	client := sdkClient(t, "sub", func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err := submitApply(t.Context(), client, newDesire().Spec)
	status, err := deploymentStackDesireStatusForRejectedSubmission("hash", azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, err)
	if status != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transport failure wrote status: %+v, %v", status, err)
	}
}

func TestSDKImmediateApplyFailure(t *testing.T) {
	for _, test := range []struct {
		state, code string
		invalid     bool
	}{
		{state: "failed", code: "InvalidTemplate", invalid: true},
		{state: "failed", code: "InvalidTemplateDeployment"},
		{state: "canceled", code: "OperationCanceled"},
	} {
		t.Run(test.state+"/"+test.code, func(t *testing.T) {
			body := fmt.Sprintf(`{"properties":{"provisioningState":%q,"correlationId":"ours","error":{"code":%q,"message":"initial terminal failure"}}}`, test.state, test.code)
			requests, submissions := 0, 0
			controller, storage, clk := setup(t, func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method == http.MethodGet && submissions == 0 {
					return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
				}
				if request.Method == http.MethodPut {
					submissions++
				}
				response := httpResponse(request, 200, body)
				response.Header.Del("Azure-Asyncoperation")
				return response, nil
			})
			err := controller.SyncOnce(t.Context(), testKey)
			if (err == nil) != test.invalid || (!test.invalid && !errors.Is(err, errExecutionFailed)) || storage.writes != 1 || submissions != 1 {
				t.Fatalf("initial failure not persisted: writes %d, submissions %d, error %v", storage.writes, submissions, err)
			}
			if storage.doc.Status.Stack == nil || storage.doc.Status.ObservedSpecHash == "" || meta.IsStatusConditionTrue(storage.doc.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
				t.Fatal("initial terminal failure lost association or proved readiness")
			}
			clk.Step(time.Second)
			err = controller.SyncOnce(t.Context(), testKey)
			if test.invalid {
				if err != nil || requests != 2 || storage.writes != 1 {
					t.Fatalf("invalid initial failure did not cool down: %v", err)
				}
			} else if !errors.Is(err, errExecutionFailed) || submissions != 2 || storage.writes != 2 || requests != 4 {
				t.Fatalf("recorded terminal failure not resubmitted: requests %d, writes %d, error %v", requests, storage.writes, err)
			}
		})
	}
}

// TestSDKTokenFailures checks that a token failure during the GET (observation)
// phase surfaces a Degraded condition and writes once while retrying, whereas a
// token failure during an Apply or Delete submission (a plain error, not an
// *azcore.ResponseError) returns without writing a status.
func TestSDKTokenFailures(t *testing.T) {
	for _, phase := range []string{"GET", "Apply", "Delete"} {
		t.Run(phase, func(t *testing.T) {
			controller, storage, _ := setup(t, func(*http.Request) (*http.Response, error) { t.Fatal("default client used"); return nil, nil })
			if phase == "Delete" {
				storage.doc.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
			}
			builds, requests := 0, 0
			tokenErr := errors.New("token acquisition failed")
			controller.firstPartyApplicationClientBuilder = fpaBuilder{build: func(_, subscription string) (*armdeploymentstacks.Client, error) {
				builds++
				credential := stackCredential{}
				if phase == "GET" || builds == 2 {
					credential.err = tokenErr
				}
				return armdeploymentstacks.NewClient(subscription, credential, &azcorearm.ClientOptions{DisableRPRegistration: true, ClientOptions: policy.ClientOptions{
					Retry: policy.RetryOptions{MaxRetries: -1},
					Transport: stackTransport(func(request *http.Request) (*http.Response, error) {
						requests++
						if phase == "GET" || request.Method != http.MethodGet {
							t.Fatal("request sent after token failure")
						}
						return httpResponse(request, 200, `{"properties":{"provisioningState":"succeeded","correlationId":"external"}}`), nil
					}),
				}})
			}}
			err := controller.SyncOnce(t.Context(), testKey)
			if phase == "GET" {
				// A token failure during the observing GET now surfaces a Degraded
				// condition and writes once, still returning the error for retry.
				if !errors.Is(err, tokenErr) || storage.writes != 1 || len(controller.queue.(*recordingQueue).delays) != 0 ||
					!meta.IsStatusConditionTrue(storage.doc.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeDegraded) {
					t.Fatalf("GET token failure did not surface Degraded: writes %d, error %v", storage.writes, err)
				}
			} else if !errors.Is(err, tokenErr) || storage.writes != 0 || len(controller.queue.(*recordingQueue).delays) != 0 {
				t.Fatalf("submission token failure wrote or polled: writes %d, error %v", storage.writes, err)
			}
			wantBuilds := 2
			if phase == "GET" {
				wantBuilds = 1
			}
			if builds != wantBuilds || requests != wantBuilds-1 {
				t.Fatalf("wrong execution phase: builds %d, requests %d", builds, requests)
			}
		})
	}
}

func TestSDKEmptyGETDoesNotProveAbsence(t *testing.T) {
	for _, body := range []string{"", "null", "{}"} {
		t.Run(fmt.Sprintf("body=%q", body), func(t *testing.T) {
			client := sdkClient(t, "sub", func(request *http.Request) (*http.Response, error) {
				return httpResponse(request, 200, body), nil
			})
			desire := newDesire()
			desire.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
			stack, err := getDeploymentStack(t.Context(), client, desire.Spec.TargetResourceID)
			if err != nil || stack == nil {
				t.Fatalf("SDK empty object response: %+v, %v", stack, err)
			}
			result := checkedDecide(t, desire, stack, nil, testNow, 1)
			if result.status == nil || result.status.ObservedSpecHash != "" || meta.IsStatusConditionTrue(result.status.Conditions, "SuccessfullyDeleted") {
				t.Fatalf("empty GET proved deletion: %+v", result)
			}
			desire.Status = *result.status
			if next := checkedDecide(t, desire, stack, nil, testNow.Add(time.Second), 1); next.requeue != pendingInterval {
				t.Fatalf("empty GET must wait without panicking: %+v", next)
			}
		})
	}
}

type memoryCRUD struct {
	cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire]
	doc      *azureresourcesapi.DeploymentStackDesire
	writes   int
	writeErr error
	lastETag azcore.ETag
}

// No Get implementation: the controller must not read/merge after an ETag conflict.
func (storage *memoryCRUD) Replace(_ context.Context, desire *azureresourcesapi.DeploymentStackDesire, options *azcosmos.ItemOptions) (*azureresourcesapi.DeploymentStackDesire, error) {
	storage.writes++
	if options != nil && options.IfMatchEtag != nil {
		storage.lastETag = *options.IfMatchEtag
	}
	if storage.writeErr != nil {
		return nil, storage.writeErr
	}
	if options == nil || options.IfMatchEtag == nil || *options.IfMatchEtag != storage.doc.CosmosETag {
		return nil, &azcore.ResponseError{StatusCode: 412}
	}
	storage.doc = desire.DeepCopy()
	storage.doc.CosmosETag = azcore.ETag(fmt.Sprint(storage.writes))
	return storage.doc.DeepCopy(), nil
}

type memoryDB struct {
	azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD
	crud *memoryCRUD
}

func (database memoryDB) DeploymentStackDesiresFor(azureresourcescosmosstorage.DeploymentStackDesireScope) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	return database.crud, nil
}

type memoryLister struct {
	azureresourceslisters.DeploymentStackDesireLister
	crud *memoryCRUD
}

func (lister memoryLister) GetByResourceID(context.Context, string) (*azureresourcesapi.DeploymentStackDesire, error) {
	if lister.crud.doc == nil {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	return lister.crud.doc, nil
}

type recordingQueue struct {
	workqueue.TypedRateLimitingInterface[Key]
	delays []time.Duration
}

func (q *recordingQueue) AddAfter(_ Key, delay time.Duration) {
	q.delays = append(q.delays, delay)
}

type fpaBuilder struct {
	azureclient.FirstPartyApplicationClientBuilder
	build func(string, string) (*armdeploymentstacks.Client, error)
}

func (builder fpaBuilder) DeploymentStacksClient(tenant, subscription string) (*armdeploymentstacks.Client, error) {
	return builder.build(tenant, subscription)
}

type smiBuilder struct {
	azureclient.ServiceManagedIdentityClientBuilder
	build func(context.Context, string, *azcorearm.ResourceID, string) (*armdeploymentstacks.Client, error)
}

func (builder smiBuilder) DeploymentStacksClient(ctx context.Context, url string, identity *azcorearm.ResourceID, subscription string) (*armdeploymentstacks.Client, error) {
	return builder.build(ctx, url, identity, subscription)
}

func setup(t *testing.T, transport stackTransport) (*controller, *memoryCRUD, *clocktesting.FakeClock) {
	t.Helper()
	storage := &memoryCRUD{doc: newDesire()}
	clk := clocktesting.NewFakeClock(testNow)
	fpa := fpaBuilder{build: func(tenant, subscription string) (*armdeploymentstacks.Client, error) {
		if tenant != "tenant" || subscription != "sub" {
			t.Fatalf("FPA builder: %s %s", tenant, subscription)
		}
		return sdkClient(t, subscription, transport), nil
	}}
	return &controller{crudByParent: memoryDB{crud: storage}, deploymentStackDesireLister: memoryLister{crud: storage}, firstPartyApplicationClientBuilder: fpa, clock: clk, queue: &recordingQueue{}}, storage, clk
}

func TestOuterSingleMutationAndPolling(t *testing.T) {
	var storage *memoryCRUD
	calls := 0
	controller, crud, clk := setup(t, func(request *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if request.Method != http.MethodGet {
				t.Fatal(request.Method)
			}
			return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
		case 2:
			if request.Method != http.MethodPut || storage.writes != 0 {
				t.Fatal("pre-submit checkpoint or wrong method")
			}
			return httpResponse(request, 201, `{"properties":{"provisioningState":"deploying","correlationId":"ours"}}`), nil
		case 3:
			return httpResponse(request, 200, `{"properties":{"provisioningState":"deploying","correlationId":"ours"}}`), nil
		case 4:
			return httpResponse(request, 200, `{"properties":{"provisioningState":"succeeded","correlationId":"ours"}}`), nil
		default:
			t.Fatal("unexpected request")
			return nil, errors.New("unexpected request")
		}
	})
	storage = crud
	for index, wantWrites := range []int{1, 1, 2, 2, 2} {
		if err := controller.SyncOnce(t.Context(), testKey); err != nil || crud.writes != wantWrites {
			t.Fatalf("pass %d: writes %d, error %v", index, crud.writes, err)
		}
		clk.Step(time.Second)
	}
	if calls != 4 || !reflect.DeepEqual(controller.queue.(*recordingQueue).delays, []time.Duration{pendingInterval}) {
		t.Fatalf("requests %d, queue %+v", calls, controller.queue)
	}
}

func TestSubmissionFailureWrites(t *testing.T) {
	for _, action := range []azureresourcesapi.DeploymentStackDesireActionType{azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireActionTypeDelete} {
		for _, failure := range []string{"transport", "rejected", "invalid", "decode"} {
			if action == azureresourcesapi.DeploymentStackDesireActionTypeDelete && failure == "decode" {
				continue
			}
			t.Run(string(action)+"/"+failure, func(t *testing.T) {
				controller, storage, _ := setup(t, func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return httpResponse(request, 200, `{"properties":{"provisioningState":"succeeded","correlationId":"external"}}`), nil
					}
					switch failure {
					case "transport":
						return nil, context.DeadlineExceeded
					case "decode":
						return httpResponse(request, 201, `{"properties":{"provisioningState":"deploying"},"tags":"not an object"}`), nil
					case "invalid":
						return httpResponse(request, 400, `{"error":{"code":"InvalidTemplate","message":"bad template"}}`), nil
					default:
						return httpResponse(request, 403, `{"error":{"code":"Denied","message":"permission missing"}}`), nil
					}
				})
				storage.doc.Spec.Action = action
				err := controller.SyncOnce(t.Context(), testKey)
				if (err == nil) != (failure == "invalid") {
					t.Fatalf("unexpected submission error: %v", err)
				}
				if failure == "transport" || failure == "decode" {
					if storage.writes != 0 {
						t.Fatalf("unknown outcome wrote status: %d writes", storage.writes)
					}
				} else if storage.writes != 1 {
					t.Fatalf("rejection not persisted: %d writes", storage.writes)
				}
			})
		}
	}
}

func TestOuterErrorsAndOriginalETag(t *testing.T) {
	for _, test := range []struct {
		name                   string
		getCode, submitCode    int
		transportErr, writeErr error
		concurrent             bool
		writes                 int
	}{
		{name: "GET denied", getCode: 403, writes: 1},
		{name: "GET transport", transportErr: context.DeadlineExceeded, writes: 1},
		{name: "ambiguous 400", submitCode: 400, writes: 1},
		{name: "denied", submitCode: 403, writes: 1},
		{name: "busy", submitCode: 409, writes: 1},
		{name: "precondition", submitCode: 412, writes: 1},
		{name: "write unavailable", writeErr: errors.New("Cosmos unavailable"), writes: 1},
		{name: "concurrent producer", concurrent: true, writes: 1},
		{name: "both failures", submitCode: 403, writeErr: errors.New("Cosmos unavailable"), writes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var storage *memoryCRUD
			mutations := 0
			controller, crud, _ := setup(t, func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					if test.transportErr != nil {
						return nil, test.transportErr
					}
					if test.getCode != 0 {
						return httpResponse(request, test.getCode, `{"error":{"code":"Denied"}}`), nil
					}
					return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
				}
				mutations++
				if mutations > 1 || storage.writes != 0 {
					t.Fatal("multiple mutations or pre-submit checkpoint")
				}
				if test.concurrent {
					storage.doc = storage.doc.DeepCopy()
					storage.doc.Spec.Stack.Location = ptr.To("new intent")
					storage.doc.CosmosETag = "concurrent"
				}
				if test.submitCode != 0 {
					return httpResponse(request, test.submitCode, `{"error":{"code":"Rejected","message":"complete diagnostics","details":[{"code":"NestedFailure","message":"nested"}]}}`), nil
				}
				return httpResponse(request, 201, `{"properties":{"provisioningState":"deploying","correlationId":"new"}}`), nil
			})
			storage = crud
			crud.writeErr = test.writeErr
			before := crud.doc.DeepCopy()
			err := controller.SyncOnce(t.Context(), testKey)
			if err == nil || crud.writes != test.writes || len(controller.queue.(*recordingQueue).delays) != 0 {
				t.Fatalf("writes %d, error %v", crud.writes, err)
			}
			if crud.writes > 0 && crud.lastETag != "original" {
				t.Fatalf("wrong ETag: %s", crud.lastETag)
			}
			if test.concurrent && (!cosmosstorageutils.IsPreconditionFailedError(err) || crud.doc.CosmosETag != "concurrent" || !cmp.Equal(before.Status, crud.doc.Status)) {
				t.Fatalf("lost original ETag or merged stale status: %v", err)
			}
			if test.writeErr != nil && !errors.Is(err, test.writeErr) {
				t.Fatalf("lost Cosmos error: %v", err)
			}
			if test.submitCode != 0 {
				var responseErr *azcore.ResponseError
				if !errors.As(err, &responseErr) || responseErr.StatusCode != test.submitCode {
					t.Fatalf("lost execution error: %v", err)
				}
				if test.writeErr == nil && (crud.doc.Status.Error == nil || !strings.Contains(ptr.Deref(crud.doc.Status.Error.Message, ""), "NestedFailure")) {
					t.Fatal("lost SDK error summary diagnostics")
				}
			}
		})
	}
}

// TestCredentialBuilderRoutingAndCaching verifies both auth types route to the correct
// builder, and that FPA builds an independent client per use while SMI caches a single
// client (and the token fetch behind it) across the lazy GET and submission paths within
// one SyncOnce.
func TestCredentialBuilderRoutingAndCaching(t *testing.T) {
	for _, authentication := range []azureresourcesapi.DeploymentStackDesireAuthenticationType{azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA, azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI} {
		cached := authentication == azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI
		failBuilds := []int{0, 1, 2}
		if cached {
			// SMI caches a successful client across both uses within one SyncOnce, so the
			// underlying builder is invoked at most once; there is no second build to fail.
			failBuilds = []int{0, 1}
		}
		for _, failBuild := range failBuilds {
			t.Run(fmt.Sprintf("%s/fail%d", authentication, failBuild), func(t *testing.T) {
				controller, storage, _ := setup(t, func(*http.Request) (*http.Response, error) { t.Fatal("default builder used"); return nil, nil })
				identity, _ := azcorearm.ParseResourceID("/subscriptions/identity-sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi")
				storage.doc.Spec.Authentication = azureresourcesapi.DeploymentStackDesireAuthentication{
					Type:                   authentication,
					FirstPartyApplication:  &azureresourcesapi.DeploymentStackDesireAuthenticationFirstPartyApplication{TenantID: "tenant"},
					ServiceManagedIdentity: &azureresourcesapi.DeploymentStackDesireAuthenticationServiceManagedIdentity{ServiceManagedIdentityResourceID: identity, ClusterIdentityURL: "https://identity.example/cluster"},
				}
				builds, requests := 0, 0
				build := func() (*armdeploymentstacks.Client, error) {
					builds++
					if builds == failBuild {
						return nil, context.DeadlineExceeded
					}
					return sdkClient(t, "sub", func(request *http.Request) (*http.Response, error) {
						requests++
						switch request.Method {
						case http.MethodGet:
							return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
						case http.MethodPut:
							return httpResponse(request, 201, `{"properties":{"provisioningState":"deploying","correlationId":"ours"}}`), nil
						}
						t.Fatalf("unexpected method %s", request.Method)
						return nil, nil
					}), nil
				}
				controller.firstPartyApplicationClientBuilder = fpaBuilder{build: func(tenant, subscription string) (*armdeploymentstacks.Client, error) {
					if authentication != azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA || tenant != "tenant" || subscription != "sub" {
						t.Fatal("incorrect FPA routing")
					}
					return build()
				}}
				controller.serviceManagedIdentityClientBuilder = smiBuilder{build: func(_ context.Context, url string, id *azcorearm.ResourceID, subscription string) (*armdeploymentstacks.Client, error) {
					if authentication != azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI || url != "https://identity.example/cluster" || id != identity || subscription != "sub" {
						t.Fatal("incorrect SMI routing")
					}
					return build()
				}}
				err := controller.SyncOnce(t.Context(), testKey)
				if failBuild != 0 {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("client failure lost error: builds %d, requests %d, writes %d, err %v", builds, requests, storage.writes, err)
					}
					if failBuild == 1 {
						// The first build backs the lazy GET, so failing to build the client is an
						// observation failure: it now surfaces a Degraded condition and writes once
						// while still returning the error for a rate-limited retry.
						if builds != 1 || requests != 0 || storage.writes != 1 ||
							!meta.IsStatusConditionTrue(storage.doc.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeDegraded) {
							t.Fatalf("observation build failure did not surface Degraded: builds %d, requests %d, writes %d", builds, requests, storage.writes)
						}
						return
					}
					// A later build backs the submission; that failure returns before any status write.
					if storage.writes != 0 || builds != failBuild || requests != failBuild-1 {
						t.Fatalf("submission build failure wrote status: builds %d, requests %d, writes %d", builds, requests, storage.writes)
					}
					return
				}
				wantBuilds := 2
				if cached {
					wantBuilds = 1
				}
				if err != nil || builds != wantBuilds || requests != 2 || storage.writes != 1 {
					t.Fatalf("credential builder usage: builds %d, requests %d, writes %d, err %v", builds, requests, storage.writes, err)
				}
			})
		}
	}
}

func TestLostSubmissionStatusRecovery(t *testing.T) {
	stackState := "absent"
	submissions := 0
	controller, storage, clk := setup(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPut {
			submissions++
			stackState = "deploying"
			return httpResponse(request, 201, `{"properties":{"provisioningState":"deploying","correlationId":"lost"}}`), nil
		}
		if stackState == "absent" {
			return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
		}
		return httpResponse(request, 200, fmt.Sprintf(`{"properties":{"provisioningState":%q,"correlationId":"lost"}}`, stackState)), nil
	})
	storage.writeErr = errors.New("lost status write")
	if err := controller.SyncOnce(t.Context(), testKey); !errors.Is(err, storage.writeErr) || submissions != 1 || storage.doc.Status.ObservedSpecHash != "" {
		t.Fatalf("failed write: %v", err)
	}
	storage.writeErr = nil
	clk.Step(time.Second)
	if err := controller.SyncOnce(t.Context(), testKey); err != nil || submissions != 1 || storage.doc.Status.ObservedSpecHash != "" {
		t.Fatalf("unknown running operation claimed: %v", err)
	}
	if err := controller.SyncOnce(t.Context(), testKey); err != nil || storage.writes != 2 || len(controller.queue.(*recordingQueue).delays) != 1 {
		t.Fatalf("unknown operation did not poll without write: %v", err)
	}
	stackState = "succeeded"
	if err := controller.SyncOnce(t.Context(), testKey); err != nil || submissions != 2 || storage.writes != 3 {
		t.Fatalf("unknown completed operation did not replay immediately: %v", err)
	}
	if storage.doc.Status.ObservedSpecHash == "" || meta.IsStatusConditionTrue(storage.doc.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
		t.Fatal("new running submission not associated correctly")
	}
}

func TestOuterLocalValidationAndMissingDocument(t *testing.T) {
	controller, storage, _ := setup(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected Azure request"); return nil, nil })
	storage.doc.Spec.Authentication.Type = azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI
	if err := controller.SyncOnce(t.Context(), testKey); err != nil || storage.writes != 1 || storage.doc.Status.Conditions[0].Reason != "PreCheckFailed" || meta.IsStatusConditionTrue(storage.doc.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeDegraded) {
		t.Fatalf("nil SMI did not produce nondegraded local validation: %v", err)
	}
	storage.doc = nil
	if err := controller.SyncOnce(t.Context(), testKey); err != nil || storage.writes != 1 {
		t.Fatalf("missing desire: %v", err)
	}
}

func TestOuterIdenticalRejectionRefreshesCooldown(t *testing.T) {
	for _, operation := range []azureresourcesapi.DeploymentStackDesireActionType{azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireActionTypeDelete} {
		t.Run(string(operation), func(t *testing.T) {
			requests, submissions := 0, 0
			controller, storage, clk := setup(t, func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method == http.MethodGet {
					return httpResponse(request, 200, `{"properties":{"provisioningState":"succeeded","correlationId":"older"}}`), nil
				}
				submissions++
				return httpResponse(request, 400, `{"error":{"code":"InvalidRequestContent","message":"identical rejection"}}`), nil
			})
			storage.doc.Spec.Action = operation
			for attempt := 1; attempt <= 3; attempt++ {
				if err := controller.SyncOnce(t.Context(), testKey); err != nil || storage.writes != attempt || submissions != attempt || requests != 2*attempt {
					t.Fatalf("attempt %d: writes %d, submissions %d, requests %d, error %v", attempt, storage.writes, submissions, requests, err)
				}
				if !storage.doc.Status.ObservedAt.Time.Equal(clk.Now()) || !storage.doc.Status.Conditions[0].LastTransitionTime.Time.Equal(testNow) {
					t.Fatal("identical new rejection must refresh attempt time but preserve false condition age")
				}
				clk.Step(defaultInterval - time.Nanosecond)
				if err := controller.SyncOnce(t.Context(), testKey); err != nil || storage.writes != attempt || requests != 2*attempt {
					t.Fatalf("identical rejection failed to restart cooldown: %v", err)
				}
				clk.Step(time.Nanosecond)
			}
		})
	}
}

func TestOuterDeleteRequiresObservedAbsence(t *testing.T) {
	calls := 0
	controller, storage, clk := setup(t, func(request *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if request.Method != http.MethodGet {
				t.Fatal(request.Method)
			}
			return httpResponse(request, 200, `{"properties":{"provisioningState":"succeeded","correlationId":"old-apply"}}`), nil
		case 2:
			if request.Method != http.MethodDelete {
				t.Fatal(request.Method)
			}
			return httpResponse(request, 204, ""), nil
		case 3, 4:
			return httpResponse(request, 200, `{"properties":{"provisioningState":"deletingResources","correlationId":"unknown-delete"}}`), nil
		case 5:
			return httpResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
		default:
			t.Fatal("unexpected request during cooldown")
			return nil, nil
		}
	})
	storage.doc.Spec.Action = azureresourcesapi.DeploymentStackDesireActionTypeDelete
	for index, wantWrites := range []int{1, 2, 2, 3, 3} {
		if err := controller.SyncOnce(t.Context(), testKey); err != nil || storage.writes != wantWrites {
			t.Fatalf("pass %d: writes %d, error %v", index, storage.writes, err)
		}
		if ready := meta.IsStatusConditionTrue(storage.doc.Status.Conditions, "SuccessfullyDeleted"); ready != (index >= 3) {
			t.Fatalf("pass %d: readiness %v", index, ready)
		}
		clk.Step(time.Second)
	}
	hash, _ := azureresourcesapihelpers.DeploymentStackDesireSpecHash(storage.doc.Spec)
	if storage.doc.Status.ObservedSpecHash != hash || !reflect.DeepEqual(controller.queue.(*recordingQueue).delays, []time.Duration{pendingInterval}) {
		t.Fatal("Delete confirmation hash or fixed polling incorrect")
	}
}
