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

func extractAssertionContext(text string) string {
	lowered := strings.ToLower(text)
	if strings.Contains(lowered, "timed out after") && strings.Contains(lowered, "to be true") {
		if boolContext := extractBoolAssertionContext(text); boolContext != "" {
			return boolContext
		}
	}
	if strings.Contains(lowered, "expected") {
		if eventuallyContext := extractEventuallyFailureContext(text); eventuallyContext != "" {
			return eventuallyContext
		}
	}
	if strings.Contains(lowered, "expected success, but got an error:") {
		if successContext := extractGomegaSuccessFailureContext(text); successContext != "" {
			return successContext
		}
	}
	if strings.Contains(lowered, "operation result model did not match") {
		if modelDiffSummary := extractModelDiffSummaryContext(text); modelDiffSummary != "" {
			return modelDiffSummary
		}
	}
	if strings.Contains(lowered, "to equal") {
		if placeholderEquality := extractPlaceholderOnlyEqualityAssertionContext(text); placeholderEquality != "" {
			return placeholderEquality
		}
	}
	if strings.Contains(lowered, "expected an error to have occurred") && reExpectedErrorButNil.MatchString(text) {
		for _, line := range strings.Split(text, "\n") {
			if detail := assertionHeaderDetail(collapseWS(line)); detail != "" {
				return detail
			}
		}
	}

	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if !isAssertionTail(line) {
			continue
		}

		best := ""
		start := maxInt(0, index-30)
		for i := index - 1; i >= start; i-- {
			candidate := collapseWS(lines[i])
			if isNoiseAssertionContextLine(candidate) {
				continue
			}
			candidate = unwrapTimedOutAssertionWrapper(candidate)
			if reAssertionErrorSignal.MatchString(candidate) {
				return candidate
			}
			if best == "" && reASCIILetter.MatchString(candidate) {
				best = candidate
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

func likelyContainsAssertion(lowered string) bool {
	if strings.Contains(lowered, "expected success, but got an error:") ||
		strings.Contains(lowered, "operation result model did not match") ||
		containsTrimmedLine(lowered, "expected") {
		return true
	}
	for _, prefix := range assertionTailPrefixes {
		if strings.Contains(lowered, prefix) {
			return true
		}
	}
	return false
}

func containsTrimmedLine(text string, target string) bool {
	for {
		line, rest, found := strings.Cut(text, "\n")
		if strings.TrimSpace(line) == target {
			return true
		}
		if !found {
			return false
		}
		text = rest
	}
}

func unwrapTimedOutAssertionWrapper(value string) string {
	trimmed := strings.TrimSpace(value)
	match := reTimedOutAssertionWrapper.FindStringSubmatch(trimmed)
	if len(match) <= 1 {
		return trimmed
	}
	return strings.TrimSpace(match[1])
}

func extractModelDiffSummaryContext(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		candidate := collapseWS(line)
		if candidate == "" {
			continue
		}
		if reModelDiffSummary.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

func extractPlaceholderOnlyEqualityAssertionContext(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(strings.ToLower(collapseWS(line)), "to equal") {
			continue
		}
		sawPlaceholderValue := false
		sawMeaningfulContext := false
		start := maxInt(0, index-4)
		stop := minInt(len(lines), index+5)
		for i := start; i < stop; i++ {
			if i == index {
				continue
			}
			candidate := strings.TrimSpace(lines[i])
			if candidate == "" {
				continue
			}
			normalized := strings.ToLower(collapseWS(candidate))
			if normalized == "expected" || isAssertionTail(candidate) || isWrapperNoiseLine(candidate) {
				continue
			}
			if isPlaceholderAssertionValueLine(candidate) {
				sawPlaceholderValue = true
				continue
			}
			if isStructBoundaryLine(candidate) || isStructFieldNoiseLine(candidate) {
				continue
			}
			sawMeaningfulContext = true
			break
		}
		if sawPlaceholderValue && !sawMeaningfulContext {
			return "assertion failed: expected values to equal"
		}
	}
	return ""
}

func extractBoolAssertionContext(text string) string {
	match := reBoolAssertionContext.FindStringSubmatch(text)
	if len(match) == 0 {
		return ""
	}
	for i, name := range reBoolAssertionContext.SubexpNames() {
		if name == "context" && i < len(match) {
			return collapseWS(match[i])
		}
	}
	return ""
}

func extractEventuallyFailureContext(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if !isExpectedBlockStartLine(line) {
			continue
		}
		start := maxInt(0, index-12)
		for i := index - 1; i >= start; i-- {
			candidate := collapseWS(lines[i])
			if candidate == "" {
				continue
			}
			if isNoiseAssertionContextLine(candidate) || isEventuallyWrapperLine(candidate) || isTimedOutAfterLine(candidate) {
				continue
			}
			return unwrapTimedOutAssertionWrapper(candidate)
		}
	}
	return ""
}

func extractGomegaSuccessFailureContext(text string) string {
	matchIdx := reGomegaSuccessFailure.FindStringSubmatchIndex(text)
	if len(matchIdx) < 4 {
		return ""
	}
	candidate := strings.TrimSpace(text[matchIdx[2]:matchIdx[3]])
	if candidate == "" || candidate == "..." {
		return ""
	}
	if strings.HasSuffix(candidate, ":") {
		afterMatch := text[matchIdx[1]:]
		for _, line := range strings.Split(afterMatch, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed == "..." || strings.HasSuffix(trimmed, ":") {
				continue
			}
			return trimmed
		}
	}
	return candidate
}

func bestGomegaInnerError(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !reGomegaTypeAnnotation.MatchString(trimmed) {
			continue
		}
		for j := i + 1; j < len(lines) && j < i+5; j++ {
			candidate := strings.TrimSpace(lines[j])
			if candidate == "" || candidate == "..." {
				continue
			}
			if isStructBoundaryLine(candidate) {
				break
			}
			if isStructFieldNoiseLine(candidate) || isWrapperNoiseLine(candidate) {
				continue
			}
			if strings.ContainsAny(candidate, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				return candidate
			}
		}
	}
	return ""
}

func isExpectedBlockStartLine(line string) bool {
	return strings.EqualFold(collapseWS(line), "expected")
}

func isEventuallyWrapperLine(line string) bool {
	return reEventuallyWrapperLine.MatchString(collapseWS(line))
}

func isTimedOutAfterLine(line string) bool {
	return reTimedOutAfterLine.MatchString(collapseWS(line))
}

func isAssertionTail(line string) bool {
	normalized := strings.ToLower(collapseWS(line))
	for _, prefix := range assertionTailPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func isNoiseAssertionContextLine(line string) bool {
	normalized := collapseWS(line)
	lowered := strings.ToLower(normalized)
	if normalized == "" {
		return true
	}
	if isAssertionTail(normalized) {
		return true
	}
	if isEventuallyWrapperLine(normalized) || isTimedOutAfterLine(normalized) {
		return true
	}
	switch lowered {
	case "expected", "{", "}", "},", "]", "],", "(", ")":
		return true
	}
	if strings.HasPrefix(lowered, "expected") ||
		strings.HasPrefix(lowered, "learn more here:") ||
		strings.HasPrefix(lowered, "gomega truncated") ||
		strings.HasPrefix(lowered, "consider having") {
		return true
	}
	if strings.HasPrefix(lowered, "fail [") && strings.HasSuffix(lowered, ": expected") {
		return true
	}
	if isStructFieldNoiseLine(normalized) {
		return true
	}
	return strings.HasPrefix(normalized, "<") || strings.HasPrefix(normalized, "{") || strings.HasPrefix(normalized, "}")
}

func isPlaceholderAssertionValueLine(line string) bool {
	return rePlaceholderAssertionValue.MatchString(strings.TrimSpace(line))
}

// assertionHeaderDetail extracts the message following a Ginkgo
// "fail [file.go:line]:" marker, e.g. "failed to wait for first cluster
// \"basic-hcp-cluster\" to complete creation (timeout '20.000000' minutes)".
// Returns "" if there is no header, or the message itself is just the
// generic "context deadline exceeded" phrase.
func assertionHeaderDetail(line string) string {
	match := reFailAssertionHeader.FindStringSubmatch(line)
	if len(match) < 2 {
		return ""
	}
	detail := strings.TrimSpace(match[1])
	if detail == "" || strings.EqualFold(detail, "context deadline exceeded") {
		return ""
	}
	return detail
}

func extractAssertionHeaderContext(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if detail := assertionHeaderDetail(collapseWS(line)); detail != "" {
			return detail
		}
	}
	return ""
}
