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

package validationutils

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

const (
	// nonpassedRetryBase is the base retry delay for non-passed outcomes (Failed, Unknown, Skipped).
	nonpassedRetryBase = 60 * time.Second
	// passedRetryBase is the base retry delay for passed outcomes.
	passedRetryBase = 12 * time.Hour
	// jitterFactor is the maxFactor passed to wait.Jitter; 0.5 means up to +50% of the base.
	jitterFactor = 0.5
	// maxNonPassedRetryDelay is the longest delay FailedValidation, UnknownValidation, and
	// SkippedValidation schedule before the next run (nonpassedRetryBase plus maximum jitter).
	maxNonPassedRetryDelay = time.Duration(float64(nonpassedRetryBase) * (1 + jitterFactor))
)

// NonPassedRetryDelay returns the jittered retry delay FailedValidation, UnknownValidation, and
// SkippedValidation use, in [nonpassedRetryBase, maxNonPassedRetryDelay]. Jitter avoids retry storms.
func NonPassedRetryDelay() time.Duration {
	return wait.Jitter(nonpassedRetryBase, jitterFactor)
}

const (
	// ValidationRunTimeout bounds a single validation run. Without it a hung Azure call blocks the
	// worker indefinitely and the validation silently stops producing fresh results. It also keeps a
	// recorded probe time (the run's start) close to when Azure was actually observed.
	ValidationRunTimeout = 2 * time.Minute

	// ValidationProbeRewriteInterval throttles how often a still-failing validation rewrites its probe
	// time, so a continuously failing validation does not turn every retry into a Cosmos write.
	//
	// With the default retry delays (nonpassedRetryBase plus non-negative jitter, measured from the
	// end of the previous run) consecutive failing runs usually start less than this interval apart,
	// so the throttle skips roughly every other rewrite. It also caps writes when a run happens
	// sooner than that: a validation that overrides EarliestRetryAfter with a shorter delay, or an
	// immediate re-run after a backend restart or leader change drops the in-memory retry cooldown.
	ValidationProbeRewriteInterval = ValidationRunTimeout

	// validationProbeLagSlack absorbs workqueue and Cosmos write latency that the probe lag budget
	// below does not model explicitly.
	validationProbeLagSlack = 30 * time.Second

	// ValidationProbeFreshnessWindow bounds how old a validation's recorded probe (the start time of
	// its most recent conclusive Failed run, stored in ServiceProviderCluster.Status.ValidationProbeTimes)
	// may be while its False condition still counts toward terminally failing a create or update
	// operation. A probe older than this means the check has not actually run recently (stalled
	// controller, workqueue backlog, or runs that keep timing out), so the stored failure is stale
	// evidence rather than a freshly confirmed one.
	//
	// The window is derived from the other bounds so that a validation re-checking on schedule
	// never looks stale. The rewrite throttle is anchored on the last successful probe write, and
	// the stored probe is the start time of the run that wrote it: that run can itself run for up
	// to ValidationRunTimeout before its write lands, so the first rewrite after it completes no
	// earlier than ValidationRunTimeout (the writing run's own duration) +
	// ValidationProbeRewriteInterval after the stored probe. If a run completes just before that
	// bound, it does not rewrite, and the next run starts up to one retry delay later and runs up
	// to one run timeout longer. The worst-case stored probe age while re-checks continue is
	// therefore ValidationRunTimeout + max(ValidationProbeRewriteInterval, ValidationRunTimeout) +
	// maxNonPassedRetryDelay + ValidationRunTimeout; the compile-time guard below keeps the middle
	// max at ValidationRunTimeout.
	// A run that times out is Unknown and does not rewrite, which correctly lets the probe go
	// stale; the same holds for a rewrite lost to a write conflict until the next run lands.
	ValidationProbeFreshnessWindow = 3*ValidationRunTimeout + maxNonPassedRetryDelay + validationProbeLagSlack
)

// Fails to compile if ValidationProbeRewriteInterval exceeds ValidationRunTimeout: the operand
// becomes negative, which cannot be converted to an unsigned constant. The freshness window
// derivation above assumes the rewrite interval is no longer than a run.
const _ uint64 = uint64(ValidationRunTimeout - ValidationProbeRewriteInterval)

// Fails to compile if the rewrite interval is not positive; a zero interval would rewrite on
// every run and defeat the write throttle.
const _ uint64 = uint64(ValidationProbeRewriteInterval - 1)

// OutcomeType discriminates which validation outcome payload is populated.
type OutcomeType string

