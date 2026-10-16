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

// azureQuotaErrorCodes is a conservative subset of Azure ARM error codes whose
// presence indicates quota exhaustion, where the correct customer remediation
// is to request a quota increase. The codes are drawn from Microsoft's
// documented ARM/compute error codes and from codes observed in production
// quota incidents (see AROSLSRE-872). The list is intentionally not exhaustive:
// a code is added only when quota exhaustion is the unambiguous cause.
//
// These codes are matched as case-insensitive substrings within an error
// message (see isQuotaRelatedMessage); there is no structured
// ResponseError.Code extraction path.
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

// quotaMessagePatterns lists compiled, case-insensitive regexps that match
// quota-related wording in free-form ARM error messages. Entries may combine
// an ARM error-code token (e.g. OperationNotAllowed) with quota/resource
// context; requiring that context keeps them from firing on unrelated
// connection, retry, or rate-limit messages. They complement the exact-code
// substring fast path in isQuotaRelatedMessage for codes that are quota-related
// only in certain messages.
var quotaMessagePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)insufficient\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\bexceeded\b`),
	regexp.MustCompile(`(?i)\b(quota|resource)\b.*\blimit\b.*\breached\b`),
	regexp.MustCompile(`(?i)\bOperationNotAllowed\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\bOperationNotAllowed\b`),
	regexp.MustCompile(`(?i)\bexceeds\b.*\bquota\b`),
	regexp.MustCompile(`(?i)\bstorage account(s)?\b.*\b(quota|limit|maximum|reached|exceeded)\b`),
	regexp.MustCompile(`(?i)\bquota\b.*\breached\b`),
	regexp.MustCompile(`(?i)\breached\b.*\bquota\b`),
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

// NormalizeQuotaCloudErrorCode collapses recognized Azure quota errors to the
// canonical CloudErrorCodeQuotaExceeded, while preserving intentionally
// explicit non-quota codes.
func NormalizeQuotaCloudErrorCode(code, message string) string {
	if code == "" {
		return code
	}
	if azureQuotaErrorCodes[code] {
		return CloudErrorCodeQuotaExceeded
	}
	// OperationNotAllowed is a raw ARM code without a coreapi constant.
	if code == CloudErrorCodeInternalServerError || code == "OperationNotAllowed" {
		return CloudErrorCodeForMessage(message, code)
	}
	return code
}
