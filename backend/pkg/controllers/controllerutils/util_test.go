// Copyright 2025 Microsoft Corporation
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

package controllerutils

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestGetCondition(t *testing.T) {
	tests := []struct {
		name          string
		conditions    []api.Condition
		conditionType string
		wantFound     bool
		wantMessage   string
	}{
		{
			name:          "returns nil for nil slice",
			conditions:    nil,
			conditionType: "Degraded",
			wantFound:     false,
		},
		{
			name:          "returns nil when type not found",
			conditions:    []api.Condition{{Type: "Available", Status: api.ConditionTrue, Message: "Available"}},
			conditionType: "Degraded",
			wantFound:     false,
		},
		{
			name:          "returns first match when multiple have same type",
			conditions:    []api.Condition{{Type: "Degraded", Status: api.ConditionTrue, Message: "first"}, {Type: "Degraded", Status: api.ConditionFalse, Message: "second"}},
			conditionType: "Degraded",
			wantFound:     true,
			wantMessage:   "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := GetCondition(tt.conditions, tt.conditionType)
			require.Equal(t, tt.wantFound, res != nil)
			if tt.wantFound {
				require.Equal(t, tt.wantMessage, res.Message)
			}
			if len(tt.wantMessage) > 0 {
				require.Equal(t, tt.wantMessage, res.Message)
			}
		})
	}

	t.Run("a reference to the returned condition is returned", func(t *testing.T) {
		conditions := []api.Condition{
			{Type: "Available", Status: api.ConditionTrue, Message: "Available"},
			{Type: "Degraded", Status: api.ConditionTrue, Reason: "NoErrors", Message: "As expected."},
		}
		wantCondition := &conditions[1]

		res := GetCondition(conditions, "Degraded")
		// We intentionally perform a pointer comparison to check that the returned condition is a reference to the found one in the list.
		if res != wantCondition {
			t.Errorf("returned condition is not a reference to the found one in the list")
		}
	})
}

func TestSetCondition(t *testing.T) {
	tests := []struct {
		name                 string
		conditions           []api.Condition
		toSet                api.Condition
		wantLen              int
		wantConditionMessage string
	}{
		{
			name:                 "adds condition when slice is nil",
			conditions:           nil,
			toSet:                api.Condition{Type: "Degraded", Status: api.ConditionFalse, Reason: "NoErrors", Message: "As expected."},
			wantLen:              1,
			wantConditionMessage: "As expected.",
		},
		{
			name:                 "adds condition when type not found",
			conditions:           []api.Condition{{Type: "Available", Status: api.ConditionTrue}},
			toSet:                api.Condition{Type: "Degraded", Status: api.ConditionFalse, Reason: "NoErrors", Message: "As expected."},
			wantLen:              2,
			wantConditionMessage: "As expected.",
		},
		{
			name:                 "modifies existing condition when found",
			conditions:           []api.Condition{{Type: "Degraded", Status: api.ConditionTrue, Reason: "Failed", Message: "Had an error"}},
			toSet:                api.Condition{Type: "Degraded", Status: api.ConditionFalse, Reason: "NoErrors", Message: "As expected."},
			wantLen:              1,
			wantConditionMessage: "As expected.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := tt.conditions
			SetCondition(&conditions, tt.toSet)
			require.Len(t, conditions, tt.wantLen)
			retrievedCondition := GetCondition(conditions, tt.toSet.Type)
			require.NotNil(t, retrievedCondition)
			require.Equal(t, tt.wantConditionMessage, retrievedCondition.Message)
		})
	}
}

func TestIsConditionTrue(t *testing.T) {
	tests := []struct {
		name          string
		conditions    []api.Condition
		conditionType string
		want          bool
	}{
		{"returns false for nil conditions", nil, "Degraded", false},
		{
			name:          "returns false when condition not found",
			conditions:    []api.Condition{{Type: "Available", Status: api.ConditionTrue}},
			conditionType: "Degraded",
			want:          false,
		},
		{
			name:          "returns false when condition found and its status is False",
			conditions:    []api.Condition{{Type: "Degraded", Status: api.ConditionFalse}},
			conditionType: "Degraded",
			want:          false,
		},
		{
			name:          "returns true when condition found and status is True",
			conditions:    []api.Condition{{Type: "Degraded", Status: api.ConditionTrue}},
			conditionType: "Degraded",
			want:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsConditionTrue(tt.conditions, tt.conditionType)
			if got != tt.want {
				t.Errorf("IsConditionTrue() = %v, want %v", got, tt.want)
			}
		})
	}
}
