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
	"errors"
	"testing"
	"time"
)

// TestValidationError tests validation error functionality.
func TestValidationError(t *testing.T) {
	testCases := []struct {
		name        string
		field       string
		value       string
		constraint  string
		underlying  error
		expectedMsg string
	}{
		{
			name:        "without underlying error",
			field:       "clusterID",
			value:       "-invalid-",
			constraint:  "cannot start or end with hyphen",
			underlying:  nil,
			expectedMsg: "validation failed for field 'clusterID' with value '-invalid-': cannot start or end with hyphen",
		},
		{
			name:        "with underlying error",
			field:       "timeout",
			value:       "30s",
			constraint:  "must be at least 1 minute",
			underlying:  errors.New("duration too short"),
			expectedMsg: "validation failed for field 'timeout' with value '30s': must be at least 1 minute (duration too short)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewValidationError(tc.field, tc.value, tc.constraint, tc.underlying)

			if err.Error() != tc.expectedMsg {
				t.Errorf("Expected message '%s', got '%s'", tc.expectedMsg, err.Error())
			}

			if err.Unwrap() != tc.underlying {
				t.Errorf("Expected unwrapped error to be %v, got %v", tc.underlying, err.Unwrap())
			}
		})
	}
}

// TestTimeoutError tests timeout error functionality.
func TestTimeoutError(t *testing.T) {
	duration := 30 * time.Second
	underlyingErr := errors.New("context deadline exceeded")

	testCases := []struct {
		name        string
		operation   string
		duration    time.Duration
		expected    string
		underlying  error
		expectedMsg string
	}{
		{
			name:        "without underlying error",
			operation:   "CSR approval",
			duration:    duration,
			expected:    "certificate signing",
			underlying:  nil,
			expectedMsg: "timeout after 30s waiting for certificate signing during CSR approval",
		},
		{
			name:        "with underlying error",
			operation:   "port forwarding",
			duration:    duration,
			expected:    "connection establishment",
			underlying:  underlyingErr,
			expectedMsg: "timeout after 30s waiting for connection establishment during port forwarding: context deadline exceeded",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewTimeoutError(tc.operation, tc.duration, tc.expected, tc.underlying)

			if err.Error() != tc.expectedMsg {
				t.Errorf("Expected message '%s', got '%s'", tc.expectedMsg, err.Error())
			}

			if err.Unwrap() != tc.underlying {
				t.Errorf("Expected unwrapped error to be %v, got %v", tc.underlying, err.Unwrap())
			}
		})
	}
}

// TestConfigurationError tests configuration error functionality.
func TestConfigurationError(t *testing.T) {
	testCases := []struct {
		name        string
		component   string
		setting     string
		reason      string
		underlying  error
		expectedMsg string
	}{
		{
			name:        "without underlying error",
			component:   "services",
			setting:     "kubeAPIServer",
			reason:      "cannot be empty",
			underlying:  nil,
			expectedMsg: "configuration error in services.kubeAPIServer: cannot be empty",
		},
		{
			name:        "with underlying error",
			component:   "templates",
			setting:     "csrNameTemplate",
			reason:      "invalid format",
			underlying:  errors.New("missing format specifier"),
			expectedMsg: "configuration error in templates.csrNameTemplate: invalid format (missing format specifier)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewConfigurationError(tc.component, tc.setting, tc.reason, tc.underlying)

			if err.Error() != tc.expectedMsg {
				t.Errorf("Expected message '%s', got '%s'", tc.expectedMsg, err.Error())
			}

			if err.Unwrap() != tc.underlying {
				t.Errorf("Expected unwrapped error to be %v, got %v", tc.underlying, err.Unwrap())
			}
		})
	}
}

// TestCertificateError tests certificate error functionality.
func TestCertificateError(t *testing.T) {
	underlyingErr := errors.New("invalid key usage")
	err := NewCertificateError("validation", "client", underlyingErr)

	expectedMsg := "certificate error during validation of client certificate: invalid key usage"
	if err.Error() != expectedMsg {
		t.Errorf("Expected message '%s', got '%s'", expectedMsg, err.Error())
	}

	if err.Unwrap() != underlyingErr {
		t.Errorf("Expected unwrapped error to be %v, got %v", underlyingErr, err.Unwrap())
	}
}