const (
	// OutcomeTypePassed becomes a .status.validation.status=True, .reason=Passed.Reason, .message=Passed.UserMessage.
	OutcomeTypePassed OutcomeType = "Passed"
	// OutcomeTypeFailed becomes a .status.validation.status=False, .reason=Failed.Reason, .message=Failed.UserMessage.
	OutcomeTypeFailed OutcomeType = "Failed"
	// OutcomeTypeUnknown becomes a .status.validation.status=Unknown, .reason=Unknown.Reason, .message=Unknown.UserMessage.
	OutcomeTypeUnknown OutcomeType = "Unknown"
	// OutcomeTypeSkipped becomes a .status.validation.status=Unknown, .reason=Skipped.Reason, .message=Skipped.UserMessage.
	OutcomeTypeSkipped OutcomeType = "Skipped"
)

// ValidationResult is the result of a single validation check. Controllers map it to a status
// condition via ToCondition. Requeue scheduling is handled explicitly by the controller using
// SettableCooldownChecker and AfterEnqueuer based on EarliestRetryAfter.
//
// Do not instantiate this directly. Build one with FailedValidation, PassedValidation,
// SkippedValidation, or UnknownValidation. Those helpers set sensible defaults:
// FailedValidation, UnknownValidation, and SkippedValidation set EarliestRetryAfter to
// nonPassedRetryBase + jitter; PassedValidation sets it to passedRetryBase + jitter.
// If further customization is needed, EarliestRetryAfter can be overridden to a different value.
//
// For example:
//
//	result := validationutils.FailedValidation("ValidationFailed", "The validation failed.", "The validation failed.")
//	result.EarliestRetryAfter = ptr.To(60 * time.Second)
//
//	return result
type ValidationResult struct {
	// Outcome is exactly one of Passed, Failed, Unknown, or Skipped, as indicated by its Type.
	Outcome outcome
	// EarliestRetryAfter is an optional retry throttle for this validation result.
	//
	// It affects the validation controller in two ways:
	//  1. Earliest-retry gate: when a sync event arrives, the controller skips the
	//     actual validation work until EarliestRetryAfter has elapsed since the
	//     previous attempt.
	//  2. Workqueue requeue: whether the reconciled key is re-added to the workqueue
	//     after a delay, so a retry is scheduled without waiting for an external event.
	//
	// Semantics:
	//
	//   - nil: neither the earliest-retry gate nor a workqueue requeue is applied.
	//
	//   - non-nil, Outcome Passed or Skipped:
	//       - earliest-retry gate uses EarliestRetryAfter
	//       - key is not requeued
	//
	//   - non-nil, Outcome Failed or Unknown:
	//       - earliest-retry gate uses EarliestRetryAfter
	//       - key is requeued after EarliestRetryAfter + 1s.
	//         The extra 1s is only on the workqueue delay, not the gate. It avoids
	//         landing on the gate boundary: duration+1s makes the next attempt
	//         run strictly after the gate opens. Otherwise the sync could occur
	//         too early, become a no-op, and never schedule another attempt.
	EarliestRetryAfter *time.Duration
}

// Reason returns the machine-readable reason string for the outcome.
func (r ValidationResult) Reason() string {
	switch r.Outcome.Type {
	case OutcomeTypeFailed:
		return r.Outcome.Failed.Reason
	case OutcomeTypeUnknown:
		return r.Outcome.Unknown.Reason
	case OutcomeTypeSkipped:
		return r.Outcome.Skipped.Reason
	case OutcomeTypePassed:
		return r.Outcome.Passed.Reason
	}
	return ""
}

// InternalMessage returns the human-readable internal message for the outcome,
// intended for logs and diagnostics (not surfaced to the user).
func (r ValidationResult) InternalMessage() string {
	switch r.Outcome.Type {
	case OutcomeTypeFailed:
		return r.Outcome.Failed.InternalMessage
	case OutcomeTypeUnknown:
		return r.Outcome.Unknown.InternalMessage
	case OutcomeTypeSkipped:
		return r.Outcome.Skipped.InternalMessage
	case OutcomeTypePassed:
		return r.Outcome.Passed.InternalMessage
	}
	return ""
}

