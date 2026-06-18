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

package statuscontrollers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

// buildController is a tiny helper for table cases. It produces a Controller
// document whose ResourceID has the given trailing controller name; only the
// fields that collectDegradedConditions reads are populated.
func buildController(t *testing.T, controllerName string, conditions ...metav1.Condition) *api.Controller {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000" +
			"/resourceGroups/rg" +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c" +
			"/hcpOpenShiftControllers/" + controllerName)
	if err != nil {
		t.Fatalf("parsing resource id: %v", err)
	}
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
		Status:         api.ControllerStatus{Conditions: conditions},
	}
}

func TestCollectDegradedConditions(t *testing.T) {
	degradedTrue := metav1.Condition{Type: degradedConditionType, Status: metav1.ConditionTrue, Reason: "Failed", Message: "boom"}
	degradedFalse := metav1.Condition{Type: degradedConditionType, Status: metav1.ConditionFalse, Reason: "NoErrors"}
	degradedUnknown := metav1.Condition{Type: degradedConditionType, Status: metav1.ConditionUnknown, Reason: "Investigating", Message: "still figuring out"}
	availableTrue := metav1.Condition{Type: "Available", Status: metav1.ConditionTrue}

	// expectation describes what we expect a returned SourcedCondition to
	// look like at the union-relevant fields. ControllerName is also the
	// implicit ordering key.
	type expectation struct {
		controllerName string
		status         metav1.ConditionStatus
		reason         string
		// useFirstObservedTime is true when the synthetic missing-Degraded entry
		// is expected — LastTransitionTime should equal fixedNow (the clock's
		// "now") rather than mirroring any existing condition.
		useFirstObservedTime bool
	}

	tests := []struct {
		name        string
		controllers []*api.Controller
		expected    []expectation
	}{
		{
			name:        "empty input -> empty output",
			controllers: nil,
			expected:    nil,
		},
		{
			name: "controller missing ResourceID is skipped",
			controllers: []*api.Controller{
				{CosmosMetadata: api.CosmosMetadata{}, Status: api.ControllerStatus{Conditions: []metav1.Condition{degradedTrue}}},
			},
			expected: nil,
		},
		{
			name: "Degraded=True passes through with its real LastTransitionTime",
			controllers: []*api.Controller{
				buildController(t, "A", degradedTrue),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: "Failed"}},
		},
		{
			name: "Degraded=False passes through",
			controllers: []*api.Controller{
				buildController(t, "A", degradedFalse),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionFalse, reason: "NoErrors"}},
		},
		{
			name: "Degraded=Unknown passes through unchanged (real LastTransitionTime, original reason)",
			controllers: []*api.Controller{
				buildController(t, "A", degradedUnknown),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionUnknown, reason: "Investigating"}},
		},
		{
			name: "missing Degraded condition -> synthesized Degraded=True with first-observed-bad time",
			controllers: []*api.Controller{
				buildController(t, "A", availableTrue),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: reasonMissingDegraded, useFirstObservedTime: true}},
		},
		{
			name: "controller with no conditions at all -> synthesized missing-Degraded entry",
			controllers: []*api.Controller{
				buildController(t, "A"),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: reasonMissingDegraded, useFirstObservedTime: true}},
		},
		{
			name: "mix: real conditions and missing controllers each get their own entry",
			controllers: []*api.Controller{
				buildController(t, "A", degradedTrue),
				buildController(t, "B", availableTrue),
				buildController(t, "C", degradedFalse),
			},
			expected: []expectation{
				{controllerName: "A", status: metav1.ConditionTrue, reason: "Failed"},
				{controllerName: "B", status: metav1.ConditionTrue, reason: reasonMissingDegraded, useFirstObservedTime: true},
				{controllerName: "C", status: metav1.ConditionFalse, reason: "NoErrors"},
			},
		},
		{
			name: "nil entries in the slice are tolerated",
			controllers: []*api.Controller{
				nil,
				buildController(t, "A", degradedTrue),
				nil,
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: "Failed"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := clocktesting.NewFakePassiveClock(fixedNow)
			cache := newFirstObservedBadCache(clock)
			got := collectDegradedConditions(tc.controllers, cache)

			require := assert.New(t)
			require.Equal(len(tc.expected), len(got), "result length")
			for i, want := range tc.expected {
				if i >= len(got) {
					break
				}
				require.Equal(want.controllerName, got[i].ControllerName, "controller name at index %d", i)
				require.Equal(degradedConditionType, got[i].Condition.Type, "type at index %d", i)
				require.Equal(want.status, got[i].Condition.Status, "status at index %d", i)
				require.Equal(want.reason, got[i].Condition.Reason, "reason at index %d", i)
				if want.useFirstObservedTime {
					require.True(got[i].Condition.LastTransitionTime.Time.Equal(fixedNow),
						"index %d: expected LastTransitionTime to be fixedNow (cache observation), got %v", i, got[i].Condition.LastTransitionTime.Time)
				}
			}
		})
	}
}

// TestCollectDegradedConditions_FirstObservedBadIsSticky verifies that a
// controller stuck in "missing Degraded" across multiple reconcile passes
// keeps its original first-observed-bad time even when the clock advances,
// so inertia is measured from when the problem began (not from every new
// "now").
func TestCollectDegradedConditions_FirstObservedBadIsSticky(t *testing.T) {
	clock := clocktesting.NewFakePassiveClock(fixedNow)
	cache := newFirstObservedBadCache(clock)

	controllers := []*api.Controller{buildController(t, "A")}

	first := collectDegradedConditions(controllers, cache)
	assert.Len(t, first, 1)
	firstTime := first[0].Condition.LastTransitionTime.Time

	// Advance the clock by an hour. A second pass with the same missing
	// state should keep using the original observation time.
	clock.SetTime(fixedNow.Add(time.Hour))
	second := collectDegradedConditions(controllers, cache)
	assert.Len(t, second, 1)
	assert.True(t, second[0].Condition.LastTransitionTime.Time.Equal(firstTime),
		"expected first-observed-bad time to be sticky across reconciles")
}

// TestCollectDegradedConditions_RealConditionForgetsCache verifies that
// once a controller starts reporting a real (non-missing) Degraded
// condition, the cache entry is dropped so a later "condition disappeared
// again" starts a fresh inertia window.
func TestCollectDegradedConditions_RealConditionForgetsCache(t *testing.T) {
	clock := clocktesting.NewFakePassiveClock(fixedNow)
	cache := newFirstObservedBadCache(clock)

	missingPhase := []*api.Controller{buildController(t, "A")}
	reportingPhase := []*api.Controller{
		buildController(t, "A", metav1.Condition{Type: degradedConditionType, Status: metav1.ConditionFalse, Reason: "NoErrors"}),
	}

	// First pass: missing -> cache populated at fixedNow.
	_ = collectDegradedConditions(missingPhase, cache)

	// Second pass: controller now reports Degraded=False -> cache forgets.
	clock.SetTime(fixedNow.Add(5 * time.Minute))
	_ = collectDegradedConditions(reportingPhase, cache)

	// Third pass: controller goes missing again. Inertia should start at the
	// LATER observation (fixedNow+10m), not the original fixedNow.
	clock.SetTime(fixedNow.Add(10 * time.Minute))
	third := collectDegradedConditions(missingPhase, cache)
	assert.Len(t, third, 1)
	assert.True(t, third[0].Condition.LastTransitionTime.Time.Equal(fixedNow.Add(10*time.Minute)),
		"expected the cache to start fresh after a real condition appeared and then disappeared")
}
