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
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// RequirementsValidConditionType is the user-facing condition type that
	// aggregates ServiceProvider* Status.Validations onto the parent resource's
	// Status.UserFacingConditions.
	RequirementsValidConditionType = "RequirementsValid"

	// RequirementsValidConditionReasonValid is set when every known validation
	// has Status=True (or there are no validations yet). Paired with
	// Status=True and an empty Message.
	RequirementsValidConditionReasonValid = "Valid"
	// RequirementsValidConditionReasonDegraded is set when at least one
	// validation has Status False or Unknown. Paired with Status=False and a
	// Message that enumerates those non-True validations.
	RequirementsValidConditionReasonDegraded = "Degraded"
)

// AggregateRequirementsValidCondition builds the single RequirementsValid
// condition that RequirementsValid aggregators write onto
// status.userFacingConditions.
//
// Input is the ServiceProvider* Status.Validations slice.
//
// Result:
//   - Type is always RequirementsValid.
//   - When every validation has Status=True (or the slice is empty):
//     Status=True, Reason=Valid, Message empty.
//   - When at least one validation has Status False or Unknown: Status=False,
//     Reason=Degraded, Message lists only those non-True validations, sorted
//     by Type and formatted by joinNamedMessages as "ValidationName: <line>"
//     (one line per unique message line), joined with newlines.
func AggregateRequirementsValidCondition(validations []metav1.Condition) metav1.Condition {
	failed := make([]namedMessage, 0, len(validations))
	for _, validation := range validations {
		if validation.Status == metav1.ConditionTrue {
			continue
		}
		failed = append(failed, namedMessage{
			name:    validation.Type,
			message: validation.Message,
		})
	}
	sort.Slice(failed, func(i, j int) bool {
		return failed[i].name < failed[j].name
	})

	if len(failed) == 0 {
		return metav1.Condition{
			Type:    RequirementsValidConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  RequirementsValidConditionReasonValid,
			Message: "",
		}
	}

	return metav1.Condition{
		Type:    RequirementsValidConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  RequirementsValidConditionReasonDegraded,
		Message: joinNamedMessages(failed),
	}
}
