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

package base

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleetapi "github.com/Azure/ARO-HCP/internal/api/fleet"
)

func TestSetRegistrationCondition(t *testing.T) {
	conditionType := string(fleetapi.ManagementClusterConditionClustersServiceRegistered)

	tests := []struct {
		name               string
		existingConditions []metav1.Condition
		syncErr            error
		expectedStatus     metav1.ConditionStatus
		expectedReason     string
		expectedMessage    string
	}{
		{
			name:            "no error sets True/Registered",
			syncErr:         nil,
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  string(fleetapi.ManagementClusterConditionReasonRegistered),
			expectedMessage: "Registration successful",
		},
		{
			name: "error after previous True stays True with CheckFailed reason",
			existingConditions: []metav1.Condition{
				{
					Type:   conditionType,
					Status: metav1.ConditionTrue,
					Reason: string(fleetapi.ManagementClusterConditionReasonRegistered),
				},
			},
			syncErr:         errors.New("transient network error"),
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  string(fleetapi.ManagementClusterConditionReasonRegistrationCheckFailed),
			expectedMessage: "transient network error",
		},
		{
			name:            "error without previous condition sets False/RegistrationFailed",
			syncErr:         errors.New("connection refused"),
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  string(fleetapi.ManagementClusterConditionReasonRegistrationFailed),
			expectedMessage: "connection refused",
		},
		{
			name: "error after previous False stays False/RegistrationFailed",
			existingConditions: []metav1.Condition{
				{
					Type:   conditionType,
					Status: metav1.ConditionFalse,
					Reason: string(fleetapi.ManagementClusterConditionReasonRegistrationFailed),
				},
			},
			syncErr:         errors.New("still broken"),
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  string(fleetapi.ManagementClusterConditionReasonRegistrationFailed),
			expectedMessage: "still broken",
		},
		{
			name: "no error after previous False transitions to True",
			existingConditions: []metav1.Condition{
				{
					Type:   conditionType,
					Status: metav1.ConditionFalse,
					Reason: string(fleetapi.ManagementClusterConditionReasonRegistrationFailed),
				},
			},
			syncErr:         nil,
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  string(fleetapi.ManagementClusterConditionReasonRegistered),
			expectedMessage: "Registration successful",
		},
		{
			name: "no error after CheckFailed returns to Registered reason",
			existingConditions: []metav1.Condition{
				{
					Type:   conditionType,
					Status: metav1.ConditionTrue,
					Reason: string(fleetapi.ManagementClusterConditionReasonRegistrationCheckFailed),
				},
			},
			syncErr:         nil,
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  string(fleetapi.ManagementClusterConditionReasonRegistered),
			expectedMessage: "Registration successful",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := make([]metav1.Condition, len(tt.existingConditions))
			copy(conditions, tt.existingConditions)

			SetRegistrationCondition(&conditions, conditionType, tt.syncErr)

			require.Len(t, conditions, 1, "expected exactly one condition")
			condition := conditions[0]
			assert.Equal(t, conditionType, condition.Type)
			assert.Equal(t, tt.expectedStatus, condition.Status)
			assert.Equal(t, tt.expectedReason, condition.Reason)
			assert.Equal(t, tt.expectedMessage, condition.Message)
		})
	}
}
