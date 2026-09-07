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

package framework

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestIsRetryableBootDiagnosticsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "non-Azure error",
			err:      fmt.Errorf("some random error"),
			expected: false,
		},
		{
			name: "409 OperationNotAllowed is retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "OperationNotAllowed",
			},
			expected: true,
		},
		{
			name: "409 with different error code is not retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "ResourceGroupNotFound",
			},
			expected: false,
		},
		{
			name: "404 is not retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
			},
			expected: false,
		},
		{
			name: "500 is not retryable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
			},
			expected: false,
		},
		{
			name: "wrapped 409 OperationNotAllowed is retryable",
			err: fmt.Errorf("outer: %w", &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "OperationNotAllowed",
			}),
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isRetryableBootDiagnosticsError(tc.err)
			if result != tc.expected {
				t.Errorf("isRetryableBootDiagnosticsError(%v) = %v, want %v", tc.err, result, tc.expected)
			}
		})
	}
}
