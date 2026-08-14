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

package kubeapplierapihelpers

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

func TestIsConditionTruePreferring(t *testing.T) {
	cond := func(condType string, status metav1.ConditionStatus) metav1.Condition {
		return metav1.Condition{Type: condType, Status: status}
	}

	for _, tc := range []struct {
		name        string
		conds       []metav1.Condition
		preferTypes []string
		want        bool
	}{
		{
			name:        "prefers first condition when present and True",
			conds:       []metav1.Condition{cond(kubeapplierapi.ConditionTypeSuccessfullyDeleted, metav1.ConditionTrue), cond(kubeapplierapi.ConditionTypeSuccessful, metav1.ConditionFalse)},
			preferTypes: []string{kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful},
			want:        true,
		},
		{
			name:        "prefers first condition when present and False (ignores later True)",
			conds:       []metav1.Condition{cond(kubeapplierapi.ConditionTypeSuccessfullyDeleted, metav1.ConditionFalse), cond(kubeapplierapi.ConditionTypeSuccessful, metav1.ConditionTrue)},
			preferTypes: []string{kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful},
			want:        false,
		},
		{
			name:        "falls back to legacy Successful=True when preferred absent",
			conds:       []metav1.Condition{cond(kubeapplierapi.ConditionTypeSuccessful, metav1.ConditionTrue)},
			preferTypes: []string{kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful},
			want:        true,
		},
		{
			name:        "falls back to legacy Successful=False when preferred absent",
			conds:       []metav1.Condition{cond(kubeapplierapi.ConditionTypeSuccessful, metav1.ConditionFalse)},
			preferTypes: []string{kubeapplierapi.ConditionTypeSuccessfullyApplied, kubeapplierapi.ConditionTypeSuccessful},
			want:        false,
		},
		{
			name:        "no listed conditions present is false",
			conds:       []metav1.Condition{cond(kubeapplierapi.ConditionTypeDegraded, metav1.ConditionFalse)},
			preferTypes: []string{kubeapplierapi.ConditionTypeSuccessfullyApplied, kubeapplierapi.ConditionTypeSuccessful},
			want:        false,
		},
		{
			name:        "nil conditions is false",
			conds:       nil,
			preferTypes: []string{kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful},
			want:        false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsConditionTruePreferring(tc.conds, tc.preferTypes...); got != tc.want {
				t.Errorf("IsConditionTruePreferring = %v, want %v", got, tc.want)
			}
		})
	}
}
