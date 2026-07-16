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

import "time"

type OutcomeType string

const (
	OutcomePassed  OutcomeType = "Passed"
	OutcomeFailed  OutcomeType = "Failed"
	OutcomeUnknown OutcomeType = "Unknown"
)

type ReportingPolicyType string

const (
	ReportingPolicyLogOnly     ReportingPolicyType = "LogOnly"
	ReportingPolicyReportError ReportingPolicyType = "ReportError"
)

type ValidationResult struct {
	// EarliestRetryAfter is the earliest duration after which a retry is allowed.
	EarliestRetryAfter *time.Duration

	Outcome ValidationOutcome
}

// ValidationOutcome is a "oneof" (discriminated union) of Passed, Failed, or Unknown.
// Exactly one of Passed/Failed/Unknown is expected to be set, based on Type.
type ValidationOutcome struct {
	Type OutcomeType
	// Oneof payload. Only the field matching Type should be non-nil.
	Passed  *PassedResult
	Failed  *FailedResult
	Unknown *UnknownResult
}

type PassedResult struct {
	// UserMessage is the user-facing message when the validation passes.
	// It is optional, and must be non-sensitive.
	UserMessage string
}

type FailedResult struct {
	Reason                 string
	ServiceProviderMessage string
	UserMessage            string
}

type UnknownResult struct {
	Reason                 string
	ServiceProviderMessage string
	UserMessage            string
	ReportingPolicy        ReportingPolicyType
}
