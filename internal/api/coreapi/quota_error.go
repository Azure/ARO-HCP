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
	"regexp"
	"strings"
)

// azureQuotaErrorCodes lists Azure ARM error codes that indicate quota or
// capacity exhaustion. When one of these codes appears in an ARM ResponseError,
// the error should be classified as a quota error.
var azureQuotaErrorCodes = map[string]bool{
	"QuotaExceeded":                          true,
	"PublicIPCountLimitReached":              true,
	"OverconstrainedZonalAllocationRequest":  true,
	"OverconstrainedAllocationRequest":       true,
	"MaxStorageAccountsCountPerSubscriptionExceeded": true,
	"NetworkCountLimitReached": true,
}

// quotaMessagePatterns lists compiled regexps that match error messages
// indicating quota or capacity issues. Each pattern is case-insensitive.
var quotaMessagePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)insufficient\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\bexceeded\b`),
	regexp.MustCompile(`(?i)\blimit\b.*\breached\b`),
	regexp.MustCompile(`(?i)\bPublicIPCountLimitReached\b`),
	regexp.MustCompile(`(?i)\bOverconstrainedZonalAllocationRequest\b`),
	regexp.MustCompile(`(?i)\bOverconstrainedAllocationRequest\b`),
	regexp.MustCompile(`(?i)\bQuotaExceeded\b`),
	regexp.MustCompile(`(?i)\bOperationNotAllowed\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\bOperationNotAllowed\b`),
	regexp.MustCompile(`(?i)\bexceeds\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\blimit\b`),
	regexp.MustCompile(`(?i)\bcapacity\b.*\binsufficient\b`),
	regexp.MustCompile(`(?i)\binsufficient\b.*\bcapacity\b`),
	regexp.MustCompile(`(?i)\bno\b.*\bcapacity\b.*\bzone\b`),
	regexp.MustCompile(`(?i)\bMaxStorageAccountsCountPerSubscriptionExceeded\b`),
	regexp.MustCompile(`(?i)\bNetworkCountLimitReached\b`),
}

// IsAzureQuotaErrorCode reports whether the given Azure ARM error code
// indicates a quota or capacity issue.
func IsAzureQuotaErrorCode(errorCode string) bool {
	return azureQuotaErrorCodes[errorCode]
}

// IsQuotaRelatedMessage reports whether the given error message indicates
// an Azure quota or capacity error. It matches against known Azure ARM
// error codes embedded in messages as well as natural-language patterns
// such as "insufficient public IP address quota".
func IsQuotaRelatedMessage(message string) bool {
	if message == "" {
		return false
	}

	// Fast path: check if any known Azure error code appears as a substring
	// (case-insensitive). This catches structured error messages that embed
	// the ARM error code directly.
	lowerMessage := strings.ToLower(message)
	for code := range azureQuotaErrorCodes {
		if strings.Contains(lowerMessage, strings.ToLower(code)) {
			return true
		}
	}

	// Slow path: match against compiled regexp patterns for natural-language
	// quota/capacity error messages.
	for _, pattern := range quotaMessagePatterns {
		if pattern.MatchString(message) {
			return true
		}
	}

	return false
}

// NewQuotaExceededError creates a CloudError for a quota/capacity exceeded
// error. It returns HTTP 500 with the QuotaExceeded error code and the
// provided message describing the specific quota that was exhausted.
func NewQuotaExceededError(message string) *CloudError {
	return NewCloudError(
		http.StatusInternalServerError,
		CloudErrorCodeQuotaExceeded,
		"",
		"%s", message,
	)
}

// CloudErrorCodeForMessage returns CloudErrorCodeQuotaExceeded if the message
// indicates a quota/capacity error, or the provided defaultCode otherwise.
// This is a convenience function for operation controllers that need to select
// the appropriate error code when constructing a CloudErrorBody for a failed
// operation.
func CloudErrorCodeForMessage(message string, defaultCode string) string {
	if IsQuotaRelatedMessage(message) {
		return CloudErrorCodeQuotaExceeded
	}
	return defaultCode
}
