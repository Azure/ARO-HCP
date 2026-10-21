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
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestValidationCondition(name string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               name,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(FixedNow.Add(-time.Minute)),
	}
}

func TestAggregateRequirementsValidCondition(t *testing.T) {
	validCondition := metav1.Condition{
		Type:    RequirementsValidConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  RequirementsValidConditionReasonValid,
		Message: "",
	}

	testCases := []struct {
		name          string
		validations   []metav1.Condition
		wantCondition metav1.Condition
	}{
		{
			name:          "no validations writes True/Valid with empty message",
			validations:   nil,
			wantCondition: validCondition,
		},
		{
			name: "all validations succeeded writes True/Valid",
			validations: []metav1.Condition{
				newTestValidationCondition("AValidation", metav1.ConditionTrue, "Succeeded", "Validation succeeded"),
				newTestValidationCondition("BValidation", metav1.ConditionTrue, "Succeeded", "Validation succeeded"),
			},
			wantCondition: validCondition,
		},
		{
			name: "one failed validation writes False/Degraded with that message",
			validations: []metav1.Condition{
				newTestValidationCondition("AValidation", metav1.ConditionTrue, "Succeeded", "Validation succeeded"),
				newTestValidationCondition("BValidation", metav1.ConditionFalse, "Failed", "Validation failed: boom"),
			},
			wantCondition: metav1.Condition{
				Type:    RequirementsValidConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  RequirementsValidConditionReasonDegraded,
				Message: "BValidation: Validation failed: boom",
			},
		},
		{
			name: "multiple failed validations are sorted and joined like UnionCondition",
			validations: []metav1.Condition{
				newTestValidationCondition("ZValidation", metav1.ConditionFalse, "Failed", "Validation failed: zed"),
				newTestValidationCondition("AValidation", metav1.ConditionFalse, "Failed", "Validation failed: aye"),
				newTestValidationCondition("MValidation", metav1.ConditionTrue, "Succeeded", "Validation succeeded"),
			},
			wantCondition: metav1.Condition{
				Type:    RequirementsValidConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  RequirementsValidConditionReasonDegraded,
				Message: "AValidation: Validation failed: aye\nZValidation: Validation failed: zed",
			},
		},
		{
			name: "multi-line failed messages are expanded per line with source prefix",
			validations: []metav1.Condition{
				newTestValidationCondition("AValidation", metav1.ConditionFalse, "Failed", "line one\nline two"),
			},
			wantCondition: metav1.Condition{
				Type:    RequirementsValidConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  RequirementsValidConditionReasonDegraded,
				Message: "AValidation: line one\nAValidation: line two",
			},
		},
		{
			name: "Unknown validations count as Degraded",
			validations: []metav1.Condition{
				newTestValidationCondition("AValidation", metav1.ConditionUnknown, "Pending", "still running"),
			},
			wantCondition: metav1.Condition{
				Type:    RequirementsValidConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  RequirementsValidConditionReasonDegraded,
				Message: "AValidation: still running",
			},
		},
		{
			name: "False and Unknown validations are both included in the message",
			validations: []metav1.Condition{
				newTestValidationCondition("AValidation", metav1.ConditionTrue, "Succeeded", "Validation succeeded"),
				newTestValidationCondition("BValidation", metav1.ConditionUnknown, "Pending", "still running"),
				newTestValidationCondition("CValidation", metav1.ConditionFalse, "Failed", "Validation failed: boom"),
			},
			wantCondition: metav1.Condition{
				Type:    RequirementsValidConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  RequirementsValidConditionReasonDegraded,
				Message: "BValidation: still running\nCValidation: Validation failed: boom",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateRequirementsValidCondition(tc.validations)
			// Only Type/Status/Reason/Message are asserted; LastTransitionTime is not considered.
			require.Equal(t, tc.wantCondition.Type, got.Type, "type")
			assert.Equal(t, tc.wantCondition.Status, got.Status, "status")
			assert.Equal(t, tc.wantCondition.Reason, got.Reason, "reason")
			assert.Equal(t, tc.wantCondition.Message, got.Message, "message")
		})
	}
}