// Validate checks that the outcome is well-formed: exactly one payload is set and it matches the declared Type. Controllers should call this before acting
// on the result to fail fast on programmer errors.
func (r ValidationResult) Validate() error {
	count := 0
	if r.Outcome.Passed != nil {
		count++
	}
	if r.Outcome.Failed != nil {
		count++
	}
	if r.Outcome.Unknown != nil {
		count++
	}
	if r.Outcome.Skipped != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one outcome to be set, got %d", count)
	}

	switch r.Outcome.Type {
	case OutcomeTypePassed:
		if r.Outcome.Passed == nil {
			return fmt.Errorf("outcome Type is %q but Passed payload is nil", r.Outcome.Type)
		}
		if r.Outcome.Passed.Reason == "" {
			return fmt.Errorf("passed outcome has empty Reason")
		}
	case OutcomeTypeFailed:
		if r.Outcome.Failed == nil {
			return fmt.Errorf("outcome Type is %q but Failed payload is nil", r.Outcome.Type)
		}
		if r.Outcome.Failed.Reason == "" {
			return fmt.Errorf("failed outcome has empty Reason")
		}
	case OutcomeTypeUnknown:
		if r.Outcome.Unknown == nil {
			return fmt.Errorf("outcome Type is %q but Unknown payload is nil", r.Outcome.Type)
		}
		if r.Outcome.Unknown.Reason == "" {
			return fmt.Errorf("unknown outcome has empty Reason")
		}
	case OutcomeTypeSkipped:
		if r.Outcome.Skipped == nil {
			return fmt.Errorf("outcome Type is %q but Skipped payload is nil", r.Outcome.Type)
		}
		if r.Outcome.Skipped.Reason == "" {
			return fmt.Errorf("skipped outcome has empty Reason")
		}
	default:
		return fmt.Errorf("unrecognized outcome Type: %q", r.Outcome.Type)
	}

	if r.EarliestRetryAfter != nil && *r.EarliestRetryAfter < 0 {
		return fmt.Errorf("EarliestRetryAfter must be >= 0, got %s", *r.EarliestRetryAfter)
	}
	return nil
}

// ToCondition maps the validationResult to a metav1.Condition with the given condition type (typically the validation name).
func (r ValidationResult) ToCondition(conditionType string) metav1.Condition {
	cond := metav1.Condition{
		Type: conditionType,
	}
	switch r.Outcome.Type {
	case OutcomeTypePassed:
		cond.Status = metav1.ConditionTrue
		cond.Reason = r.Outcome.Passed.Reason
		cond.Message = r.Outcome.Passed.UserMessage
	case OutcomeTypeFailed:
		cond.Status = metav1.ConditionFalse
		cond.Reason = r.Outcome.Failed.Reason
		cond.Message = r.Outcome.Failed.UserMessage
	case OutcomeTypeSkipped:
		cond.Status = metav1.ConditionUnknown
		cond.Reason = r.Outcome.Skipped.Reason
		cond.Message = r.Outcome.Skipped.UserMessage
	case OutcomeTypeUnknown:
		unknown := r.Outcome.Unknown
		cond.Status = metav1.ConditionUnknown
		cond.Reason = unknown.Reason
		cond.Message = unknown.UserMessage
	}
	return cond
}

// outcome is the outcome of a validation: exactly one of Passed, Failed, Unknown, or Skipped, as
// indicated by Type. Construct one via the FailedValidation, PassedValidation, SkippedValidation, or
// UnknownValidation helpers — never build an outcome literal directly. Because outcome itself is
// unexported, those helpers (and the validationResult they return) are the only way for callers
// outside this package to produce one, which guarantees Type can never disagree with the populated
// payload.
type outcome struct {
	// Type discriminates which payload field is populated. Exactly one of Passed, Failed, Unknown, or Skipped will be non-nil, matching this value.
	Type OutcomeType

	// Failed is set when the validation deterministically failed (e.g. quota exceeded).
	Failed *failedOutcome
	// Unknown is set when the validation could not reach a conclusive result (e.g. transient Azure error).
	Unknown *unknownOutcome
	// Passed is set when the validation succeeded.
	Passed *passedOutcome
	// Skipped is set when the validation was intentionally not evaluated.
	Skipped *skippedOutcome
}

// failedOutcome indicates the validation determinately failed. It becomes a
// .status.validation.status=False, .reason=Reason, .message=UserMessage.
type failedOutcome struct {
	// machine readable, must not be sensitive
	Reason string
	// human readable, for internal use (e.g. logs); not surfaced to the user
	InternalMessage string
	// human readable for user
	UserMessage string
}

// FailedValidation returns a validationResult indicating the validation failed.
// EarliestRetryAfter is set to nonPassedRetryBase + jitter.
func FailedValidation(reason string, userMessage string, internalMessage string) ValidationResult {
	result := ValidationResult{
		Outcome: outcome{
			Type: OutcomeTypeFailed,
			Failed: &failedOutcome{
				Reason:          reason,
				InternalMessage: internalMessage,
				UserMessage:     userMessage,
			}},
	}
	// Jitter avoids retry storms: wait.Jitter(base, 0.5) returns a value in [base, base*1.5].
	retryWithJitter := wait.Jitter(nonpassedRetryBase, jitterFactor)
	result.EarliestRetryAfter = ptr.To(retryWithJitter)
	return result
}

// passedOutcome indicates the validation succeeded. It becomes a
// .status.validation.status=True, .reason=Reason, .message=UserMessage.
type passedOutcome struct {
	// machine readable, must not be sensitive
	Reason string
	// human readable, for internal use (e.g. logs); not surfaced to the user
	InternalMessage string
	// human readable for user
	UserMessage string
}

