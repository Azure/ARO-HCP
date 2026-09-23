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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsConditionTruePreferring reports whether the first of preferTypes present on conds is True.
// preferTypes are tried in order — most specific first — so callers pass the operation-specific
// condition (e.g. kubeapplierapi.ConditionTypeSuccessfullyDeleted) followed by the legacy
// kubeapplierapi.ConditionTypeSuccessful as the backwards-compatible fallback for documents last
// written by an older kube-applier. When none of preferTypes is present it returns false.
func IsConditionTruePreferring(conds []metav1.Condition, preferTypes ...string) bool {
	for _, conditionType := range preferTypes {
		if c := meta.FindStatusCondition(conds, conditionType); c != nil {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}
