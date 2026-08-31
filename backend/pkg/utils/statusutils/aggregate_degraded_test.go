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

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAggregateExternalAuthDegradedCondition(t *testing.T) {
	const (
		condType       = "Degraded"
		degradedReason = "SomethingWrong"
		goodReason     = "AsExpected"
	)

	tests := []struct {
		name         string
		conditions   []metav1.Condition
		expectStatus metav1.ConditionStatus
		expectReason string
		expectMsg    string
	}{
		{
			name:         "nil conditions -> False/AsExpected",
			conditions:   nil,
			expectStatus: metav1.ConditionFalse,
			expectReason: goodReason,
			expectMsg:    "",
		},
		{
			name:         "empty conditions -> False/AsExpected",
			conditions:   []metav1.Condition{},
			expectStatus: metav1.ConditionFalse,
			expectReason: goodReason,
			expectMsg:    "",
		},
		{
			name: "all False -> False/AsExpected",
			conditions: []metav1.Condition{
				{Type: "OIDCClientsDegraded", Status: metav1.ConditionFalse, Reason: "OK", Message: "all good"},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: goodReason,
			expectMsg:    "",
		},
		{
			name: "one True -> True/degraded with prefixed message",
			conditions: []metav1.Condition{
				{Type: "OIDCClientsDegraded", Status: metav1.ConditionTrue, Reason: "Bad", Message: "console: secret missing"},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: degradedReason,
			expectMsg:    "OIDCClientsDegraded: console: secret missing",
		},
		{
			name: "multiple True conditions -> sorted, prefixed messages",
			conditions: []metav1.Condition{
				{Type: "ClaimsDegraded", Status: metav1.ConditionTrue, Reason: "ClaimIssue", Message: "claim X is invalid"},
				{Type: "OIDCClientsDegraded", Status: metav1.ConditionTrue, Reason: "Bad", Message: "console: secret missing"},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: degradedReason,
			expectMsg:    "ClaimsDegraded: claim X is invalid\nOIDCClientsDegraded: console: secret missing",
		},
		{
			name: "mix of True and False -> only True in message",
			conditions: []metav1.Condition{
				{Type: "ClaimsDegraded", Status: metav1.ConditionFalse, Reason: "OK", Message: "fine"},
				{Type: "OIDCClientsDegraded", Status: metav1.ConditionTrue, Reason: "Bad", Message: "console: secret missing"},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: degradedReason,
			expectMsg:    "OIDCClientsDegraded: console: secret missing",
		},
		{
			name: "Unknown status is treated as degraded",
			conditions: []metav1.Condition{
				{Type: "OIDCClientsDegraded", Status: metav1.ConditionUnknown, Reason: "Investigating", Message: "checking"},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: degradedReason,
			expectMsg:    "OIDCClientsDegraded: checking",
		},
		{
			name: "multiline message prefixes only first line per source",
			conditions: []metav1.Condition{
				{Type: "OIDCClientsDegraded", Status: metav1.ConditionTrue, Reason: "Bad", Message: "console: not working\ncli: not working"},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: degradedReason,
			expectMsg:    "OIDCClientsDegraded: console: not working\ncli: not working",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateExternalAuthDegradedCondition(condType, degradedReason, goodReason, tc.conditions)
			assert.Equal(t, condType, got.Type, "condition type")
			assert.Equal(t, tc.expectStatus, got.Status, "status")
			assert.Equal(t, tc.expectReason, got.Reason, "reason")
			assert.Equal(t, tc.expectMsg, got.Message, "message")
		})
	}
}
