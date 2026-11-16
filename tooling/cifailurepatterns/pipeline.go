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

func extractLogfmtStepError(raw string) string {
	const errField = ` err="`
	start := strings.LastIndex(raw, errField)
	if start < 0 {
		match := reLogfmtStepErroredErr.FindStringSubmatch(raw)
		if len(match) <= 1 {
			return ""
		}
		value := strings.TrimSpace(match[1])
		if value == "" {
			return ""
		}
		if refined := bestInnerStepErrorLine(unescapeLogfmtNewlines(value)); refined != "" {
			return refined
		}
		return value
	}
	start += len(errField)
	end := findUnescapedQuote(raw, start)
	value := strings.TrimSpace(raw[start:end])
	if value == "" {
		return ""
	}
	if refined := bestInnerStepErrorLine(unescapeLogfmtNewlines(value)); refined != "" {
		return refined
	}
	return value
}

func findUnescapedQuote(value string, start int) int {
	escaped := false
	for index := start; index < len(value); index++ {
		switch value[index] {
		case '\\':
			escaped = !escaped
		case '"':
			if !escaped {
				return index
			}
			escaped = false
		default:
			escaped = false
		}
	}
	return len(value)
}

func extractLogfmtReleaseStatusDescription(raw string) string {
	match := reLogfmtReleaseStatusDesc.FindStringSubmatch(raw)
	if len(match) <= 1 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func isReleaseStatusWrapperPick(value string) bool {
	return strings.Contains(strings.ToLower(collapseWS(value)), `msg="determined release status."`)
}

func bestCandidateGraphFailure(text string) string {
	lines := splitNonEmptyLines(text)
	best := ""
	for _, line := range lines {
		token := collapseWS(line)
		lowered := strings.ToLower(token)
		if !strings.Contains(lowered, "query candidate graph for") {
			continue
		}
		switch {
		case strings.Contains(lowered, "returned 503 service unavailable"):
			return token
		case strings.Contains(lowered, "no such host"):
			return token
		case strings.Contains(lowered, "client.timeout exceeded while awaiting headers"):
			return token
		case strings.Contains(lowered, "context deadline exceeded"):
			best = token
		}
	}
	return best
}

func bestImageMirrorInnerFailure(text string) string {
	if !strings.Contains(strings.ToLower(text), "error running image mirror step, failed to execute shell command:") {
		return ""
	}
	return bestInnerStepErrorLine(text)
}

func bestInnerStepErrorLine(text string) string {
	lines := splitNonEmptyLines(text)
	best := ""
	givingUp := ""
	for _, line := range lines {
		token := collapseWS(line)
		lowered := strings.ToLower(token)
		if token == "" || isAssertionTail(token) || isStructFieldNoiseLine(token) || isStatusBannerLine(token) {
			continue
		}
		if isStructuredStepWrapperPick(token) || isStepSetupNoiseLine(lowered) {
			continue
		}
		// Repetitive progress-polling lines (state-transition waits and
		// retry-loop attempts) carry no distinguishing signal beyond elapsed
		// seconds/attempt counts that vary run-to-run; skip them so they
		// never become the picked canonical text.
		if isProgressPollingNoiseLine(lowered) {
			continue
		}
		if givingUp == "" {
			if match := reGivingUpAfterAttempts.FindString(token); match != "" {
				givingUp = normalizeGivingUpAfterAttempts(token)
			}
		}
		if strings.HasPrefix(lowered, "error:") {
			best = token
			continue
		}
		// A bare "context deadline exceeded" line carries no distinguishing
		// detail on its own (see reCleanQuotedOpaqueID / wrapperOnly generic
		// handling elsewhere); prefer letting a genuinely distinguishing line
		// (or the unmodified raw value fallback) win instead of collapsing to
		// this wrapper phrase.
		if lowered == "context deadline exceeded" {
			continue
		}
		if rePickErrorSignal.MatchString(token) {
			best = token
		}
	}
	if best != "" {
		return best
	}
	return givingUp
}

// unescapeLogfmtNewlines converts literal "\r\n"/"\n"/"\r" escape sequences
// (as they appear inside a logfmt/JSON quoted string field) into real newline
// characters so downstream line-based noise filtering can operate per log
// line instead of treating the whole multi-line value as one unsplit token.
func unescapeLogfmtNewlines(value string) string {
	value = strings.ReplaceAll(value, `\r\n`, "\n")
	value = strings.ReplaceAll(value, `\n`, "\n")
	value = strings.ReplaceAll(value, `\r`, "\n")
	return value
}

// isProgressPollingNoiseLine matches repetitive step-progress log lines whose
// only variance is an elapsed-time or attempt counter, e.g.:
//
//	Waiting for swift-vnet-aks-net to finish (state: Running, elapsed 180s)...
//	[swift-vnet] dns readiness (login.microsoftonline.com): attempt 12 failed (elapsed 116s/480s; ...); retrying in 5s...
func isProgressPollingNoiseLine(lowered string) bool {
	return reWaitingToFinishProgressLine.MatchString(lowered) || reRetryAttemptProgressLine.MatchString(lowered)
}

func normalizeGivingUpAfterAttempts(line string) string {
	return reGivingUpAfterAttempts.ReplaceAllString(line, "giving up after <n> attempt(s) / <duration>s (limit <duration>s)")
}

func isStructuredStepWrapperPick(value string) bool {
	normalized := strings.ToLower(collapseWS(value))
	return strings.Contains(normalized, "error running image mirror step, failed to execute shell command:") ||
		strings.Contains(normalized, "error running helm release deployment step, failed to deploy helm release:") ||
		strings.Contains(normalized, "error running shell step, failed to execute shell command:") ||
		strings.Contains(normalized, "failed to run arm step:")
}

func isStepSetupNoiseLine(lowered string) bool {
	return strings.HasPrefix(lowered, "checking use_oc_login_registries:") ||
		strings.HasPrefix(lowered, "setting up registry authentication") ||
		strings.HasPrefix(lowered, "info: using registry public hostname") ||
		strings.HasPrefix(lowered, "saved credentials for registry") ||
		strings.HasPrefix(lowered, "logging into target acr") ||
		strings.HasPrefix(lowered, "login succeeded") ||
		strings.HasPrefix(lowered, "mirroring image ") ||
		strings.HasPrefix(lowered, "the image will still be available under")
}
