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
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/quotaerror"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Illustrative Azure text in the shape the Azure SDK, CAPZ and Hypershift produce.
// See the quotaerror tests.
const (
	sdkSeparator = "--------------------------------------------------------------------------------"

	allMachinesReadyPrefix = "hypershift NodePool condition AllMachinesReady is False: "

	// The Hypershift AllMachinesReady condition message for one failed machine.
	quotaCondition = "1 of 1 machines are not ready\n" +
		"Machine pool-1-abc: Failed: virtualmachine failed to create or update. err: PUT https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm\n" +
		sdkSeparator + "\nRESPONSE 409: 409 Conflict\nERROR CODE: OperationNotAllowed\n" + sdkSeparator + "\n" +
		"{\n  \"error\": {\n    \"code\": \"OperationNotAllowed\",\n    \"message\": \"Operation could not be completed as it results in exceeding approved Total Regional Cores quota.\"\n  }\n}\n" +
		sdkSeparator + "\n"

	platformCondition = "1 of 1 machines are not ready\n" +
		"Machine pool-1-abc: Failed: virtualmachine failed to create or update. err: PUT https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm\n" +
		sdkSeparator + "\nRESPONSE 500: 500 Internal Server Error\nERROR CODE: InternalServerError\n" + sdkSeparator + "\n" +
		"{\n  \"error\": {\n    \"code\": \"InternalServerError\",\n    \"message\": \"Internal server error.\"\n  }\n}\n" +
		sdkSeparator + "\n"

	quotaMessage    = allMachinesReadyPrefix + quotaCondition
	platformMessage = allMachinesReadyPrefix + platformCondition
)

func updatingState(message string) *operationbase.OperationState {
	return operationbase.NewOperationState(coreapi.ProvisioningStateUpdating, message)
}

func TestQuotaFailFast_LogsConversionWithDetail(t *testing.T) {
	var logged []string
	logger := funcr.New(func(_, args string) { logged = append(logged, args) }, funcr.Options{})
	ctx := utils.ContextWithLogger(t.Context(), logger)

	clk := testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := newQuotaFailFast(clk)

	q.apply(ctx, "op-1", updatingState(quotaMessage))
	require.Empty(t, logged, "starting the window must not log a conversion")

	clk.SetTime(clk.Now().Add(quotaFailFastWindow + time.Second))
	got := q.apply(ctx, "op-1", updatingState(quotaMessage))
	require.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState)

	require.Len(t, logged, 1)
	require.Contains(t, logged[0], "failing operation")
	require.Contains(t, logged[0], "Total Regional Cores quota", "log must carry the Azure quota detail")
	require.Contains(t, logged[0], "AllMachinesReady", "log must carry the original diagnostic message")
}

// The detector keys on text built by the state calculations. These run the real
// producers, so a format change there cannot silently disable detection.
func TestQuotaDetectionMatchesRealStateCalculationMessages(t *testing.T) {
	desired := &coreapi.NodePool{Properties: coreapi.NodePoolProperties{Replicas: 2}}
	status := func(allMachinesReadyMessage string) v1beta1.NodePoolStatus {
		return v1beta1.NodePoolStatus{Conditions: []v1beta1.NodePoolCondition{
			{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionFalse, Message: "2 of 2 machines are not healthy\nMachine pool-1-abc: NodeProvisioning: waiting\n"},
			{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Reason: "Failed", Message: allMachinesReadyMessage},
		}}
	}
	producers := map[string]func(v1beta1.NodePoolStatus) string{
		"create": func(s v1beta1.NodePoolStatus) string {
			_, message := (&operationNodePoolCreate{}).hypershiftNodePoolStatusMatchesDesired(desired, s)
			return message
		},
		"update": func(s v1beta1.NodePoolStatus) string {
			_, message := (&operationNodePoolUpdate{}).hypershiftNodePoolStatusMatchesDesired(desired, s)
			return message
		},
	}
	for name, produce := range producers {
		t.Run(name, func(t *testing.T) {
			message := produce(status(quotaCondition))
			detail, ok := quotaerror.Detect(message)
			require.True(t, ok, "quota failure not detected in the real %s message: %s", name, message)
			require.Contains(t, detail, "Total Regional Cores quota")

			_, ok = quotaerror.Detect(produce(status(platformCondition)))
			require.False(t, ok, "platform failure must not be detected as quota")
		})
	}
}

func TestQuotaFailFast_LogsUnclassifiedMachineFailures(t *testing.T) {
	var logged []string
	logger := funcr.New(func(_, args string) { logged = append(logged, args) }, funcr.Options{Verbosity: 1})
	ctx := utils.ContextWithLogger(t.Context(), logger)
	q := newQuotaFailFast(testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))

	q.apply(ctx, "op-1", updatingState(platformMessage))
	require.Len(t, logged, 1, "machine failures that are not quota must leave a trace")
	require.Contains(t, logged[0], "not classified as quota")

	logged = nil
	q.apply(ctx, "op-1", updatingState("hypershift NodePool status replicas is 1, want 2"))
	q.apply(ctx, "op-1", updatingState(quotaMessage))
	require.Empty(t, logged, "no miss is logged without machine failures or when quota matched")
}