// PassedValidation returns a validationResult indicating the validation passed.
// EarliestRetryAfter is set to passedRetryBase + jitter.
func PassedValidation(reason string, userMessage string, internalMessage string) ValidationResult {
	result := ValidationResult{
		Outcome: outcome{
			Type: OutcomeTypePassed,
			Passed: &passedOutcome{
				Reason:          reason,
				InternalMessage: internalMessage,
				UserMessage:     userMessage,
			},
		},
	}
	// Jitter avoids retry storms: wait.Jitter(base, 0.5) returns a value in [base, base*1.5].
	retryWithJitter := wait.Jitter(passedRetryBase, jitterFactor)
	result.EarliestRetryAfter = ptr.To(retryWithJitter)
	return result
}

// skippedOutcome indicates the validation was not evaluated. It becomes a
// .status.validation.status=Unknown, .reason=Reason, .message=UserMessage.
type skippedOutcome struct {
	// machine readable, must not be sensitive
	Reason string
	// human readable, for internal use (e.g. logs); not surfaced to the user
	InternalMessage string
	// human readable for user
	UserMessage string
}

// SkippedValidation returns a validationResult indicating the validation was not evaluated.
// EarliestRetryAfter is set to nonPassedRetryBase + jitter.
func SkippedValidation(reason string, userMessage string, internalMessage string) ValidationResult {
	result := ValidationResult{
		Outcome: outcome{
			Type: OutcomeTypeSkipped,
			Skipped: &skippedOutcome{
				Reason:          reason,
				InternalMessage: internalMessage,
				UserMessage:     userMessage,
			},
		},
	}
	// Jitter avoids retry storms: wait.Jitter(base, 0.5) returns a value in [base, base*1.5].
	retryWithJitter := wait.Jitter(nonpassedRetryBase, jitterFactor)
	result.EarliestRetryAfter = ptr.To(retryWithJitter)
	return result
}

// unknownOutcome indicates the validation could not be conclusively evaluated. It becomes a
// .status.validation.status=Unknown, .reason=Reason, .message=UserMessage.
type unknownOutcome struct {
	// machine readable, must not be sensitive
	Reason string
	// human readable, for internal use (e.g. logs); not surfaced to the user
	InternalMessage string
	// human readable for user
	UserMessage string
	// ControllerReportingPolicy controls how this Unknown result is surfaced to the controller machinery
	// (see controllerReportingPolicyType); it has no effect on retry/requeue scheduling.
	ControllerReportingPolicy controllerReportingPolicyType
}

// UnknownValidation returns a validationResult indicating the validation could not be conclusively evaluated.
// EarliestRetryAfter is set to nonPassedRetryBase + jitter. reportingPolicy only controls whether the
// controller's SyncOnce returns nil or an error for this result (see controllerReportingPolicyType); it
// does not affect requeue scheduling, which is driven entirely by EarliestRetryAfter.
func UnknownValidation(reason string, userMessage string, internalMessage string, reportingPolicy controllerReportingPolicyType) ValidationResult {
	result := ValidationResult{
		Outcome: outcome{
			Type: OutcomeTypeUnknown,
			Unknown: &unknownOutcome{
				Reason:                    reason,
				InternalMessage:           internalMessage,
				UserMessage:               userMessage,
				ControllerReportingPolicy: reportingPolicy,
			},
		},
	}
	// Jitter avoids retry storms: wait.Jitter(base, 0.5) returns a value in [base, base*1.5].
	retryWithJitter := wait.Jitter(nonpassedRetryBase, jitterFactor)
	result.EarliestRetryAfter = ptr.To(retryWithJitter)
	return result
}

// controllerReportingPolicyType governs how a controller's SyncOnce reports an validation outcome back to the generic controller machinery,
// by selecting whether SyncOnce returns nil or a non-nil error for that sync.
// It is deliberately independent of retry/requeue scheduling: EarliestRetryAfter alone controls whether and how soon, the resource is requeued.
// Do not use controllerReportingPolicyType to try to suppress or influence requeue behavior — use EarliestRetryAfter for that instead.
type controllerReportingPolicyType string

var (
	// ControllerReportingPolicyTypeLogOnly means SyncOnce returns nil, so it is only logged and does not count as a controller error (e.g. in workqueue error metrics). Useful for
	// certain types of failures that are expected/benign and shouldn't be alerted on.
	ControllerReportingPolicyTypeLogOnly controllerReportingPolicyType = "LogOnly"
	// ControllerReportingPolicyTypeError means SyncOnce returns a non-nil error, so it is tracked as a controller error for reporting/metrics purposes.
	ControllerReportingPolicyTypeError controllerReportingPolicyType = "ReportError"
)
