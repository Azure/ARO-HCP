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

package validation

import (
	"context"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// MockNodePoolValidation is a NodePoolValidation implementation for controller tests.
type MockNodePoolValidation struct {
	validationName string
	result         validationutils.ValidationResult
}

var _ validationutils.NodePoolValidation = (*MockNodePoolValidation)(nil)

// NewMockNodePoolValidation creates a mock validation with the given name and no result configured.
func NewMockNodePoolValidation(name string) *MockNodePoolValidation {
	return &MockNodePoolValidation{validationName: name}
}

// WithPassed configures the mock to return a passed validation result.
func (m *MockNodePoolValidation) WithPassed() *MockNodePoolValidation {
	m.result = validationutils.PassedValidation(coreapi.ControllerConditionReasonAsExpected, "As expected.", "")
	return m
}

// WithFailed configures the mock to return a failed validation result.
func (m *MockNodePoolValidation) WithFailed(reason, internalMessage, userMessage string) *MockNodePoolValidation {
	m.result = validationutils.FailedValidation(reason, userMessage, internalMessage)
	return m
}

// WithUnknownLogOnly configures the mock to return an unknown validation result with log-only reporting.
func (m *MockNodePoolValidation) WithUnknownLogOnly(reason, internalMessage, userMessage string) *MockNodePoolValidation {
	m.result = validationutils.UnknownValidation(reason, userMessage, internalMessage, validationutils.ControllerReportingPolicyTypeLogOnly)
	return m
}

// WithUnknownReportError configures the mock to return an unknown validation result that reports as an error.
func (m *MockNodePoolValidation) WithUnknownReportError(reason, internalMessage, userMessage string) *MockNodePoolValidation {
	m.result = validationutils.UnknownValidation(reason, userMessage, internalMessage, validationutils.ControllerReportingPolicyTypeError)
	return m
}

// WithSkipped configures the mock to return a skipped validation result.
func (m *MockNodePoolValidation) WithSkipped(reason, internalMessage, userMessage string) *MockNodePoolValidation {
	m.result = validationutils.SkippedValidation(reason, userMessage, internalMessage)
	return m
}

// WithEarliestRetryAfter overrides the currently configured result's EarliestRetryAfter, e.g. to nil to
// exercise the "no retry backoff" path.
func (m *MockNodePoolValidation) WithEarliestRetryAfter(d *time.Duration) *MockNodePoolValidation {
	m.result.EarliestRetryAfter = d
	return m
}

func (m *MockNodePoolValidation) Name() string { return m.validationName }

func (m *MockNodePoolValidation) Validate(_ context.Context, _ *coreapi.HCPOpenShiftCluster, _ *coreapi.Subscription, _ *coreapi.HCPOpenShiftClusterNodePool) validationutils.ValidationResult {
	return m.result
}
