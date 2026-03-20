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

package provisioningconditions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var (
	t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 = time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC)
	t2 = time.Date(2024, 1, 1, 0, 2, 0, 0, time.UTC)
	t3 = time.Date(2024, 1, 1, 0, 3, 0, 0, time.UTC)
)

func TestSet_InitialState(t *testing.T) {
	var conditions []api.Condition

	Set(&conditions, arm.ProvisioningStateAccepted, t0)

	require.Len(t, conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateAccepted), conditions[0].Type)
	assert.Equal(t, api.ConditionTrue, conditions[0].Status)
	assert.Equal(t, t0, conditions[0].LastTransitionTime)
}

func TestSet_Transition(t *testing.T) {
	var conditions []api.Condition

	Set(&conditions, arm.ProvisioningStateAccepted, t0)
	Set(&conditions, arm.ProvisioningStateProvisioning, t1)

	require.Len(t, conditions, 2)

	byType := make(map[string]api.Condition)
	for _, c := range conditions {
		byType[c.Type] = c
	}

	accepted, ok := byType[string(arm.ProvisioningStateAccepted)]
	require.True(t, ok, "expected Accepted condition")
	assert.Equal(t, api.ConditionFalse, accepted.Status)

	provisioning, ok := byType[string(arm.ProvisioningStateProvisioning)]
	require.True(t, ok, "expected Provisioning condition")
	assert.Equal(t, api.ConditionTrue, provisioning.Status)
}

func TestSet_FullLifecycle(t *testing.T) {
	var conditions []api.Condition

	Set(&conditions, arm.ProvisioningStateAccepted, t0)
	Set(&conditions, arm.ProvisioningStateProvisioning, t1)
	Set(&conditions, arm.ProvisioningStateSucceeded, t2)

	require.Len(t, conditions, 3)

	for _, c := range conditions {
		if c.Type == string(arm.ProvisioningStateSucceeded) {
			assert.Equal(t, api.ConditionTrue, c.Status, "Succeeded should be True")
		} else {
			assert.Equal(t, api.ConditionFalse, c.Status, "%s should be False", c.Type)
		}
	}
}

func TestSet_PreservesEntryTimes(t *testing.T) {
	var conditions []api.Condition

	Set(&conditions, arm.ProvisioningStateAccepted, t0)
	Set(&conditions, arm.ProvisioningStateProvisioning, t1)

	byType := make(map[string]api.Condition)
	for _, c := range conditions {
		byType[c.Type] = c
	}

	assert.Equal(t, t0, byType[string(arm.ProvisioningStateAccepted)].LastTransitionTime,
		"accepted time should be preserved when transitioning to another state")
	assert.Equal(t, t1, byType[string(arm.ProvisioningStateProvisioning)].LastTransitionTime)
}

func TestSet_SameStateTwice(t *testing.T) {
	var conditions []api.Condition

	Set(&conditions, arm.ProvisioningStateAccepted, t0)
	Set(&conditions, arm.ProvisioningStateAccepted, t1)

	require.Len(t, conditions, 1, "should not duplicate conditions")
	assert.Equal(t, t0, conditions[0].LastTransitionTime,
		"LastTransitionTime should not change when status doesn't change")
}

func TestSeed_AddsInitialCondition(t *testing.T) {
	var conditions []api.Condition

	Seed(&conditions, arm.ProvisioningStateAccepted, t0)

	require.Len(t, conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateAccepted), conditions[0].Type)
	assert.Equal(t, api.ConditionTrue, conditions[0].Status)
	assert.Equal(t, t0, conditions[0].LastTransitionTime)
}

func TestSeed_IsIdempotent(t *testing.T) {
	var conditions []api.Condition

	Seed(&conditions, arm.ProvisioningStateAccepted, t0)
	Seed(&conditions, arm.ProvisioningStateAccepted, t1)

	require.Len(t, conditions, 1, "should not duplicate conditions")
	assert.Equal(t, t0, conditions[0].LastTransitionTime,
		"timestamp should not change on second seed")
}

func TestSeed_DoesNotOverwriteExisting(t *testing.T) {
	var conditions []api.Condition

	Set(&conditions, arm.ProvisioningStateAccepted, t0)
	Seed(&conditions, arm.ProvisioningStateAccepted, t3)

	require.Len(t, conditions, 1)
	assert.Equal(t, t0, conditions[0].LastTransitionTime,
		"seed should not overwrite existing condition timestamp")
}

func TestSet_OnClusterStatus(t *testing.T) {
	cluster := &api.HCPOpenShiftCluster{}

	Set(&cluster.Status.Conditions, arm.ProvisioningStateProvisioning, t0)

	require.Len(t, cluster.Status.Conditions, 1)
	assert.Equal(t, string(arm.ProvisioningStateProvisioning), cluster.Status.Conditions[0].Type)
	assert.Equal(t, api.ConditionTrue, cluster.Status.Conditions[0].Status)
}
