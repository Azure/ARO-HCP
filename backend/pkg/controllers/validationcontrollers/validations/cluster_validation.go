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
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// ClusterValidation represents a validation that can be performed on a cluster.
type ClusterValidation interface {
	// Name returns the name of the validation.
	Name() string
	// Validate validates the Cluster.
	// nil validation result is treated as a validation Unknown, Reason nil result, EarliestRetryAfter 60s
	Validate(ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster) *ValidationResult
}

type ValidationResult struct {
	Outcome OutcomeType

	Failed *FailedResult

	Unknown *UnknownResult

	// EarliestRetryAfter is the earliest time that a retry is allowed.  It is enforced.
	// Recommended for all OutcomeTypes.
	// For all Failed and Unknown, we will queue this plus one second
	// For Pass, we will get it on the next resync
	EarliestRetryAfter *time.Duration
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

	// ReportingPolicyTypeLogOnly means to return nil from the controller so it doesn't count in metrics.  Useful for certain types of failures.
	ReportingPolicyTypeLogOnly ReportingPolicyType = "LogOnly"
	// ReportingPolicyTypeError will return an error for rapid retry, but the EarliestRetryAfter will prevent rapid retry and queue (without error) for a time after that.
	ReportingPolicyTypeError ReportingPolicyType = "ReportError"
)

// DefaultResult returns a non-nil result with defaults applied. A nil input
// is treated as Unknown with a 60s retry. A non-nil input with no
// EarliestRetryAfter gets a 60s default.
func DefaultResult(result *ValidationResult) *ValidationResult {
	if result == nil {
		return &ValidationResult{
			Outcome: OutcomeTypeUnknown,
			Unknown: &UnknownResult{
				Reason:                 "NilResult",
				ServiceProviderMessage: "Validation returned nil result.",
				UserMessage:            "Validation status is unknown.",
				ReportingPolicy:        ReportingPolicyTypeError,
			},
			EarliestRetryAfter: ptr.To(60 * time.Second),
		}
	}
	if result.EarliestRetryAfter == nil {
		result.EarliestRetryAfter = ptr.To(60 * time.Second)
	}
	return result
}

// BuildCondition converts a ValidationResult into a metav1.Condition with the
// given condition type (typically the validation's Name()).
func BuildCondition(conditionType string, result *ValidationResult) metav1.Condition {
	switch result.Outcome {
	case OutcomeTypePassed:
		return metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionTrue,
			Reason:  "AsExpected",
			Message: "As expected.",
		}
	case OutcomeTypeFailed:
		return metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionFalse,
			Reason:  result.Failed.Reason,
			Message: result.Failed.UserMessage,
		}
	default:
		reason := "Unknown"
		message := "Validation status is unknown."
		if result.Unknown != nil {
			reason = result.Unknown.Reason
			message = result.Unknown.UserMessage
		}
		return metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionUnknown,
			Reason:  reason,
			Message: message,
		}
	}
}
