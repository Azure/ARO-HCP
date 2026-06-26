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

package status

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fixedNow is the synthetic "now" used by every UnionCondition test case so
// the inertia arithmetic is reproducible. Pick a value that doesn't sit on
// a daylight-savings boundary.
var fixedNow = time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

// degradedAt builds a Degraded SourcedCondition shaped for these tests.
// `age` is how long ago the condition's LastTransitionTime is from fixedNow
// — a positive value means the condition has held its current value for at
// least that long.
func degradedAt(controllerName string, status metav1.ConditionStatus, reason, message string, age time.Duration) SourcedCondition {
	return SourcedCondition{
		ControllerName: controllerName,
		Condition: metav1.Condition{
			Type:               DegradedConditionType,
			Status:             status,
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.NewTime(fixedNow.Add(-age)),
		},
	}
}

func TestUnionCondition(t *testing.T) {
	// Three inertia setups exercised across cases:
	//   - nilInertia: every disagreeing source is propagated immediately.
	//   - thirtySecondInertia: the default 30s window the production wiring uses.
	//   - perControllerInertia: one controller has 5m inertia, everything else
	//     uses the default 30s. Lets us verify that overrides actually apply.
	thirtySecondInertia := MustNewInertia(30 * time.Second).Inertia
	perControllerInertia := MustNewInertia(
		30*time.Second,
		InertiaController{ControllerNameMatcher: regexp.MustCompile(`^SlowController$`), Duration: 5 * time.Minute},
	).Inertia

	tests := []struct {
		name string
		// inputs
		inertia Inertia
		sources []SourcedCondition
		// expectations on the resulting aggregated metav1.Condition
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		// --- shape cases (no inertia involvement) ---
		{
			name:           "no sources -> Unknown/NoData",
			inertia:        thirtySecondInertia,
			sources:        nil,
			expectedStatus: metav1.ConditionUnknown,
			expectedReason: "NoData",
			// no message expected; the aggregate has nothing to say.
		},
		{
			name:    "all sources at default -> defaultStatus/AsExpected with empty-message fallback",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionFalse, "NoErrors", "", 1*time.Minute),
				degradedAt("BController", metav1.ConditionFalse, "NoErrors", "", 1*time.Minute),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "AsExpected",
			expectedMessage: "All is well",
		},
		{
			name:    "all sources at default with messages -> joined message kept attributable",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionFalse, "NoErrors", "everything green", 1*time.Minute),
				degradedAt("BController", metav1.ConditionFalse, "NoErrors", "all good", 1*time.Minute),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "AsExpected",
			expectedMessage: "AController: everything green\nBController: all good",
		},
		{
			name:    "ignores conditions whose type does not match",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				{
					ControllerName: "AController",
					Condition: metav1.Condition{
						Type:               "Available",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(fixedNow.Add(-time.Hour)),
					},
				},
			},
			expectedStatus: metav1.ConditionUnknown,
			expectedReason: "NoData",
		},

		// --- inertia: nil disables flap protection entirely ---
		{
			name:    "nil inertia propagates a brand-new Degraded=True immediately",
			inertia: nil,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Second),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "AController_Failed",
			expectedMessage: "AController: boom",
		},

		// --- inertia: 30s default window ---
		{
			name:    "single bad source within 30s inertia is hidden as default/AsExpected",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "boom", 10*time.Second),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "AsExpected",
			expectedMessage: "AController: boom",
		},
		{
			name:    "bad source past 30s inertia flips aggregate to True",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "boom", 31*time.Second),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "AController_Failed",
			expectedMessage: "AController: boom",
		},
		{
			name:    "bad source exactly at inertia boundary is not yet elder (strictly Before required)",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "boom", 30*time.Second),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "AsExpected",
			expectedMessage: "AController: boom",
		},
		{
			name:    "mix of fresh and elder bad sources: elder drives the flip, aggregated text covers all bad sources",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("ElderController", metav1.ConditionTrue, "Failed", "long-standing", 2*time.Minute),
				degradedAt("FreshController", metav1.ConditionTrue, "Failed", "just happened", 5*time.Second),
				degradedAt("GoodController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			// Once elderBad is non-empty the aggregate flips, and the message/reason
			// then enumerate every bad source (matching library-go). GoodController
			// is not "bad", so it does not contribute.
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "ElderController_Failed::FreshController_Failed",
			expectedMessage: "ElderController: long-standing\nFreshController: just happened",
		},
		{
			name:    "all bad sources fresh -> aggregate stays default and reports all-good (flap protection working)",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "transient blip", 5*time.Second),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "AsExpected",
			expectedMessage: "AController: transient blip",
		},

		// --- inertia: per-controller override ---
		{
			name:    "per-controller override: SlowController stays in inertia window while NormalController flips",
			inertia: perControllerInertia,
			sources: []SourcedCondition{
				// 2m old: would flip if it had the default 30s, but SlowController has 5m so it is
				// still in its inertia window and is NOT counted as elder. The aggregated reason and
				// message still mention all bad sources (matching library-go's behavior), but the
				// aggregate would not have flipped without the elder NormalController below.
				degradedAt("SlowController", metav1.ConditionTrue, "Failed", "still settling", 2*time.Minute),
				// 1m old: default 30s so it is elder, and drives the flip.
				degradedAt("NormalController", metav1.ConditionTrue, "Failed", "boom", 1*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "NormalController_Failed::SlowController_Failed",
			expectedMessage: "NormalController: boom\nSlowController: still settling",
		},
		{
			name:    "per-controller override: SlowController alone within window -> no flip",
			inertia: perControllerInertia,
			sources: []SourcedCondition{
				degradedAt("SlowController", metav1.ConditionTrue, "Failed", "still settling", 2*time.Minute),
			},
			// 2m old < 5m override window -> elderBad is empty -> all-good path.
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "AsExpected",
			expectedMessage: "SlowController: still settling",
		},
		{
			name:    "per-controller override: SlowController past its 5m window also flips",
			inertia: perControllerInertia,
			sources: []SourcedCondition{
				degradedAt("SlowController", metav1.ConditionTrue, "Failed", "really stuck", 6*time.Minute),
				degradedAt("NormalController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "SlowController_Failed",
			expectedMessage: "SlowController: really stuck",
		},

		// --- aggregation of multiple elder bad sources ---
		{
			name:    "multiple elder bad sources -> sorted-by-controller reason, joined message",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				// Deliberately out-of-order to verify sort.
				degradedAt("Zeta", metav1.ConditionTrue, "Failed", "zeta broke", 2*time.Minute),
				degradedAt("Alpha", metav1.ConditionTrue, "Crashed", "alpha broke", 2*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "Alpha_Crashed::Zeta_Failed",
			expectedMessage: "Alpha: alpha broke\nZeta: zeta broke",
		},
		{
			name:    "elder bad source with empty reason -> reason carries only the controller name",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "", "boom", 2*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "AController",
			expectedMessage: "AController: boom",
		},
		{
			name:    "elder bad source with empty message contributes nothing to the joined message",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "", 2*time.Minute),
				degradedAt("BController", metav1.ConditionTrue, "Failed", "real failure", 2*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "AController_Failed::BController_Failed",
			expectedMessage: "BController: real failure",
		},
		{
			name:    "duplicate message lines from a single source are deduped",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "line one\nline one\nline two", 2*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "AController_Failed",
			expectedMessage: "AController: line one\nAController: line two",
		},

		// --- Unknown handling ---
		{
			name:    "Unknown-status bad source past inertia -> aggregate Unknown",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionUnknown, "Investigating", "still figuring out", 2*time.Minute),
			},
			// One Unknown bad source and no True bad sources -> aggregate stays at Unknown
			// (the initial badConditionStatus value).
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "AController_Investigating",
			expectedMessage: "AController: still figuring out",
		},
		{
			name:    "Unknown + True bad sources past inertia -> True wins for the aggregate status",
			inertia: thirtySecondInertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionUnknown, "Investigating", "still figuring out", 2*time.Minute),
				degradedAt("BController", metav1.ConditionTrue, "Failed", "boom", 2*time.Minute),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "AController_Investigating::BController_Failed",
			expectedMessage: "AController: still figuring out\nBController: boom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UnionCondition(DegradedConditionType, metav1.ConditionFalse, tc.inertia, fixedNow, tc.sources...)
			assert.Equal(t, tc.expectedStatus, got.Status, "status")
			assert.Equal(t, tc.expectedReason, got.Reason, "reason")
			assert.Equal(t, tc.expectedMessage, got.Message, "message")
		})
	}
}

