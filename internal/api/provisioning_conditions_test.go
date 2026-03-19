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
	var conditions []Condition

	before := time.Now().UTC()
	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)
	after := time.Now().UTC()

	require.Len(t, conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateAccepted), conditions[0].Type)
	assert.Equal(t, ConditionTrue, conditions[0].Status)
	assert.False(t, conditions[0].LastTransitionTime.Before(before))
	assert.False(t, conditions[0].LastTransitionTime.After(after))
}

func TestSetProvisioningCondition_Transition(t *testing.T) {
	var conditions []Condition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)
	SetProvisioningCondition(&conditions, arm.ProvisioningStateProvisioning)

	require.Len(t, conditions, 2)

	byType := make(map[string]Condition)
	for _, c := range conditions {
		byType[c.Type] = c
	}

	accepted, ok := byType[string(arm.ProvisioningStateAccepted)]
	require.True(t, ok, "expected Accepted condition")
	assert.Equal(t, ConditionFalse, accepted.Status)

	provisioning, ok := byType[string(arm.ProvisioningStateProvisioning)]
	require.True(t, ok, "expected Provisioning condition")
	assert.Equal(t, ConditionTrue, provisioning.Status)
}

func TestSetProvisioningCondition_FullLifecycle(t *testing.T) {
	var conditions []Condition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)
	SetProvisioningCondition(&conditions, arm.ProvisioningStateProvisioning)
	SetProvisioningCondition(&conditions, arm.ProvisioningStateSucceeded)

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
	var conditions []Condition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)
	acceptedTime := conditions[0].LastTransitionTime

	SetProvisioningCondition(&conditions, arm.ProvisioningStateProvisioning)

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
	assert.False(t, provisioningTime.Before(acceptedTime),
		"provisioning time should be >= accepted time")
}

func TestSetProvisioningCondition_SameStateTwice(t *testing.T) {
	var conditions []Condition

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)
	firstTime := conditions[0].LastTransitionTime

	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)

	require.Len(t, conditions, 1, "should not duplicate conditions")
	assert.Equal(t, firstTime, conditions[0].LastTransitionTime,
		"LastTransitionTime should not change when status doesn't change")
}

func TestSeedProvisioningCondition_AddsInitialCondition(t *testing.T) {
	var conditions []Condition
	timestamp := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	SeedProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, timestamp)

	require.Len(t, conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateAccepted), conditions[0].Type)
	assert.Equal(t, ConditionTrue, conditions[0].Status)
	assert.Equal(t, timestamp, conditions[0].LastTransitionTime)
}

func TestSeedProvisioningCondition_IsIdempotent(t *testing.T) {
	var conditions []Condition
	firstTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	secondTime := time.Date(2024, 6, 15, 11, 0, 0, 0, time.UTC)

	SeedProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, firstTime)
	SeedProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, secondTime)

	require.Len(t, conditions, 1, "should not duplicate conditions")
	assert.Equal(t, firstTime, conditions[0].LastTransitionTime,
		"timestamp should not change on second seed")
}

func TestSeedProvisioningCondition_DoesNotOverwriteExisting(t *testing.T) {
	var conditions []Condition
	seedTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	// SetProvisioningCondition creates the condition first
	SetProvisioningCondition(&conditions, arm.ProvisioningStateAccepted)
	existingTime := conditions[0].LastTransitionTime

	// Seed should not overwrite since condition already exists
	SeedProvisioningCondition(&conditions, arm.ProvisioningStateAccepted, seedTime)

	require.Len(t, conditions, 1)
	assert.Equal(t, existingTime, conditions[0].LastTransitionTime,
		"seed should not overwrite existing condition timestamp")
}

func TestSetProvisioningCondition_OnClusterStatus(t *testing.T) {
	cluster := &HCPOpenShiftCluster{}

	SetProvisioningCondition(&cluster.Status.Conditions, arm.ProvisioningStateProvisioning)

	require.Len(t, cluster.Status.Conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateProvisioning), cluster.Status.Conditions[0].Type)
	assert.Equal(t, ConditionTrue, cluster.Status.Conditions[0].Status)
}
