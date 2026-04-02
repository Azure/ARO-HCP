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

package degradedsummary

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
)

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	resourceID, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return resourceID
}

func newTestController(t *testing.T, name string, degradedStatus api.ConditionStatus, reason, message string, transitionTime time.Time) *api.Controller {
	t.Helper()
	resourceID := mustParseResourceID(t,
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/hcpOpenShiftControllers/"+name)
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Status: api.ControllerStatus{
			Conditions: []api.Condition{
				{
					Type:               "Degraded",
					Status:             degradedStatus,
					Reason:             reason,
					Message:            message,
					LastTransitionTime: transitionTime,
				},
			},
		},
	}
}

func newTestControllerWithoutDegradedCondition(t *testing.T, name string) *api.Controller {
	t.Helper()
	resourceID := mustParseResourceID(t,
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/hcpOpenShiftControllers/"+name)
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Status: api.ControllerStatus{
			Conditions: []api.Condition{},
		},
	}
}

func TestComputeDegradedCondition_NoDegradedControllers(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(0)

	controllers := []*api.Controller{
		newTestController(t, "ControllerA", api.ConditionFalse, "NoErrors", "As expected.", now.Add(-10*time.Minute)),
		newTestController(t, "ControllerB", api.ConditionFalse, "NoErrors", "As expected.", now.Add(-5*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "AsExpected", result.Reason)
	require.Equal(t, "AsExpected", result.Message)
	require.Equal(t, "Degraded", result.Type)
}

func TestComputeDegradedCondition_NilControllers(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(0)

	result := computeDegradedCondition(nil, inertia, now)

	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "AsExpected", result.Reason)
}

func TestComputeDegradedCondition_SingleDegradedController(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(0)

	controllers := []*api.Controller{
		newTestController(t, "ControllerA", api.ConditionFalse, "NoErrors", "As expected.", now.Add(-10*time.Minute)),
		newTestController(t, "ControllerB", api.ConditionTrue, "Failed", "Had an error while syncing: something broke", now.Add(-5*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "ControllerB", result.Reason)

	var degradedControllers []DegradedControllerCondition
	require.NoError(t, json.Unmarshal([]byte(result.Message), &degradedControllers))
	require.Len(t, degradedControllers, 1)
	require.Equal(t, "ControllerB", degradedControllers[0].ControllerName)
	require.Equal(t, api.ConditionTrue, degradedControllers[0].Condition.Status)
	require.Equal(t, "Failed", degradedControllers[0].Condition.Reason)
}

func TestComputeDegradedCondition_MultipleDegradedControllers_AlphabeticalOrder(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(0)

	controllers := []*api.Controller{
		newTestController(t, "Zebra", api.ConditionTrue, "Failed", "error z", now.Add(-5*time.Minute)),
		newTestController(t, "Alpha", api.ConditionTrue, "Failed", "error a", now.Add(-5*time.Minute)),
		newTestController(t, "Middle", api.ConditionFalse, "NoErrors", "As expected.", now.Add(-5*time.Minute)),
		newTestController(t, "Beta", api.ConditionTrue, "Failed", "error b", now.Add(-5*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "Alpha,Beta,Zebra", result.Reason)

	var degradedControllers []DegradedControllerCondition
	require.NoError(t, json.Unmarshal([]byte(result.Message), &degradedControllers))
	require.Len(t, degradedControllers, 3)
	require.Equal(t, "Alpha", degradedControllers[0].ControllerName)
	require.Equal(t, "Beta", degradedControllers[1].ControllerName)
	require.Equal(t, "Zebra", degradedControllers[2].ControllerName)
}

func TestComputeDegradedCondition_InertiaFiltersRecentlyDegradedControllers(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(5 * time.Minute)

	controllers := []*api.Controller{
		// Degraded for 10 minutes - should be included (past the 5-minute inertia)
		newTestController(t, "LongDegraded", api.ConditionTrue, "Failed", "error", now.Add(-10*time.Minute)),
		// Degraded for 2 minutes - should NOT be included (within 5-minute inertia)
		newTestController(t, "RecentlyDegraded", api.ConditionTrue, "Failed", "error", now.Add(-2*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "LongDegraded", result.Reason)

	var degradedControllers []DegradedControllerCondition
	require.NoError(t, json.Unmarshal([]byte(result.Message), &degradedControllers))
	require.Len(t, degradedControllers, 1)
	require.Equal(t, "LongDegraded", degradedControllers[0].ControllerName)
}

func TestComputeDegradedCondition_InertiaFiltersMakesAllDegradedDisappear(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(5 * time.Minute)

	controllers := []*api.Controller{
		// Degraded for 2 minutes - within inertia
		newTestController(t, "RecentlyDegraded", api.ConditionTrue, "Failed", "error", now.Add(-2*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "AsExpected", result.Reason)
	require.Equal(t, "AsExpected", result.Message)
}

func TestComputeDegradedCondition_PerControllerInertia(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(5*time.Minute,
		controllerutils.InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Fast"),
			Duration:              1 * time.Minute,
		},
		controllerutils.InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Slow"),
			Duration:              30 * time.Minute,
		},
	)

	controllers := []*api.Controller{
		// Fast controller: degraded for 3 minutes, inertia is 1 minute -> included
		newTestController(t, "FastController", api.ConditionTrue, "Failed", "error", now.Add(-3*time.Minute)),
		// Slow controller: degraded for 3 minutes, inertia is 30 minutes -> NOT included
		newTestController(t, "SlowController", api.ConditionTrue, "Failed", "error", now.Add(-3*time.Minute)),
		// Default controller: degraded for 3 minutes, inertia is 5 minutes -> NOT included
		newTestController(t, "DefaultController", api.ConditionTrue, "Failed", "error", now.Add(-3*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "FastController", result.Reason)
}

func TestComputeDegradedCondition_ControllerWithoutDegradedCondition(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(0)

	controllers := []*api.Controller{
		newTestControllerWithoutDegradedCondition(t, "NoDegradedCondition"),
		newTestController(t, "DegradedController", api.ConditionTrue, "Failed", "error", now.Add(-5*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "DegradedController", result.Reason)
}

func TestComputeDegradedCondition_ControllerWithUnknownStatus(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(0)

	controllers := []*api.Controller{
		newTestController(t, "UnknownController", api.ConditionUnknown, "Unknown", "status unknown", now.Add(-5*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionFalse, result.Status)
	require.Equal(t, "AsExpected", result.Reason)
}

func TestComputeDegradedCondition_ExactInertiaBoundary(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(5 * time.Minute)

	controllers := []*api.Controller{
		// Degraded for exactly 5 minutes - should be included (at the boundary)
		newTestController(t, "ExactBoundary", api.ConditionTrue, "Failed", "error", now.Add(-5*time.Minute)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionTrue, result.Status)
	require.Equal(t, "ExactBoundary", result.Reason)
}

func TestComputeDegradedCondition_JustBeforeInertia(t *testing.T) {
	now := time.Now()
	inertia := controllerutils.MustNewInertia(5 * time.Minute)

	controllers := []*api.Controller{
		// Degraded for 4 minutes 59 seconds - should NOT be included
		newTestController(t, "JustBefore", api.ConditionTrue, "Failed", "error", now.Add(-4*time.Minute-59*time.Second)),
	}

	result := computeDegradedCondition(controllers, inertia, now)

	require.Equal(t, metav1.ConditionFalse, result.Status)
}
