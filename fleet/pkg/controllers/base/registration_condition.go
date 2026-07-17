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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleetapi "github.com/Azure/ARO-HCP/internal/api/fleet"
)

// SetRegistrationCondition updates a registration condition based on the
// reconcile result. Once True, the condition never regresses to False —
// errors after a successful registration are recorded as True/CheckFailed
// so operators can observe the problem without losing the registration state.
//
// When the condition already carries the target Status and Reason, the
// update is skipped even if the Message differs. External API errors
// embed per-request fields (operation IDs, timestamps) that change on
// every call; writing a new Message each retry would bump the Cosmos
// etag, trigger an informer update event, and enqueue the key via Add()
// — bypassing the workqueue rate limiter and creating a hot retry loop.
// The full error is still logged by the workqueue error handler, so no
// debugging information is lost.
func SetRegistrationCondition(conditions *[]metav1.Condition, conditionType string, syncErr error) {
	var desired metav1.Condition
	switch {
	case syncErr == nil:
		desired = metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionTrue,
			Reason:  string(fleetapi.ManagementClusterConditionReasonRegistered),
			Message: "Registration successful",
		}
	case apimeta.IsStatusConditionTrue(*conditions, conditionType):
		desired = metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionTrue,
			Reason:  string(fleetapi.ManagementClusterConditionReasonRegistrationCheckFailed),
			Message: syncErr.Error(),
		}
	default:
		desired = metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionFalse,
			Reason:  string(fleetapi.ManagementClusterConditionReasonRegistrationFailed),
			Message: syncErr.Error(),
		}
	}

	existing := apimeta.FindStatusCondition(*conditions, conditionType)
	if existing != nil && existing.Status == desired.Status && existing.Reason == desired.Reason {
		return
	}

	apimeta.SetStatusCondition(conditions, desired)
}
