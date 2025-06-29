// Copyright 2025 Microsoft Corporation
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

package breakglass

import (
	"fmt"
	"time"
)

// ValidationError represents errors related to input validation.
type ValidationError struct {
	// Field is the name of the field that failed validation
	Field string
	// Value is the value that failed validation
	Value string
	// Constraint describes what validation rule was violated
	Constraint string
	// Underlying is the original error if applicable
	Underlying error
}

func (e *ValidationError) Error() string {
	if e.Underlying != nil {
		return fmt.Sprintf("validation failed for field '%s' with value '%s': %s (%v)",
			e.Field, e.Value, e.Constraint, e.Underlying)
	}
	return fmt.Sprintf("validation failed for field '%s' with value '%s': %s",
		e.Field, e.Value, e.Constraint)
}

func (e *ValidationError) Unwrap() error {
	return e.Underlying
}

// NewValidationError creates a new ValidationError with the specified parameters.
func NewValidationError(field, value, constraint string, err error) *ValidationError {
	return &ValidationError{
		Field:      field,
		Value:      value,
		Constraint: constraint,
		Underlying: err,
	}
}

// TimeoutError represents errors related to operation timeouts.
type TimeoutError struct {
	// Operation describes the operation that timed out
	Operation string
	// Duration is how long the operation waited before timing out
	Duration time.Duration
	// Expected describes what the operation was waiting for
	Expected string
	// Underlying is the original error if applicable
	Underlying error
}

func (e *TimeoutError) Error() string {
	if e.Underlying != nil {
		return fmt.Sprintf("timeout after %v waiting for %s during %s: %v",
			e.Duration, e.Expected, e.Operation, e.Underlying)
	}
	return fmt.Sprintf("timeout after %v waiting for %s during %s",
		e.Duration, e.Expected, e.Operation)
}

func (e *TimeoutError) Unwrap() error {
	return e.Underlying
}

// NewTimeoutError creates a new TimeoutError with the specified parameters.
func NewTimeoutError(operation string, duration time.Duration, expected string, err error) *TimeoutError {
	return &TimeoutError{
		Operation:  operation,
		Duration:   duration,
		Expected:   expected,
		Underlying: err,
	}
}

// ConfigurationError represents errors related to configuration and setup.
type ConfigurationError struct {
	// Component is the component that has the configuration issue
	Component string
	// Setting is the specific setting that is misconfigured
	Setting string
	// Reason describes why the configuration is invalid
	Reason string
	// Underlying is the original error if applicable
	Underlying error
}

func (e *ConfigurationError) Error() string {
	if e.Underlying != nil {
		return fmt.Sprintf("configuration error in %s.%s: %s (%v)",
			e.Component, e.Setting, e.Reason, e.Underlying)
	}
	return fmt.Sprintf("configuration error in %s.%s: %s",
		e.Component, e.Setting, e.Reason)
}

func (e *ConfigurationError) Unwrap() error {
	return e.Underlying
}

// NewConfigurationError creates a new ConfigurationError with the specified parameters.
func NewConfigurationError(component, setting, reason string, err error) *ConfigurationError {
	return &ConfigurationError{
		Component:  component,
		Setting:    setting,
		Reason:     reason,
		Underlying: err,
	}
}

// CertificateError represents errors related to certificate operations.
type CertificateError struct {
	// Operation describes the certificate operation that failed
	Operation string
	// CertType describes the type of certificate (e.g., "CA", "client")
	CertType string
	// Underlying is the original error that caused this certificate error
	Underlying error
}

func (e *CertificateError) Error() string {
	return fmt.Sprintf("certificate error during %s of %s certificate: %v",
		e.Operation, e.CertType, e.Underlying)
}

func (e *CertificateError) Unwrap() error {
	return e.Underlying
}

// NewCertificateError creates a new CertificateError with the specified parameters.
func NewCertificateError(operation, certType string, err error) *CertificateError {
	return &CertificateError{
		Operation:  operation,
		CertType:   certType,
		Underlying: err,
	}
}