func TestQuotaFailFast_FailsAfterWindow(t *testing.T) {
	clk := testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := newQuotaFailFast(clk)

	// First sighting starts the window and changes nothing.
	state := updatingState(quotaMessage)
	require.Same(t, state, q.apply(t.Context(), "op-1", state))

	// Still inside the window.
	clk.SetTime(clk.Now().Add(quotaFailFastWindow - time.Second))
	state = updatingState(quotaMessage)
	require.Same(t, state, q.apply(t.Context(), "op-1", state))

	// Window passed: the operation fails with the typed code and a customer message.
	clk.SetTime(clk.Now().Add(2 * time.Second))
	got := q.apply(t.Context(), "op-1", updatingState(quotaMessage))
	require.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState)
	require.Equal(t, coreapi.CloudErrorCodeQuotaExceeded, got.CloudErrorCode)
	require.Contains(t, got.Message, "enough Azure compute quota")
	require.Contains(t, got.Message, "exceeding approved Total Regional Cores quota")
	require.NotContains(t, got.Message, "management.azure.com", "customer message must not carry the ARM request URL")
	require.NotContains(t, got.Message, "ERROR CODE", "customer message must not carry raw SDK error text")
	require.NotNil(t, got.Error, "failed state must carry a customer safe error body")
	require.Equal(t, coreapi.CloudErrorCodeQuotaExceeded, got.Error.Code)
	require.Equal(t, got.Message, got.Error.Message)
}

func TestQuotaFailFast_PastDeadlineFailsAtFirstSighting(t *testing.T) {
	q := newQuotaFailFast(testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))

	got := q.applyPastDeadline(t.Context(), "op-1", updatingState(quotaMessage))
	require.Equal(t, coreapi.ProvisioningStateFailed, got.ProvisioningState, "no window applies once the deadline has passed")
	require.Equal(t, coreapi.CloudErrorCodeQuotaExceeded, got.CloudErrorCode)
	require.NotNil(t, got.Error)
	require.Equal(t, coreapi.CloudErrorCodeQuotaExceeded, got.Error.Code)

	// Platform failures and non machine messages still reach the deadline path unchanged.
	state := updatingState(platformMessage)
	require.Same(t, state, q.applyPastDeadline(t.Context(), "op-2", state))
	state = updatingState("hypershift NodePool status replicas is 1, want 2")
	require.Same(t, state, q.applyPastDeadline(t.Context(), "op-3", state))
}

func TestQuotaFailFast_ResetsWhenQuotaErrorGoesAway(t *testing.T) {
	clk := testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := newQuotaFailFast(clk)

	q.apply(t.Context(), "op-1", updatingState(quotaMessage))

	// The quota problem clears for one sync (machines progressing again).
	clk.SetTime(clk.Now().Add(quotaFailFastWindow - time.Minute))
	q.apply(t.Context(), "op-1", updatingState("hypershift NodePool status replicas is 1, want 2"))

	// It returns after the original window would have passed. The window restarted.
	clk.SetTime(clk.Now().Add(2 * time.Minute))
	state := updatingState(quotaMessage)
	require.Same(t, state, q.apply(t.Context(), "op-1", state))

	clk.SetTime(clk.Now().Add(quotaFailFastWindow + time.Second))
	require.Equal(t, coreapi.ProvisioningStateFailed, q.apply(t.Context(), "op-1", updatingState(quotaMessage)).ProvisioningState)
}

func TestQuotaFailFast_OtherFailuresKeepTheirPath(t *testing.T) {
	clk := testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := newQuotaFailFast(clk)

	// A platform failure never fails fast, no matter how long it lasts.
	for i := 0; i < 3; i++ {
		state := updatingState(platformMessage)
		require.Same(t, state, q.apply(t.Context(), "op-1", state))
		clk.SetTime(clk.Now().Add(quotaFailFastWindow))
	}
}

func TestQuotaFailFast_TerminalStatesPassThroughAndReset(t *testing.T) {
	clk := testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := newQuotaFailFast(clk)

	q.apply(t.Context(), "op-1", updatingState(quotaMessage))
	clk.SetTime(clk.Now().Add(quotaFailFastWindow + time.Minute))

	succeeded := operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, "")
	require.Same(t, succeeded, q.apply(t.Context(), "op-1", succeeded))

	// The earlier first seen time is gone, so quota starts a new window.
	state := updatingState(quotaMessage)
	require.Same(t, state, q.apply(t.Context(), "op-1", state))
}

func TestQuotaFailFast_OperationsAreIndependent(t *testing.T) {
	clk := testingclock.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	q := newQuotaFailFast(clk)

	q.apply(t.Context(), "op-1", updatingState(quotaMessage))
	clk.SetTime(clk.Now().Add(quotaFailFastWindow + time.Second))

	// op-2 first appears now, so only op-1 has been waiting long enough.
	state := updatingState(quotaMessage)
	require.Same(t, state, q.apply(t.Context(), "op-2", state))
	require.Equal(t, coreapi.ProvisioningStateFailed, q.apply(t.Context(), "op-1", updatingState(quotaMessage)).ProvisioningState)
}

func TestQuotaFailFast_NilReceiverIsNoOp(t *testing.T) {
	var q *quotaFailFast
	state := updatingState(quotaMessage)
	require.Same(t, state, q.apply(t.Context(), "op-1", state))
}
