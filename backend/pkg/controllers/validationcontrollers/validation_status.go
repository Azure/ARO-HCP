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

package validationcontrollers

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
)

func validationResultToStatus(validationType string, result validations.ValidationResult, now time.Time) api.ValidationStatus {
	vs := api.ValidationStatus{
		Type: validationType,
		Condition: metav1.Condition{
			Type:               validationType,
			LastTransitionTime: metav1.NewTime(now),
		},
	}

	switch result.Outcome.Type {
	case validations.OutcomePassed:
		vs.Condition.Status = metav1.ConditionTrue
		vs.Condition.Reason = "Succeeded"
		if result.Outcome.Passed != nil && result.Outcome.Passed.UserMessage != "" {
			vs.Condition.Message = result.Outcome.Passed.UserMessage
		} else {
			vs.Condition.Message = "Validation succeeded"
		}
	case validations.OutcomeFailed:
		vs.Condition.Status = metav1.ConditionFalse
		if result.Outcome.Failed != nil && result.Outcome.Failed.Reason != "" {
			vs.Condition.Reason = result.Outcome.Failed.Reason
		} else {
			vs.Condition.Reason = "Failed"
		}
		if result.Outcome.Failed != nil && result.Outcome.Failed.UserMessage != "" {
			vs.Condition.Message = result.Outcome.Failed.UserMessage
		} else if result.Outcome.Failed != nil && result.Outcome.Failed.Reason != "" {
			vs.Condition.Message = fmt.Sprintf("Validation failed: %s", result.Outcome.Failed.Reason)
		} else {
			vs.Condition.Message = "Validation failed"
		}
	case validations.OutcomeUnknown:
		vs.Condition.Status = metav1.ConditionUnknown
		if result.Outcome.Unknown != nil && result.Outcome.Unknown.Reason != "" {
			vs.Condition.Reason = result.Outcome.Unknown.Reason
		} else {
			vs.Condition.Reason = "Unknown"
		}
		if result.Outcome.Unknown != nil && result.Outcome.Unknown.UserMessage != "" {
			vs.Condition.Message = result.Outcome.Unknown.UserMessage
		} else if result.Outcome.Unknown != nil && result.Outcome.Unknown.Reason != "" {
			vs.Condition.Message = fmt.Sprintf("Validation state is unknown: %s", result.Outcome.Unknown.Reason)
		} else {
			vs.Condition.Message = "Validation state is unknown"
		}
	default:
		vs.Condition.Status = metav1.ConditionUnknown
		vs.Condition.Reason = "Unknown"
		vs.Condition.Message = "Validation state is unknown"
	}

	vs.Internal.Outcome = string(result.Outcome.Type)
	if result.EarliestRetryAfter != nil {
		seconds := int64(result.EarliestRetryAfter.Seconds())
		vs.Internal.EarliestRetryAfterSeconds = &seconds
	}
	if result.Outcome.Failed != nil {
		vs.Internal.ServiceProviderMessage = result.Outcome.Failed.ServiceProviderMessage
		vs.Internal.ReportingPolicy = ""
	}
	if result.Outcome.Unknown != nil {
		vs.Internal.ServiceProviderMessage = result.Outcome.Unknown.ServiceProviderMessage
		vs.Internal.ReportingPolicy = string(result.Outcome.Unknown.ReportingPolicy)
	}

	return vs
}

func upsertValidationStatus(list []api.ValidationStatus, updated api.ValidationStatus) []api.ValidationStatus {
	out := make([]api.ValidationStatus, 0, len(list)+1)
	replaced := false
	for _, existing := range list {
		if existing.Type == updated.Type {
			// Preserve lastTransitionTime unless the condition Status changed.
			// This matches metav1.Condition conventions: LastTransitionTime tracks status transitions,
			// not message or reason updates.
			if existing.Condition.Status == updated.Condition.Status {
				updated.Condition.LastTransitionTime = existing.Condition.LastTransitionTime
			}
			out = append(out, updated)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, updated)
	}
	return out
}

func validationResultToSyncResult(result validations.ValidationResult) controllerutil.SyncResult {
	if result.EarliestRetryAfter == nil {
		return controllerutil.SyncResult{}
	}
	return controllerutil.SyncResult{RequeueAfter: *result.EarliestRetryAfter + time.Second}
}

func validationResultToError(result validations.ValidationResult) error {
	if result.Outcome.Type != validations.OutcomeUnknown || result.Outcome.Unknown == nil {
		return nil
	}
	if result.Outcome.Unknown.ReportingPolicy != validations.ReportingPolicyReportError {
		return nil
	}
	if result.Outcome.Unknown.Reason == "" {
		return fmt.Errorf("validation unknown")
	}
	return fmt.Errorf("validation unknown: %s", result.Outcome.Unknown.Reason)
}
