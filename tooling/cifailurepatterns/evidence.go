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

package cifailurepatterns

import (
	"strings"
)

func splitNonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func refineDeserializationNoOutputPicked(raw string, lowered string, picked string) string {
	// The regex requires this literal suffix. Reuse the lowered raw payload
	// to avoid an expensive case-insensitive regex scan on unrelated errors.
	if (!strings.Contains(lowered, "no output from command") || !containsDeserializationNoOutputSignal(raw)) &&
		!containsDeserializationNoOutputSignal(picked) {
		return picked
	}
	if deserializationLine := lastDeserializationNoOutputLine(raw); deserializationLine != "" {
		return deserializationLine
	}
	if commandLine := lastCommandErrorLine(raw); commandLine != "" && !isBareCommandErrorExitStatus(commandLine) {
		return commandLine
	}
	return picked
}

func refineCommandErrorExitStatusOnly(raw string, picked string) string {
	normalized := strings.ToLower(collapseWS(strings.TrimSpace(picked)))
	if normalized != "command error: exit status 1" &&
		normalized != "command error: exit status 2" &&
		normalized != "command error: exit status 3" {
		return picked
	}
	if refined := bestSignalErrorLine(raw); refined != "" {
		return refined
	}
	return picked
}

func containsDeserializationNoOutputSignal(value string) bool {
	return reDeserializationNoOutput.MatchString(strings.TrimSpace(value))
}

func containsDeserializationErrorToken(value string) bool {
	return reDeserializationToken.MatchString(strings.TrimSpace(value))
}

