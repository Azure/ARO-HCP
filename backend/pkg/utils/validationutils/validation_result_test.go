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

package validationutils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestValidationResult_Validate(t *testing.T) {
	testCases := []struct {
		name    string
		result  ValidationResult
		wantErr string // empty means Validate() must return nil
	}{
		{
			name:   "passed outcome from helper is valid",
			result: PassedValidation("R", "u", "i"),
		},
		{
			name:   "failed outcome from helper is valid",
			result: FailedValidation("R", "u", "i"),
		},
		{
			name:   "skipped outcome from helper is valid",
			result: SkippedValidation("R", "u", "i"),
		},
		{
			name:   "unknown outcome from helper is valid",
			result: UnknownValidation("R", "u", "i", ControllerReportingPolicyTypeError),
		},
		{
			name:    "zero value has no outcome set",
			result:  ValidationResult{},
			wantErr: "expected exactly one outcome to be set, got 0",
		},
		{
			name: "two outcomes set",
			result: ValidationResult{
				Outcome: outcome{
					Type:   OutcomeTypePassed,
					Passed: &passedOutcome{Reason: "R"},
					Failed: &failedOutcome{Reason: "R"},
				},
			},
			wantErr: "expected exactly one outcome to be set, got 2",
		},
		{
			name: "Type is Passed but Passed payload is nil",
			result: ValidationResult{
				Outcome: outcome{
					Type:   OutcomeTypePassed,
					Failed: &failedOutcome{Reason: "R"},
				},
			},
			wantErr: `outcome Type is "Passed" but Passed payload is nil`,
		},
		{
			name: "Type is Failed but Failed payload is nil",
			result: ValidationResult{
				Outcome: outcome{
					Type:   OutcomeTypeFailed,
					Passed: &passedOutcome{Reason: "R"},
				},
			},
			wantErr: `outcome Type is "Failed" but Failed payload is nil`,
		},
		{
			name: "Type is Unknown but Unknown payload is nil",
			result: ValidationResult{
				Outcome: outcome{
					Type:   OutcomeTypeUnknown,
					Passed: &passedOutcome{Reason: "R"},
				},
			},
			wantErr: `outcome Type is "Unknown" but Unknown payload is nil`,
		},
		{
			name: "Type is Skipped but Skipped payload is nil",
			result: ValidationResult{
				Outcome: outcome{
					Type:   OutcomeTypeSkipped,
					Passed: &passedOutcome{Reason: "R"},
				},
			},
			wantErr: `outcome Type is "Skipped" but Skipped payload is nil`,
		},
		{
			name: "passed outcome has empty Reason",
			result: ValidationResult{
				Outcome: outcome{Type: OutcomeTypePassed, Passed: &passedOutcome{Reason: ""}},
			},
			wantErr: "passed outcome has empty Reason",
		},
		{
			name: "failed outcome has empty Reason",
			result: ValidationResult{
				Outcome: outcome{Type: OutcomeTypeFailed, Failed: &failedOutcome{Reason: ""}},
			},
			wantErr: "failed outcome has empty Reason",
		},
		{
			name: "unknown outcome has empty Reason",
			result: ValidationResult{
				Outcome: outcome{Type: OutcomeTypeUnknown, Unknown: &unknownOutcome{Reason: ""}},
			},
			wantErr: "unknown outcome has empty Reason",
		},
		{
			name: "skipped outcome has empty Reason",
			result: ValidationResult{
				Outcome: outcome{Type: OutcomeTypeSkipped, Skipped: &skippedOutcome{Reason: ""}},
			},
			wantErr: "skipped outcome has empty Reason",
		},
		{
			name: "unrecognized outcome Type",
			result: ValidationResult{
				Outcome: outcome{Type: OutcomeType("Bogus"), Passed: &passedOutcome{Reason: "R"}},
			},
			wantErr: `unrecognized outcome Type: "Bogus"`,
		},
		{
			name: "nil EarliestRetryAfter is valid",
			result: func() ValidationResult {
				result := FailedValidation("R", "u", "i")
				result.EarliestRetryAfter = nil
				return result
			}(),
		},
		{
			name: "zero EarliestRetryAfter is valid",
			result: func() ValidationResult {
				result := FailedValidation("R", "u", "i")
				result.EarliestRetryAfter = ptr.To(time.Duration(0))
				return result
			}(),
		},
		{
			name: "negative EarliestRetryAfter is invalid",
			result: func() ValidationResult {
				result := FailedValidation("R", "u", "i")
				result.EarliestRetryAfter = ptr.To(-time.Second)
				return result
			}(),
			wantErr: "EarliestRetryAfter must be >= 0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.result.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestValidationConstructors covers FailedValidation, PassedValidation, SkippedValidation, and
// UnknownValidation: the outcome Type they set, the values Reason()/InternalMessage()/ToCondition()
// surface, and that EarliestRetryAfter is populated.
func TestValidationConstructors(t *testing.T) {
	testCases := []struct {
		name            string
		construct       func() ValidationResult
		wantType        OutcomeType
		wantReason      string
		wantInternalMsg string
		wantUserMsg     string
		wantStatus      metav1.ConditionStatus
		// extra optionally asserts on fields specific to one constructor's outcome (e.g. Unknown's
		// ControllerReportingPolicy).
		extra func(t *testing.T, result ValidationResult)
	}{
		{
			name:            "FailedValidation",
			construct:       func() ValidationResult { return FailedValidation("FailReason", "user message", "internal message") },
			wantType:        OutcomeTypeFailed,
			wantReason:      "FailReason",
			wantInternalMsg: "internal message",
			wantUserMsg:     "user message",
			wantStatus:      metav1.ConditionFalse,
		},
		{
			name:            "PassedValidation",
			construct:       func() ValidationResult { return PassedValidation("PassReason", "user message", "internal message") },
			wantType:        OutcomeTypePassed,
			wantReason:      "PassReason",
			wantInternalMsg: "internal message",
			wantUserMsg:     "user message",
			wantStatus:      metav1.ConditionTrue,
		},
		{
			name:            "SkippedValidation",
			construct:       func() ValidationResult { return SkippedValidation("SkipReason", "user message", "internal message") },
			wantType:        OutcomeTypeSkipped,
			wantReason:      "SkipReason",
			wantInternalMsg: "internal message",
			wantUserMsg:     "user message",
			wantStatus:      metav1.ConditionUnknown,
		},
		{
			name: "UnknownValidation",
			construct: func() ValidationResult {
				return UnknownValidation("UnknownReason", "user message", "internal message", ControllerReportingPolicyTypeError)
			},
			wantType:        OutcomeTypeUnknown,
			wantReason:      "UnknownReason",
			wantInternalMsg: "internal message",
			wantUserMsg:     "user message",
			wantStatus:      metav1.ConditionUnknown,
			extra: func(t *testing.T, result ValidationResult) {
				require.NotNil(t, result.Outcome.Unknown, "Unknown payload was nil")
				assert.Equal(t, ControllerReportingPolicyTypeError, result.Outcome.Unknown.ControllerReportingPolicy)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.construct()

			assert.NoError(t, result.Validate())
			assert.Equal(t, tc.wantType, result.Outcome.Type)
			assert.Equal(t, tc.wantReason, result.Reason())
			assert.Equal(t, tc.wantInternalMsg, result.InternalMessage())

			cond := result.ToCondition("SomeCondition")
			assert.Equal(t, tc.wantStatus, cond.Status)
			assert.Equal(t, tc.wantReason, cond.Reason)
			assert.Equal(t, tc.wantUserMsg, cond.Message)

			assert.NotNil(t, result.EarliestRetryAfter, "EarliestRetryAfter was nil")

			if tc.extra != nil {
				tc.extra(t, result)
			}
		})
	}
}

func TestValidationResult_ToCondition(t *testing.T) {
	testCases := []struct {
		name          string
		result        ValidationResult
		conditionType string
		wantStatus    metav1.ConditionStatus
		wantReason    string
		wantMessage   string
	}{
		{
			name:          "passed outcome maps to status True",
			result:        PassedValidation("PassReason", "user message", "internal message"),
			conditionType: "AzureRPRegistration",
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "PassReason",
			wantMessage:   "user message",
		},
		{
			name:          "failed outcome maps to status False",
			result:        FailedValidation("FailReason", "user message", "internal message"),
			conditionType: "AzureRPRegistration",
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "FailReason",
			wantMessage:   "user message",
		},
		{
			name:          "skipped outcome maps to status Unknown",
			result:        SkippedValidation("SkipReason", "user message", "internal message"),
			conditionType: "AzureRPRegistration",
			wantStatus:    metav1.ConditionUnknown,
			wantReason:    "SkipReason",
			wantMessage:   "user message",
		},
		{
			name:          "unknown outcome maps to status Unknown",
			result:        UnknownValidation("UnknownReason", "user message", "internal message", ControllerReportingPolicyTypeLogOnly),
			conditionType: "AzureRPRegistration",
			wantStatus:    metav1.ConditionUnknown,
			wantReason:    "UnknownReason",
			wantMessage:   "user message",
		},
		{
			name:          "conditionType argument is passed through verbatim",
			result:        PassedValidation("PassReason", "user message", "internal message"),
			conditionType: "SomeOtherConditionType",
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "PassReason",
			wantMessage:   "user message",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cond := tc.result.ToCondition(tc.conditionType)
			assert.Equal(t, tc.conditionType, cond.Type)
			assert.Equal(t, tc.wantStatus, cond.Status)
			assert.Equal(t, tc.wantReason, cond.Reason)
			assert.Equal(t, tc.wantMessage, cond.Message)
		})
	}
}
