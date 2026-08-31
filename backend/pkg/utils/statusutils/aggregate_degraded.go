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

// AggregateExternalAuthDegradedCondition builds a single user-facing Degraded condition
// from multiple internal conditions. It is the Degraded-polarity counterpart
// of AggregateRequirementsValidCondition.
//
// Input is a filtered slice of internal conditions. Each condition's Type is
// used as the source name in the aggregated message ("Type: line").
//
// Result:
//   - Type is always conditionType.
//   - When every condition has Status=False (or the slice is empty):
//     Status=False, Reason=asExpectedReason, Message="".
//   - When at least one condition has Status True or Unknown: Status=True,
//     Reason=degradedReason, Message lists the non-False conditions sorted
//     by Type, formatted as "ConditionType: message-line".
func AggregateExternalAuthDegradedCondition(
	conditionType string,
	degradedReason string,
	asExpectedReason string,
	conditions []metav1.Condition,
) metav1.Condition {
	failed := make([]namedMessage, 0, len(conditions))
	for _, c := range conditions {
		if c.Status == metav1.ConditionFalse {
			continue
		}
		failed = append(failed, namedMessage{
			name:    c.Type,
			message: c.Message,
		})
	}
	sort.Slice(failed, func(i, j int) bool {
		return failed[i].name < failed[j].name
	})

	if len(failed) == 0 {
		return metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionFalse,
			Reason:  asExpectedReason,
			Message: "",
		}
	}

	return metav1.Condition{
		Type:    conditionType,
		Status:  metav1.ConditionTrue,
		Reason:  degradedReason,
		Message: joinNamedMessagesHeaderOnly(failed),
	}
}
