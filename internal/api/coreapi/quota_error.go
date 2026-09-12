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
	"regexp"
	"strings"
)

// azureQuotaErrorCodes lists Azure ARM error codes that indicate quota
// exhaustion. When one of these codes appears in an ARM ResponseError,
// the error should be classified as a quota error.
//
// Note: Transient Azure placement/capacity failures such as
// OverconstrainedZonalAllocationRequest and OverconstrainedAllocationRequest
// are intentionally excluded. Those indicate temporary zone capacity issues
// (remediation: retry with a different zone or VM size) rather than quota
// limit violations (remediation: request a quota increase).
var azureQuotaErrorCodes = map[string]bool{
	"QuotaExceeded":                                  true,
	"PublicIPCountLimitReached":                      true,
	"MaxStorageAccountsCountPerSubscriptionExceeded": true,
	"NetworkCountLimitReached":                       true,
}

// quotaMessagePatterns lists compiled regexps that match error messages
// indicating quota issues. Each pattern is case-insensitive.
//
// Only natural-language patterns belong here. Patterns that match literal
// ARM error code strings are unnecessary because the fast-path substring
// check in isQuotaRelatedMessage already covers them.
var quotaMessagePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)insufficient\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\bexceeded\b`),
	regexp.MustCompile(`(?i)\b(quota|resource)\b.*\blimit\b.*\breached\b`),
	regexp.MustCompile(`(?i)\bOperationNotAllowed\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\bOperationNotAllowed\b`),
	regexp.MustCompile(`(?i)\bexceeds\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\blimit\b`),
}

// isAzureQuotaErrorCode reports whether the given Azure ARM error code
// indicates a quota issue.
func isAzureQuotaErrorCode(errorCode string) bool {
	return azureQuotaErrorCodes[errorCode]
}

// isQuotaRelatedMessage reports whether the given error message indicates
// an Azure quota error. It matches against known Azure ARM error codes
// embedded in messages as well as natural-language patterns such as
// "insufficient public IP address quota".
func isQuotaRelatedMessage(message string) bool {
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
	// quota error messages.
	for _, pattern := range quotaMessagePatterns {
		if pattern.MatchString(message) {
			return true
		}
	}

	return false
}

// CloudErrorCodeForMessage returns CloudErrorCodeQuotaExceeded if the message
// indicates a quota error, or the provided defaultCode otherwise.
// This is a convenience function for operation controllers that need to select
// the appropriate error code when constructing a CloudErrorBody for a failed
// operation.
//
// The caller's HTTP status code remains unchanged (typically 500). HTTP 500 is
// used rather than 429 or 503 because quota errors are not transient from the
// ARM client's perspective — they will not resolve on retry without a quota
// increase. The distinct CloudErrorCodeQuotaExceeded code lets monitoring
// distinguish quota failures from other internal errors (AROSLSRE-875).
func CloudErrorCodeForMessage(message string, defaultCode string) string {
	if isQuotaRelatedMessage(message) {
		return CloudErrorCodeQuotaExceeded
	}
	return defaultCode
}
