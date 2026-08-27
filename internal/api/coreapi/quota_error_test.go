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

package coreapi

import (
	"net/http"
	"testing"
)

func TestIsAzureQuotaErrorCode(t *testing.T) {
	tests := []struct {
		name      string
		errorCode string
		expected  bool
	}{
		{
			name:      "QuotaExceeded is a quota error code",
			errorCode: "QuotaExceeded",
			expected:  true,
		},
		{
			name:      "PublicIPCountLimitReached is a quota error code",
			errorCode: "PublicIPCountLimitReached",
			expected:  true,
		},
		{
			name:      "OverconstrainedZonalAllocationRequest is a quota error code",
			errorCode: "OverconstrainedZonalAllocationRequest",
			expected:  true,
		},
		{
			name:      "OverconstrainedAllocationRequest is a quota error code",
			errorCode: "OverconstrainedAllocationRequest",
			expected:  true,
		},
		{
			name:      "MaxStorageAccountsCountPerSubscriptionExceeded is a quota error code",
			errorCode: "MaxStorageAccountsCountPerSubscriptionExceeded",
			expected:  true,
		},
		{
			name:      "NetworkCountLimitReached is a quota error code",
			errorCode: "NetworkCountLimitReached",
			expected:  true,
		},
		{
			name:      "InternalServerError is not a quota error code",
			errorCode: "InternalServerError",
			expected:  false,
		},
		{
			name:      "ResourceNotFound is not a quota error code",
			errorCode: "ResourceNotFound",
			expected:  false,
		},
		{
			name:      "empty string is not a quota error code",
			errorCode: "",
			expected:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsAzureQuotaErrorCode(tc.errorCode)
			if result != tc.expected {
				t.Errorf("IsAzureQuotaErrorCode(%q) = %v, want %v", tc.errorCode, result, tc.expected)
			}
		})
	}
}

func TestIsQuotaRelatedMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected bool
	}{
		// Real-world messages from AROSLSRE-872 incident
		{
			name:     "insufficient public IP address quota from incident",
			message:  "insufficient public IP address quota: required 2, available 0",
			expected: true,
		},
		{
			name:     "insufficient public IP quota variant",
			message:  "insufficient public IP address quota in eastus2: Public IPv4 Standard: 300/300",
			expected: true,
		},

		// Azure ARM error code patterns
		{
			name:     "QuotaExceeded error code in message",
			message:  "QuotaExceeded: Operation could not be completed as it results in exceeding approved standardDSv3Family Cores quota",
			expected: true,
		},
		{
			name:     "PublicIPCountLimitReached in message",
			message:  "PublicIPCountLimitReached: Cannot create more than 300 public IP addresses for this subscription in this region",
			expected: true,
		},
		{
			name:     "OverconstrainedZonalAllocationRequest in message",
			message:  "OverconstrainedZonalAllocationRequest: The required resources are not available in zone 1",
			expected: true,
		},
		{
			name:     "OverconstrainedAllocationRequest in message",
			message:  "OverconstrainedAllocationRequest: Allocation failed due to insufficient capacity",
			expected: true,
		},
		{
			name:     "OperationNotAllowed with quota mention",
			message:  "OperationNotAllowed: Operation could not be completed as it results in exceeding approved quota",
			expected: true,
		},

		// Natural language patterns
		{
			name:     "quota exceeded generic",
			message:  "The subscription quota for VM cores has been exceeded in region eastus",
			expected: true,
		},
		{
			name:     "limit reached generic",
			message:  "The resource limit for public IP addresses has been reached in this region",
			expected: true,
		},
		{
			name:     "exceeds quota",
			message:  "Deployment exceeds quota for VM size Standard_D4s_v3",
			expected: true,
		},
		{
			name:     "quota limit",
			message:  "The quota limit for vCPUs has been reached",
			expected: true,
		},
		{
			name:     "insufficient capacity",
			message:  "There is insufficient capacity in the requested zone",
			expected: true,
		},
		{
			name:     "capacity insufficient",
			message:  "The capacity is currently insufficient for the requested VM size",
			expected: true,
		},
		{
			name:     "no capacity in zone",
			message:  "There is no capacity available in zone 2 for the requested size",
			expected: true,
		},
		{
			name:     "MaxStorageAccountsCountPerSubscriptionExceeded in message",
			message:  "MaxStorageAccountsCountPerSubscriptionExceeded: The limit of 250 storage accounts has been reached",
			expected: true,
		},
		{
			name:     "NetworkCountLimitReached in message",
			message:  "NetworkCountLimitReached: Cannot create more than 1000 virtual networks",
			expected: true,
		},

		// Case insensitivity
		{
			name:     "case insensitive quota exceeded",
			message:  "QUOTA EXCEEDED for Standard_D4s_v3",
			expected: true,
		},
		{
			name:     "case insensitive insufficient quota",
			message:  "Insufficient vCPU Quota in region westus2",
			expected: true,
		},

		// Non-quota messages that should NOT match
		{
			name:     "normal internal error",
			message:  "Internal server error occurred during processing",
			expected: false,
		},
		{
			name:     "network timeout",
			message:  "The operation timed out while connecting to the backend",
			expected: false,
		},
		{
			name:     "resource not found",
			message:  "The resource was not found in the specified resource group",
			expected: false,
		},
		{
			name:     "permission denied",
			message:  "Access is denied. You do not have the required permissions",
			expected: false,
		},
		{
			name:     "empty message",
			message:  "",
			expected: false,
		},
		{
			name:     "OperationNotAllowed without quota mention",
			message:  "OperationNotAllowed: The resource provider is not registered for this subscription",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsQuotaRelatedMessage(tc.message)
			if result != tc.expected {
				t.Errorf("IsQuotaRelatedMessage(%q) = %v, want %v", tc.message, result, tc.expected)
			}
		})
	}
}

func TestNewQuotaExceededError(t *testing.T) {
	message := "insufficient public IP address quota: required 2, available 0"
	err := NewQuotaExceededError(message)

	if err.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, http.StatusInternalServerError)
	}
	if err.CloudErrorBody.Code != CloudErrorCodeQuotaExceeded {
		t.Errorf("Code = %q, want %q", err.CloudErrorBody.Code, CloudErrorCodeQuotaExceeded)
	}
	if err.CloudErrorBody.Message != message {
		t.Errorf("Message = %q, want %q", err.CloudErrorBody.Message, message)
	}
}

func TestCloudErrorCodeForMessage(t *testing.T) {
	tests := []struct {
		name        string
		message     string
		defaultCode string
		expected    string
	}{
		{
			name:        "quota message returns QuotaExceeded",
			message:     "insufficient public IP address quota: required 2, available 0",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeQuotaExceeded,
		},
		{
			name:        "non-quota message returns default code",
			message:     "internal server error occurred during processing",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeInternalServerError,
		},
		{
			name:        "empty message returns default code",
			message:     "",
			defaultCode: CloudErrorCodeInvalidRequestContent,
			expected:    CloudErrorCodeInvalidRequestContent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := CloudErrorCodeForMessage(tc.message, tc.defaultCode)
			if result != tc.expected {
				t.Errorf("CloudErrorCodeForMessage(%q, %q) = %q, want %q", tc.message, tc.defaultCode, result, tc.expected)
			}
		})
	}
}