// TestUnionCondition_LatestTransitionTime confirms that the
// LastTransitionTime on the aggregated condition is taken from the source
// that drives the result (newest interesting source when good, newest bad
// source when flipped). Kept separate so the main table-driven test stays
// focused on status / reason / message.
func TestUnionCondition_LatestTransitionTime(t *testing.T) {
	tests := []struct {
		name    string
		inertia Inertia
		sources []SourcedCondition
		// expectedLastTransition is computed relative to fixedNow.
		expectedAge time.Duration
	}{
		{
			name:    "all-good: pick the most recent interesting source",
			inertia: MustNewInertia(30 * time.Second).Inertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionFalse, "NoErrors", "", 2*time.Minute),
				degradedAt("BController", metav1.ConditionFalse, "NoErrors", "", 10*time.Second),
			},
			expectedAge: 10 * time.Second,
		},
		{
			name:    "elder-bad: pick the most recent bad source (which is still old enough to be elder)",
			inertia: MustNewInertia(30 * time.Second).Inertia,
			sources: []SourcedCondition{
				degradedAt("AController", metav1.ConditionTrue, "Failed", "boom", 5*time.Minute),
				degradedAt("BController", metav1.ConditionTrue, "Failed", "boom", 1*time.Minute),
			},
			expectedAge: 1 * time.Minute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UnionCondition(DegradedConditionType, metav1.ConditionFalse, tc.inertia, fixedNow, tc.sources...)
			assert.True(t, got.LastTransitionTime.Time.Equal(fixedNow.Add(-tc.expectedAge)),
				"expected LastTransitionTime %v, got %v", fixedNow.Add(-tc.expectedAge), got.LastTransitionTime.Time)
		})
	}
}
