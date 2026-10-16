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
	"testing"
)

func Test_isQuotaRelatedMessage(t *testing.T) {
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

		// Azure ARM error code patterns (detected via fast-path substring check)
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
			name:     "OperationNotAllowed with quota mention",
			message:  "OperationNotAllowed: Operation could not be completed as it results in exceeding approved quota",
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

		// Natural language patterns
		{
			name:     "quota reached without limit",
			message:  "The quota has been reached.",
			expected: true,
		},
		{
			name:     "reached approved quota without limit",
			message:  "Operation reached the approved quota.",
			expected: true,
		},
		{
			name:     "plain-language storage account quota exhaustion",
			message:  "The maximum number of storage accounts per subscription in a region has been reached.",
			expected: true,
		},
		{
			name:     "quota exceeded generic",
			message:  "The subscription quota for VM cores has been exceeded in region eastus",
			expected: true,
		},
		{
			name:     "resource limit reached",
			message:  "The resource limit for public IP addresses has been reached in this region",
			expected: true,
		},
		{
			name:     "quota limit reached",
			message:  "The quota limit for vCPUs has been reached",
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

		// False-positive guards: messages that should NOT match
		{
			name:     "informational quota limit is not a quota error",
			message:  "The quota limit for this subscription is 100.",
			expected: false,
		},
		{
			name:     "storage account not found is not a quota error",
			message:  "Storage account 'foo' was not found.",
			expected: false,
		},
		{
			name:     "connection limit reached is not a quota error",
			message:  "The connection limit has been reached for this endpoint",
			expected: false,
		},
		{
			name:     "retry limit reached is not a quota error",
			message:  "Retry limit has been reached",
			expected: false,
		},
		{
			name:     "rate limit reached is not a quota error",
			message:  "Rate limit has been reached",
			expected: false,
		},
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

		// Capacity/placement errors are NOT quota errors
		{
			name:     "OverconstrainedZonalAllocationRequest is not a quota error",
			message:  "OverconstrainedZonalAllocationRequest: The required resources are not available in zone 1",
			expected: false,
		},
		{
			name:     "OverconstrainedAllocationRequest is not a quota error",
			message:  "OverconstrainedAllocationRequest: Allocation failed due to insufficient capacity",
			expected: false,
		},
		{
			name:     "insufficient capacity is not a quota error",
			message:  "There is insufficient capacity in the requested zone",
			expected: false,
		},
		{
			name:     "no capacity in zone is not a quota error",
			message:  "There is no capacity available in zone 2 for the requested size",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isQuotaRelatedMessage(tc.message)
			if result != tc.expected {
				t.Errorf("isQuotaRelatedMessage(%q) = %v, want %v", tc.message, result, tc.expected)
			}
		})
	}
}

func TestNormalizeQuotaCloudErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		message  string
		expected string
	}{
		{
			name:     "QuotaExceeded stays canonical",
			code:     "QuotaExceeded",
			expected: CloudErrorCodeQuotaExceeded,
		},
		{
			name:     "PublicIPCountLimitReached normalizes without message",
			code:     "PublicIPCountLimitReached",
			expected: CloudErrorCodeQuotaExceeded,
		},
		{
			name:     "MaxStorageAccountsCountPerSubscriptionExceeded normalizes without message",
			code:     "MaxStorageAccountsCountPerSubscriptionExceeded",
			expected: CloudErrorCodeQuotaExceeded,
		},
		{
			name:     "NetworkCountLimitReached normalizes without message",
			code:     "NetworkCountLimitReached",
			expected: CloudErrorCodeQuotaExceeded,
		},
		{
			name:     "InternalServerError upgrades for quota message",
			code:     CloudErrorCodeInternalServerError,
			message:  "The quota has been reached.",
			expected: CloudErrorCodeQuotaExceeded,
		},
		{
			name:     "InternalServerError preserves non-quota message code",
			code:     CloudErrorCodeInternalServerError,
			message:  "connection reset by peer",
			expected: CloudErrorCodeInternalServerError,
		},
		{
			name:     "OperationNotAllowed upgrades for quota message",
			code:     "OperationNotAllowed",
			message:  "The quota has been reached.",
			expected: CloudErrorCodeQuotaExceeded,
		},
		{
			name:     "OperationNotAllowed preserves non-quota message code",
			code:     "OperationNotAllowed",
			message:  "The resource provider is not registered for this subscription",
			expected: "OperationNotAllowed",
		},
		{
			name:     "InvalidRequestContent takes precedence over quota",
			code:     CloudErrorCodeInvalidRequestContent,
			message:  "The quota has been reached.",
			expected: CloudErrorCodeInvalidRequestContent,
		},
		{
			name:     "DeadlineExceeded takes precedence over quota",
			code:     CloudErrorCodeDeadlineExceeded,
			message:  "The quota has been reached.",
			expected: CloudErrorCodeDeadlineExceeded,
		},
		{
			name:     "CapacityHeavyUse takes precedence over quota",
			code:     CloudErrorCodeCapacityHeavyUse,
			message:  "The quota has been reached.",
			expected: CloudErrorCodeCapacityHeavyUse,
		},
		{
			name:     "empty code stays empty despite quota message",
			message:  "The quota has been reached.",
			expected: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := NormalizeQuotaCloudErrorCode(tc.code, tc.message)
			if result != tc.expected {
				t.Errorf("NormalizeQuotaCloudErrorCode(%q, %q) = %q, want %q", tc.code, tc.message, result, tc.expected)
			}
		})
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
			name:        "informational quota limit returns InternalServerError",
			message:     "The quota limit for this subscription is 100.",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeInternalServerError,
		},
		{
			name:        "plain-language storage account quota returns QuotaExceeded",
			message:     "The maximum number of storage accounts per subscription in a region has been reached.",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeQuotaExceeded,
		},
		{
			name:        "storage account not found returns InternalServerError",
			message:     "Storage account 'foo' was not found.",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeInternalServerError,
		},
		{
			name:        "empty message returns default code",
			message:     "",
			defaultCode: CloudErrorCodeInvalidRequestContent,
			expected:    CloudErrorCodeInvalidRequestContent,
		},
		{
			name:        "quota reached without limit returns QuotaExceeded",
			message:     "The quota has been reached.",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeQuotaExceeded,
		},
		{
			name:        "reached approved quota without limit returns QuotaExceeded",
			message:     "Operation reached the approved quota.",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeQuotaExceeded,
		},
		{
			name:        "retry limit reached returns InternalServerError",
			message:     "retry limit reached",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeInternalServerError,
		},
		{
			name:        "rate limit reached returns InternalServerError",
			message:     "rate limit reached",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeInternalServerError,
		},
		{
			name:        "connection limit reached returns InternalServerError",
			message:     "connection limit reached",
			defaultCode: CloudErrorCodeInternalServerError,
			expected:    CloudErrorCodeInternalServerError,
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
