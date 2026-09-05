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

package statusutils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// buildController is a tiny helper for table cases. It produces a Controller
// document whose ResourceID has the given trailing controller name; only the
// fields that CollectDegradedConditions reads are populated.
func buildController(t *testing.T, controllerName string, conditions ...metav1.Condition) *coreapi.Controller {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000" +
			"/resourceGroups/rg" +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c" +
			"/hcpOpenShiftControllers/" + controllerName)
	if err != nil {
		t.Fatalf("parsing resource id: %v", err)
	}
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid},
		Status:         coreapi.ControllerStatus{Conditions: conditions},
	}
}

func TestCollectDegradedConditions(t *testing.T) {
	degradedTrue := metav1.Condition{Type: DegradedConditionType, Status: metav1.ConditionTrue, Reason: "Failed", Message: "boom"}
	degradedFalse := metav1.Condition{Type: DegradedConditionType, Status: metav1.ConditionFalse, Reason: "NoErrors"}
	degradedUnknown := metav1.Condition{Type: DegradedConditionType, Status: metav1.ConditionUnknown, Reason: "Investigating", Message: "still figuring out"}
	availableTrue := metav1.Condition{Type: "Available", Status: metav1.ConditionTrue}

	// expectation describes what we expect a returned SourcedCondition to
	// look like at the union-relevant fields. ControllerName is also the
	// implicit ordering key.
	type expectation struct {
		controllerName string
		status         metav1.ConditionStatus
		reason         string
		// useFirstObservedTime is true when the synthetic missing-Degraded entry
		// is expected — LastTransitionTime should equal FixedNow (the clock's
		// "now") rather than mirroring any existing condition.
		useFirstObservedTime bool
	}

	tests := []struct {
		name        string
		controllers []*coreapi.Controller
		expected    []expectation
	}{
		{
			name:        "empty input -> empty output",
			controllers: nil,
			expected:    nil,
		},
		{
			name: "controller missing ResourceID is skipped",
			controllers: []*coreapi.Controller{
				{CosmosMetadata: coreapi.CosmosMetadata{}, Status: coreapi.ControllerStatus{Conditions: []metav1.Condition{degradedTrue}}},
			},
			expected: nil,
		},
		{
			name: "Degraded=True passes through with its real LastTransitionTime",
			controllers: []*coreapi.Controller{
				buildController(t, "A", degradedTrue),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: "Failed"}},
		},
		{
			name: "Degraded=False (healthy) is omitted",
			controllers: []*coreapi.Controller{
				buildController(t, "A", degradedFalse),
			},
			expected: nil,
		},
		{
			name: "Degraded=Unknown passes through unchanged (real LastTransitionTime, original reason)",
			controllers: []*coreapi.Controller{
				buildController(t, "A", degradedUnknown),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionUnknown, reason: "Investigating"}},
		},
		{
			name: "missing Degraded condition -> synthesized Degraded=True with first-observed-bad time",
			controllers: []*coreapi.Controller{
				buildController(t, "A", availableTrue),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: reasonMissingDegraded, useFirstObservedTime: true}},
		},
		{
			name: "controller with no conditions at all -> synthesized missing-Degraded entry",
			controllers: []*coreapi.Controller{
				buildController(t, "A"),
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: reasonMissingDegraded, useFirstObservedTime: true}},
		},
		{
			name: "mix: healthy controller omitted; degraded (True) and missing-condition still reported",
			controllers: []*coreapi.Controller{
				buildController(t, "A", degradedTrue),
				buildController(t, "B", availableTrue), // no Degraded condition -> synthesized as degraded
				buildController(t, "C", degradedFalse), // healthy -> omitted from sources
			},
			expected: []expectation{
				{controllerName: "A", status: metav1.ConditionTrue, reason: "Failed"},
				{controllerName: "B", status: metav1.ConditionTrue, reason: reasonMissingDegraded, useFirstObservedTime: true},
			},
		},
		{
			name: "nil entries in the slice are tolerated",
			controllers: []*coreapi.Controller{
				nil,
				buildController(t, "A", degradedTrue),
				nil,
			},
			expected: []expectation{{controllerName: "A", status: metav1.ConditionTrue, reason: "Failed"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := clocktesting.NewFakePassiveClock(FixedNow)
			cache := NewFirstObservedBadCache(clock)
			got := CollectDegradedConditions(tc.controllers, cache)

			require := assert.New(t)
			require.Equal(len(tc.expected), len(got), "result length")
			for i, want := range tc.expected {
				if i >= len(got) {
					break
				}
				require.Equal(want.controllerName, got[i].ControllerName, "controller name at index %d", i)
				require.Equal(DegradedConditionType, got[i].Condition.Type, "type at index %d", i)
				require.Equal(want.status, got[i].Condition.Status, "status at index %d", i)
				require.Equal(want.reason, got[i].Condition.Reason, "reason at index %d", i)
				if want.useFirstObservedTime {
					require.True(got[i].Condition.LastTransitionTime.Time.Equal(FixedNow),
						"index %d: expected LastTransitionTime to be FixedNow (cache observation), got %v", i, got[i].Condition.LastTransitionTime.Time)
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
	clock := clocktesting.NewFakePassiveClock(FixedNow)
	cache := NewFirstObservedBadCache(clock)

	controllers := []*coreapi.Controller{buildController(t, "A")}

	first := CollectDegradedConditions(controllers, cache)
	assert.Len(t, first, 1)
	firstTime := first[0].Condition.LastTransitionTime.Time

	// Advance the clock by an hour. A second pass with the same missing
	// state should keep using the original observation time.
	clock.SetTime(FixedNow.Add(time.Hour))
	second := CollectDegradedConditions(controllers, cache)
	assert.Len(t, second, 1)
	assert.True(t, second[0].Condition.LastTransitionTime.Time.Equal(firstTime),
		"expected first-observed-bad time to be sticky across reconciles")
}

// TestCollectDegradedConditions_RealConditionForgetsCache verifies that
// once a controller starts reporting a real (non-missing) Degraded
// condition, the cache entry is dropped so a later "condition disappeared
// again" starts a fresh inertia window.
func TestCollectDegradedConditions_RealConditionForgetsCache(t *testing.T) {
	clock := clocktesting.NewFakePassiveClock(FixedNow)
	cache := NewFirstObservedBadCache(clock)

	missingPhase := []*coreapi.Controller{buildController(t, "A")}
	reportingPhase := []*coreapi.Controller{
		buildController(t, "A", metav1.Condition{Type: DegradedConditionType, Status: metav1.ConditionFalse, Reason: "NoErrors"}),
	}

	// First pass: missing -> cache populated at FixedNow.
	_ = CollectDegradedConditions(missingPhase, cache)

	// Second pass: controller now reports Degraded=False -> cache forgets.
	clock.SetTime(FixedNow.Add(5 * time.Minute))
	_ = CollectDegradedConditions(reportingPhase, cache)

	// Third pass: controller goes missing again. Inertia should start at the
	// LATER observation (FixedNow+10m), not the original FixedNow.
	clock.SetTime(FixedNow.Add(10 * time.Minute))
	third := CollectDegradedConditions(missingPhase, cache)
	assert.Len(t, third, 1)
	assert.True(t, third[0].Condition.LastTransitionTime.Time.Equal(FixedNow.Add(10*time.Minute)),
		"expected the cache to start fresh after a real condition appeared and then disappeared")
}

func TestCollectDegradedDesireConditions(t *testing.T) {
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + TestSubscriptionID +
			"/resourceGroups/" + TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + TestClusterName,
	))

	applyConds := func(d *kubeapplierapi.ApplyDesire) []metav1.Condition { return d.Status.Conditions }
	readConds := func(d *kubeapplierapi.ReadDesire) []metav1.Condition { return d.Status.Conditions }

	degraded := DegradedConditionAged(metav1.ConditionTrue, "Failed", "boom", time.Minute)
	healthy := DegradedConditionAged(metav1.ConditionFalse, "NoErrors", "ok", time.Minute)
	unknown := DegradedConditionAged(metav1.ConditionUnknown, "Investigating", "hmm", time.Minute)
	// A non-Degraded condition must not qualify a desire as degraded.
	otherType := metav1.Condition{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionFalse, Reason: "PreCheckFailed"}

	t.Run("only Degraded=True ApplyDesires are included, named by full resource ID", func(t *testing.T) {
		desires := []*kubeapplierapi.ApplyDesire{
			ApplyDesireUnder(clusterID, "deg", degraded),
			ApplyDesireUnder(clusterID, "healthy", healthy), // Degraded=False -> skipped
			ApplyDesireUnder(clusterID, "unknown", unknown), // Degraded=Unknown -> skipped
			ApplyDesireUnder(clusterID, "other", otherType), // no Degraded condition -> skipped
			ApplyDesireUnder(clusterID, "no-conditions"),    // never reported -> skipped
		}
		got := CollectDegradedDesireConditions(ApplyDesireSourcePrefix, desires, applyConds)
		if assert.Len(t, got, 1, "only the degraded desire should be included") {
			wantName := ApplyDesireSourcePrefix + kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				TestSubscriptionID, TestResourceGroupName, TestClusterName, "deg")
			assert.Equal(t, wantName, got[0].ControllerName, "source name must be prefix + full lowercased resource ID")
			assert.Equal(t, DegradedConditionType, got[0].Condition.Type)
			assert.Equal(t, metav1.ConditionTrue, got[0].Condition.Status)
			assert.Equal(t, "Failed", got[0].Condition.Reason)
			assert.Equal(t, "boom", got[0].Condition.Message)
			assert.True(t, got[0].Condition.LastTransitionTime.Time.Equal(FixedNow.Add(-time.Minute)),
				"the desire's own LastTransitionTime must be preserved so inertia applies naturally")
		}
	})

	t.Run("desire with a nil ResourceID is skipped", func(t *testing.T) {
		desires := []*kubeapplierapi.ApplyDesire{
			{CosmosMetadata: coreapi.CosmosMetadata{}, Status: kubeapplierapi.ApplyDesireStatus{Conditions: []metav1.Condition{degraded}}},
		}
		got := CollectDegradedDesireConditions(ApplyDesireSourcePrefix, desires, applyConds)
		assert.Empty(t, got, "a desire with no ResourceID has no name to attribute and must be skipped")
	})

	t.Run("Degraded=True ReadDesires are included with the readdesire prefix", func(t *testing.T) {
		desires := []*kubeapplierapi.ReadDesire{
			ReadDesireUnder(clusterID, "rd", degraded),
			ReadDesireUnder(clusterID, "rd-ok", healthy),
		}
		got := CollectDegradedDesireConditions(ReadDesireSourcePrefix, desires, readConds)
		if assert.Len(t, got, 1) {
			wantName := ReadDesireSourcePrefix + kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
				TestSubscriptionID, TestResourceGroupName, TestClusterName, "rd")
			assert.Equal(t, wantName, got[0].ControllerName)
			assert.Equal(t, metav1.ConditionTrue, got[0].Condition.Status)
		}
	})

	t.Run("same trailing name at different scopes -> distinct collision-safe names", func(t *testing.T) {
		// Two ApplyDesires both named "config": one cluster-scoped, one
		// node-pool-scoped. Using the full resource ID as the source name keeps
		// them distinct (the trailing name alone would collide).
		desires := []*kubeapplierapi.ApplyDesire{
			ApplyDesireUnder(clusterID, "config", degraded),
			NodePoolScopedApplyDesireUnder(clusterID, TestNodePoolName, "config", degraded),
		}
		got := CollectDegradedDesireConditions(ApplyDesireSourcePrefix, desires, applyConds)
		if assert.Len(t, got, 2) {
			names := []string{got[0].ControllerName, got[1].ControllerName}
			assert.NotEqual(t, names[0], names[1], "same-named desires at different scopes must get distinct source names")
			assert.Contains(t, names, ApplyDesireSourcePrefix+kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				TestSubscriptionID, TestResourceGroupName, TestClusterName, "config"))
			assert.Contains(t, names, ApplyDesireSourcePrefix+kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
				TestSubscriptionID, TestResourceGroupName, TestClusterName, TestNodePoolName, "config"))
		}
	})

	t.Run("empty input -> empty output", func(t *testing.T) {
		assert.Empty(t, CollectDegradedDesireConditions(ApplyDesireSourcePrefix, nil, applyConds))
	})
}
