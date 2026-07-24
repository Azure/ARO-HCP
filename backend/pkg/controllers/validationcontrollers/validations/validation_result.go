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

package validations

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
)

type ValidationResult struct {
	Outcome OutcomeType
	Failed  *FailedResult
	Unknown *UnknownResult
	// EarliestRetryAfter is the earliest time that a retry is allowed.  It is enforced.
	// Recommended for all OutcomeTypes.
	// For all Failed and Unknown, we will queue this plus one second
	// For Pass, we will get it on the next resync
	EarliestRetryAfter *time.Duration
}

const defaultEarliestRetryAfter = 60 * time.Second

// DefaultResult normalises a possibly-nil ValidationResult:
//   - A nil input is treated as Unknown with ReportingPolicyTypeError and a 60s retry.
//   - A non-nil input with no EarliestRetryAfter gets a 60s default.
func DefaultResult(r *ValidationResult) *ValidationResult {
	if r == nil {
		d := defaultEarliestRetryAfter
		return &ValidationResult{
			Outcome: OutcomeTypeUnknown,
			Unknown: &UnknownResult{
				Reason:                 "NoResult",
				ServiceProviderMessage: "validation returned nil result",
				UserMessage:            "Validation result unavailable.",
				ReportingPolicy:        ReportingPolicyTypeError,
			},
			EarliestRetryAfter: &d,
		}
	}
	if r.EarliestRetryAfter == nil {
		d := defaultEarliestRetryAfter
		r.EarliestRetryAfter = &d
	}
	return r
}

// ToCondition maps the ValidationResult to a metav1.Condition with the given condition type (typically the validation name).
func (r *ValidationResult) ToCondition(conditionType string) metav1.Condition {
	cond := metav1.Condition{
		Type: conditionType,
	}
	switch r.Outcome {
	case OutcomeTypePassed:
		cond.Status = metav1.ConditionTrue
		cond.Reason = "AsExpected"
		cond.Message = "As expected."
	case OutcomeTypeFailed:
		cond.Status = metav1.ConditionFalse
		cond.Reason = r.Failed.Reason
		cond.Message = r.Failed.UserMessage
	case OutcomeTypeUnknown:
		cond.Status = metav1.ConditionUnknown
		cond.Reason = r.Unknown.Reason
		cond.Message = r.Unknown.UserMessage
	}
	return cond
}

// ToSyncError converts the ValidationResult into the error value that a validation syncer's SyncOnce should return, driving the controller
// framework's requeue and metrics behaviour.
func (r *ValidationResult) ToSyncError(logger logr.Logger, validationName string) error {
	switch r.Outcome {
	case OutcomeTypePassed:
		return nil

	case OutcomeTypeFailed:
		syncErr := fmt.Errorf("validation %s failed: %s", validationName, r.Failed.Reason)
		if r.EarliestRetryAfter != nil {
			return &controllerutils.RequeueAfterError{
				Err:   syncErr,
				After: *r.EarliestRetryAfter + time.Second,
			}
		}
		return syncErr

	case OutcomeTypeUnknown:
		switch r.Unknown.ReportingPolicy {
		case ReportingPolicyTypeLogOnly:
			logger.Info("validation outcome unknown (log only)",
				"validation", validationName,
				"reason", r.Unknown.Reason,
				"message", r.Unknown.ServiceProviderMessage)
			if r.EarliestRetryAfter != nil {
				return &controllerutils.RequeueAfterError{
					After: *r.EarliestRetryAfter + time.Second,
				}
			}
			return nil

		case ReportingPolicyTypeError:
			syncErr := fmt.Errorf("validation %s unknown: %s", validationName, r.Unknown.Reason)
			if r.EarliestRetryAfter != nil {
				return &controllerutils.RequeueAfterError{
					Err:   syncErr,
					After: *r.EarliestRetryAfter + time.Second,
				}
			}
			return syncErr
		}
	}

	return nil
}

type OutcomeType string

var (
	// OutcomeTypePassed becomes a .status.validation.status = True, .reason=AsExpected, .message=As expected.
	OutcomeTypePassed OutcomeType = "Passed"
	// OutcomeTypeFailed becomes a .status.validation.status=False, .reason= validationResult.Failed.Reason, .message=validationResult.Failed.UserMessage
	OutcomeTypeFailed OutcomeType = "Failed"
	// OutcomeTypeUnknown becomes a .status.validation.status=Unknown, .reason= validationResult.Unknown.Reason, .message=validationResult.Unknown.UserMessage
	OutcomeTypeUnknown OutcomeType = "Unknown"
)

type FailedResult struct {
	// machine readable, must not be sensitive
	Reason string
	// human readable for serviceProvider
	ServiceProviderMessage string
	// human readable for user
	UserMessage string
}

type UnknownResult struct {
	// machine readable, must not be sensitive
	Reason string
	// human readable for serviceProvider
	ServiceProviderMessage string
	// human readable for user
	UserMessage string
	// ReportingPolicy indicates how the error should be treated.
	ReportingPolicy ReportingPolicyType
}

type ReportingPolicyType string

var (
	// ReportingPolicyTypeLogOnly means to return nil from the controller so it doesn't count in metrics. Useful for certain types of failures.
	ReportingPolicyTypeLogOnly ReportingPolicyType = "LogOnly"
	// ReportingPolicyTypeError indicates the controller should treat the result as an error for reporting/metrics purposes, while still respecting EarliestRetryAfter for scheduling.
	ReportingPolicyTypeError ReportingPolicyType = "ReportError"
)