func lastDeserializationNoOutputLine(value string) string {
	matches := reDeserializationNoOutput.FindAllString(value, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		line := strings.TrimSpace(matches[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func lastCommandErrorLine(value string) string {
	matches := reCommandErrorLine.FindAllString(value, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		line := strings.TrimSpace(matches[i])
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "Command Error: no output from command") {
			continue
		}
		return line
	}
	return ""
}

func isBareCommandErrorExitStatus(value string) bool {
	normalized := strings.ToLower(collapseWS(strings.TrimSpace(value)))
	switch normalized {
	case "command error: exit status 1", "command error: exit status 2", "command error: exit status 3":
		return true
	default:
		return false
	}
}

func bestSignalErrorLine(text string) string {
	lines := splitNonEmptyLines(text)
	if len(lines) == 0 {
		return ""
	}

	preStruct := make([]string, 0, len(lines))
	postStruct := make([]string, 0, len(lines))
	inStructBlock := false
	for _, line := range lines {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		if isStructBoundaryLine(token) {
			inStructBlock = true
			continue
		}
		if !rePickErrorSignal.MatchString(token) || isAssertionTail(token) {
			continue
		}
		if isWrapperNoiseLine(token) || isStructFieldNoiseLine(token) || isStatusBannerLine(token) {
			continue
		}
		if inStructBlock {
			postStruct = append(postStruct, token)
		} else {
			preStruct = append(preStruct, token)
		}
	}

	if len(preStruct) > 0 {
		return preStruct[len(preStruct)-1]
	}
	if len(postStruct) > 0 {
		return postStruct[len(postStruct)-1]
	}
	return ""
}

func bestHTTPResponseStatusLine(text string) string {
	lines := splitNonEmptyLines(text)
	for _, line := range lines {
		token := collapseWS(line)
		if reHTTPResponseStatusLine.MatchString(token) {
			return token
		}
	}
	return ""
}

func isStructBoundaryLine(line string) bool {
	switch strings.TrimSpace(line) {
	case "{", "}", "},", "[", "]", "],":
		return true
	default:
		return false
	}
}

func isWrapperNoiseLine(line string) bool {
	normalized := strings.ToLower(collapseWS(line))
	if normalized == "" {
		return true
	}
	if isEventuallyWrapperLine(normalized) || isTimedOutAfterLine(normalized) {
		return true
	}
	return strings.HasPrefix(normalized, "fail [") ||
		strings.HasPrefix(normalized, "unexpected error") ||
		strings.HasPrefix(normalized, "<*fmt.wraperror") ||
		strings.HasPrefix(normalized, "<*errors.errorstring") ||
		strings.HasPrefix(normalized, "<*")
}

func isStructFieldNoiseLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	normalized := strings.ToLower(collapseWS(trimmed))
	switch normalized {
	case "", "{", "}", "},", "[]", "{}", "{},", "null":
		return true
	}
	if strings.HasPrefix(normalized, "msg:") || strings.HasPrefix(normalized, "err:") || strings.HasPrefix(normalized, "cause:") {
		return true
	}
	if strings.HasPrefix(normalized, "istimeout:") ||
		strings.HasPrefix(normalized, "istemporary:") ||
		strings.HasPrefix(normalized, "isnotfound:") ||
		strings.HasPrefix(normalized, "server:") {
		return true
	}
	if strings.HasPrefix(normalized, "errorcode:\"\"") || strings.HasPrefix(normalized, "errorcode: \"\"") || strings.HasPrefix(normalized, "errorcode:''") || strings.HasPrefix(normalized, "errorcode: ''") {
		return true
	}
	if strings.Contains(normalized, "<context.") && strings.Contains(normalized, "{") {
		return true
	}
	if isPlaceholderAssertionValueLine(trimmed) || reStructuredFieldLine.MatchString(trimmed) {
		return true
	}
	return strings.HasPrefix(trimmed, "<*") && strings.Contains(trimmed, "{")
}

func isStatusBannerLine(line string) bool {
	normalized := strings.ToLower(collapseWS(line))
	return strings.HasPrefix(normalized, "response ") ||
		strings.HasPrefix(normalized, "error code unavailable") ||
		strings.HasPrefix(normalized, "response contained no body")
}

func bestX509CertificateMismatchDetail(text string) string {
	match := reX509CertificateMismatch.FindString(text)
	if match == "" {
		return ""
	}
	return collapseWS(match)
}

func shouldPreferX509CertificateMismatchDetail(picked string) bool {
	normalized := strings.ToLower(collapseWS(picked))
	if normalized == "" {
		return true
	}
	return strings.HasPrefix(normalized, "verifybasicaccess failed:") ||
		strings.HasPrefix(normalized, "verifyallapiservicesavailable failed:") ||
		strings.Contains(normalized, "tls: failed to verify certificate: x509:")
}

func isLowInformationCanonical(value string) bool {
	canonical := strings.TrimSpace(value)
	normalized := strings.ToLower(collapseWS(canonical))
	if normalized == "" {
		return true
	}
	if _, found := wrapperOnly[normalized]; found {
		return true
	}
	if reUnexpectedOnly.MatchString(canonical) {
		return true
	}
	if isStructBoundaryLine(canonical) || isStructFieldNoiseLine(canonical) {
		return true
	}
	if strings.Contains(normalized, "<context.") && strings.Contains(normalized, "{") {
		return true
	}
	return strings.Contains(normalized, "errorcode:\"\"") || strings.Contains(normalized, "errorcode: \"\"") || strings.Contains(normalized, "errorcode:''") || strings.Contains(normalized, "errorcode: ''")
}

func bestContextDeadlineDetail(text string) string {
	lines := splitNonEmptyLines(text)
	best := ""
	assertionFallback := ""
	for _, line := range lines {
		token := collapseWS(line)
		lowered := strings.ToLower(token)
		if token == "" {
			continue
		}
		// Ginkgo assertion headers ("fail [file.go:191]: <message>") are
		// normally treated as wrapper noise, but when the rest of the block is
		// generic ("Unexpected error:" / type annotation / bare "context
		// deadline exceeded" / "occurred") this header's message is the only
		// test-specific detail available. Capture it as a last-resort fallback
		// so distinct failures (e.g. different assertions that all end in a
		// deadline error) don't collapse into the same generic canonical text.
		if assertionFallback == "" {
			if detail := assertionHeaderDetail(token); detail != "" {
				assertionFallback = detail
			}
		}
		if isWrapperNoiseLine(token) || isStructFieldNoiseLine(token) || isStatusBannerLine(token) || isAssertionTail(token) {
			continue
		}
		if reRateLimiterDeadline.MatchString(token) {
			return token
		}
		if reClusterOperatorsUnavailable.MatchString(token) {
			best = token
			continue
		}
		if strings.Contains(lowered, "context deadline exceeded") && lowered != "context deadline exceeded" {
			best = token
		}
	}
	if best != "" {
		return best
	}
	if route := reRouteHostNeverFound.FindString(text); route != "" {
		return route
	}
	if assertionFallback != "" {
		return assertionFallback
	}
	return ""
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
