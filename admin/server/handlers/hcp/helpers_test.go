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

package hcp

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestClusterServiceError(t *testing.T) {
	// Helper to create OCM errors
	createOCMError := func(status int) error {
		ocmErr, err := ocmerrors.NewError().Status(status).Build()
		if err != nil {
			t.Fatalf("Failed to create OCM error: %v", err)
		}
		return ocmErr
	}

	tests := []struct {
		name               string
		err                error
		what               string
		expectedStatusCode int
		expectedErrorCode  string
		expectedMessage    string
		isCloudError       bool
	}{
		{
			name:               "OCM not-found error returns 404 CloudError",
			err:                createOCMError(http.StatusNotFound),
			what:               "cluster data",
			expectedStatusCode: http.StatusNotFound,
			expectedErrorCode:  arm.CloudErrorCodeNotFound,
			expectedMessage:    "cluster data not found in cluster service",
			isCloudError:       true,
		},
		{
			name:               "OCM not-found error for hypershift details",
			err:                createOCMError(http.StatusNotFound),
			what:               "hypershift details",
			expectedStatusCode: http.StatusNotFound,
			expectedErrorCode:  arm.CloudErrorCodeNotFound,
			expectedMessage:    "hypershift details not found in cluster service",
			isCloudError:       true,
		},
		{
			name:            "OCM internal server error wraps error",
			err:             createOCMError(http.StatusInternalServerError),
			what:            "cluster data",
			expectedMessage: "failed to get cluster data from cluster service",
			isCloudError:    false,
		},
		{
			name:            "OCM bad request error wraps error",
			err:             createOCMError(http.StatusBadRequest),
			what:            "provision shard",
			expectedMessage: "failed to get provision shard from cluster service",
			isCloudError:    false,
		},
		{
			name:            "OCM unauthorized error wraps error",
			err:             createOCMError(http.StatusUnauthorized),
			what:            "cluster data",
			expectedMessage: "failed to get cluster data from cluster service",
			isCloudError:    false,
		},
		{
			name:            "non-OCM error wraps error",
			err:             errors.New("network timeout"),
			what:            "cluster data",
			expectedMessage: "failed to get cluster data from cluster service",
			isCloudError:    false,
		},
		{
			name:            "generic error wraps error",
			err:             errors.New("connection refused"),
			what:            "hypershift details",
			expectedMessage: "failed to get hypershift details from cluster service",
			isCloudError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClusterServiceError(tt.err, tt.what)

			if result == nil {
				t.Fatal("Expected error but got nil")
			}

			if tt.isCloudError {
				// Check if it's a CloudError
				var cloudErr *arm.CloudError
				if !errors.As(result, &cloudErr) {
					t.Fatalf("Expected CloudError but got %T: %v", result, result)
				}

				if cloudErr.StatusCode != tt.expectedStatusCode {
					t.Errorf("Expected status code %d, got %d", tt.expectedStatusCode, cloudErr.StatusCode)
				}

				if cloudErr.Code != tt.expectedErrorCode {
					t.Errorf("Expected error code %q, got %q", tt.expectedErrorCode, cloudErr.Code)
				}

				if cloudErr.Message != tt.expectedMessage {
					t.Errorf("Expected message %q, got %q", tt.expectedMessage, cloudErr.Message)
				}
			} else {
				// Check if it's a wrapped error (not CloudError)
				var cloudErr *arm.CloudError
				if errors.As(result, &cloudErr) {
					t.Fatalf("Expected wrapped error but got CloudError: %v", result)
				}

				// Verify error message contains expected text
				if !strings.Contains(result.Error(), tt.expectedMessage) {
					t.Errorf("Expected error to contain %q, got %q", tt.expectedMessage, result.Error())
				}

				// Verify original error is wrapped
				if !errors.Is(result, tt.err) {
					t.Errorf("Expected error to wrap original error")
				}
			}
		})
	}
}
