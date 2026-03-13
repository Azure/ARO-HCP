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

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestSetProvisioningCondition_InitialState(t *testing.T) {
	var conditions []ProvisioningCondition

	before := time.Now().UTC()
	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, "corr-123")
	after := time.Now().UTC()

	require.Len(t, conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateAccepted), conditions[0].Type)
	assert.Equal(t, ConditionTrue, conditions[0].Status)
	assert.Equal(t, "corr-123", conditions[0].CorrelationRequestID)
	assert.False(t, conditions[0].LastTransitionTime.Before(before))
	assert.False(t, conditions[0].LastTransitionTime.After(after))
}

func TestSetProvisioningCondition_Transition(t *testing.T) {
	var conditions []ProvisioningCondition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, "corr-1")
	SetProvisioningCondition(&conditions, arm.ProvisioningStateProvisioning, "corr-2")

	require.Len(t, conditions, 2)

	for _, c := range conditions {
		if c.Type == string(arm.ProvisioningStateAccepted) {
			assert.Equal(t, ConditionFalse, c.Status)
			assert.Equal(t, "corr-1", c.CorrelationRequestID, "correlation ID should be preserved")
		}
		if c.Type == string(arm.ProvisioningStateProvisioning) {
			assert.Equal(t, ConditionTrue, c.Status)
			assert.Equal(t, "corr-2", c.CorrelationRequestID)
		}
	}
}

func TestSetProvisioningCondition_FullLifecycle(t *testing.T) {
	var conditions []ProvisioningCondition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, "corr-1")
	SetProvisioningCondition(&conditions, arm.ProvisioningStateProvisioning, "corr-1")
	SetProvisioningCondition(&conditions, arm.ProvisioningStateSucceeded, "corr-1")

	require.Len(t, conditions, 3)

	for _, c := range conditions {
		if c.Type == string(arm.ProvisioningStateSucceeded) {
			assert.Equal(t, ConditionTrue, c.Status, "Succeeded should be True")
		} else {
			assert.Equal(t, ConditionFalse, c.Status, "%s should be False", c.Type)
		}
	}
}

func TestSetProvisioningCondition_PreservesEntryTimes(t *testing.T) {
	var conditions []ProvisioningCondition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, "corr-1")
	acceptedTime := conditions[0].LastTransitionTime

	time.Sleep(1 * time.Millisecond)
	SetProvisioningCondition(&conditions, arm.ProvisioningStateProvisioning, "corr-1")

	var acceptedTimeAfter time.Time
	var provisioningTime time.Time
	for _, c := range conditions {
		if c.Type == string(arm.ProvisioningStateAccepted) {
			acceptedTimeAfter = c.LastTransitionTime
		}
		if c.Type == string(arm.ProvisioningStateProvisioning) {
			provisioningTime = c.LastTransitionTime
		}
	}

	assert.Equal(t, acceptedTime, acceptedTimeAfter,
		"accepted time should be preserved when transitioning to another state")
	assert.True(t, provisioningTime.After(acceptedTime),
		"provisioning time should be after accepted time")
}

func TestSetProvisioningCondition_SameStateTwice(t *testing.T) {
	var conditions []ProvisioningCondition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, "corr-1")
	firstTime := conditions[0].LastTransitionTime

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, "corr-2")

	require.Len(t, conditions, 1, "should not duplicate conditions")
	assert.Equal(t, firstTime, conditions[0].LastTransitionTime,
		"LastTransitionTime should not change when status doesn't change")
	assert.Equal(t, "corr-2", conditions[0].CorrelationRequestID,
		"correlation ID should be updated to latest")
}

func TestSetProvisioningCondition_OnServiceProviderProperties(t *testing.T) {
	cluster := &HCPOpenShiftClusterServiceProviderProperties{}

	SetProvisioningCondition(&cluster.ProvisioningConditions, arm.ProvisioningStateProvisioning, "corr-abc")

	require.Len(t, cluster.ProvisioningConditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateProvisioning), cluster.ProvisioningConditions[0].Type)
	assert.Equal(t, ConditionTrue, cluster.ProvisioningConditions[0].Status)
	assert.Equal(t, "corr-abc", cluster.ProvisioningConditions[0].CorrelationRequestID)
}
