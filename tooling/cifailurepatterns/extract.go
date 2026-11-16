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
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Extract selects deterministic evidence from a single observation. Empty input
// still yields a finding, so missing evidence cannot silently drop failed inputs.
func Extract(input Input) []Finding {
	if findings, handled := extractAlerts(input); handled {
		return findings
	}
	if strings.TrimSpace(input.Text) == "" {
		return []Finding{{
			Canonical: "failure occurred", Key: patternKey("missing_evidence", input.TestName),
			Outcome: "missing_evidence", RuleID: "input.empty",
			QualityReasons: []string{"empty_failure_message"},
		}}
	}
	return []Finding{extractError(input)}
}

// patternKey hashes complete semantic fields, never a bounded display excerpt.
// The fixed family prefix separates absent evidence from ordinary semantic text.
func patternKey(family, semantic string) string {
	sum := sha256.Sum256([]byte(family + "\x00" + semantic))
	return "error:" + hex.EncodeToString(sum[:])
}

type evidencePick struct {
	text    string
	rule    string
	outcome string
}

func matched(text, rule string) evidencePick {
	return evidencePick{text: text, rule: rule, outcome: "matched"}
}

func extractError(input Input) Finding {
	raw := input.Text
	lowered := strings.ToLower(raw)
	provider := providerAnchor(raw)
	code := rootAzureErrorCode(raw)
	var pick evidencePick
	if likelyContainsAssertion(lowered) {
		if context := extractAssertionContext(raw); context != "" {
			if code == "" {
				rule := "assertion.context"
				if reHTTPRequestLine.MatchString(context) {
					if status := bestHTTPResponseStatusLine(raw); status != "" {
						context = status
						rule = "http.response_status"
					}
				}
				canonical := normalizeExtractedCanonical(cleanCanonical(prepareCanonicalText(context)))
				if !strings.EqualFold(canonical, "context deadline exceeded") {
					return finishFinding(input, matched(context, rule), canonical, provider, "", "")
				}
				// A bare deadline is a wrapper; let the existing timeout
				// refinement below recover the accompanying failure detail.
				pick = matched(context, rule)
			}
			// A Gomega wrapper can select only the SDK request line. Keep the
			// Azure code and allow the normal detail refinements below.
			if code != "" {
				pick = matched("ERROR CODE: "+code, "azure.error_code")
			}
		}
	}

	if pick.text == "" {
		pick = selectErrorEvidence(raw, lowered)
	}
	if strings.Contains(lowered, "x509:") {
		if detail := bestX509CertificateMismatchDetail(raw); detail != "" && shouldPreferX509CertificateMismatchDetail(pick.text) {
			pick = matched(detail, "transport.x509")
		}
	}
	if strings.EqualFold(strings.TrimSpace(pick.text), "cluster provisioning failed") {
		if code := reErrorCode.FindString(raw); code != "" {
			pick = matched(code, "azure.error_code")
		}
	}
	if detail := refineDeserializationNoOutputPicked(raw, lowered, pick.text); detail != pick.text {
		pick = matched(detail, "pipeline.deserialization")
	}
	if detail := refineCommandErrorExitStatusOnly(raw, pick.text); detail != pick.text {
		pick = evidencePick{text: detail, rule: "fallback.command_detail", outcome: "fallback"}
	}

	leafCode, leafMessage := "", ""
	canonical := normalizeExtractedCanonical(cleanCanonical(prepareCanonicalText(pick.text)))
	if code != "" && (isGenericCode(code) || isAlwaysDetailedProvider(provider)) {
		leafCode, leafMessage = extractLeafAzureDetail(raw, code)
		parts := []string{"ERROR CODE: " + code}
		if leafCode != "" {
			parts = append(parts, "detail code "+leafCode)
		}
		if leafMessage != "" {
			parts = append(parts, "detail message "+leafMessage)
		}
		if leafCode == "" && leafMessage == "" {
			if context := extractAssertionHeaderContext(raw); context != "" {
				parts = append(parts, "context "+cleanCanonical(context))
			}
		}
		if provider != "" {
			parts = append(parts, "provider "+provider)
		}
		canonical = strings.Join(parts, "; ")
		pick = matched(raw, "azure.detail")
	}
	if strings.Contains(lowered, "context deadline exceeded") && reCreateHCPClusterTimeout.MatchString(lowered) {
		canonical = "timeout during CreateHCPClusterAndWait; context deadline exceeded"
		pick = matched(raw, "timeout.cluster_creation")
	}
	if strings.Contains(lowered, "getadminrestconfigforhcpcluster") && strings.Contains(lowered, "timeout") {
		canonical = "timeout during GetAdminRESTConfigForHCPCluster while waiting for hcpcluster creds"
		pick = matched(raw, "timeout.admin_credentials")
	}
	if strings.Contains(lowered, "interrupted by user") {
		canonical = "Interrupted by User"
		pick = matched(raw, "operation.interrupted")
	}
	if containsDeserializationErrorToken(pick.text) || containsDeserializationErrorToken(canonical) {
		if detail := reDeserializationLiteral.FindString(raw); detail != "" {
			canonical = cleanCanonical(detail)
			pick = matched(detail, "pipeline.deserialization")
		}
	}
	if strings.EqualFold(strings.TrimSpace(canonical), "context deadline exceeded") {
		pick = matched(pick.text, "timeout.deadline")
		if detail := bestContextDeadlineDetail(raw); detail != "" {
			canonical = cleanCanonical(detail)
			pick = matched(raw, "timeout.contextual_deadline")
		}
	}
	if _, wrapper := wrapperOnly[strings.ToLower(canonical)]; wrapper || reUnexpectedOnly.MatchString(canonical) {
		if parts := reCauseBySplit.Split(raw, -1); len(parts) > 1 {
			pick = matched(strings.TrimSpace(parts[len(parts)-1]), "error.caused_by")
			canonical = cleanCanonical(pick.text)
		}
	}
	if isLowInformationCanonical(canonical) {
		if detail := bestSignalErrorLine(raw); detail != "" {
			pick = evidencePick{text: detail, rule: "fallback.error_line", outcome: "fallback"}
			canonical = cleanCanonical(detail)
		} else if detail := bestGomegaInnerError(raw); detail != "" {
			pick = matched(detail, "assertion.inner_error")
			canonical = cleanCanonical(detail)
		}
	}
	if code != "" && !isGenericCode(code) && !isAlwaysDetailedProvider(provider) && provider != "" &&
		strings.HasPrefix(strings.ToLower(canonical), "error code:") && !strings.Contains(strings.ToLower(canonical), "; provider ") {
		canonical += "; provider " + provider
	}
	return finishFinding(input, pick, canonical, provider, code, leafCode)
}

func finishFinding(input Input, pick evidencePick, canonical, provider, code, leafCode string) Finding {
	if strings.TrimSpace(canonical) == "" {
		canonical = "failure occurred"
		pick = evidencePick{text: input.Text, rule: "fallback.no_actionable_line", outcome: "fallback"}
	}
	var reasons []string
	if pick.outcome == "fallback" {
		reasons = append(reasons, "unrecognized_failure_text")
	}
	lowered := strings.ToLower(canonical)
	if isLowInformationCanonical(canonical) || isGenericAssertionCanonical(canonical) || isGenericTimedOutCanonical(canonical) || isGenericProvisioningCanonical(canonical) ||
		lowered == "failure occurred" || lowered == "step errored." || lowered == "context deadline exceeded" || lowered == "interrupted by user" ||
		(isGenericCode(code) && leafCode == "" && !strings.Contains(lowered, "detail message ")) {
		reasons = append(reasons, "generic_evidence")
	}
	if strings.Contains(lowered, "<no_message>") {
		reasons = append(reasons, "upstream_missing_detail")
	}
	canonical = contextualizeCanonicalWithTestName(canonical, input.TestName)
	location := Location{Start: 0, End: len(input.Text)}
	if pick.text != "" {
		// Text-only selectors do not retain offsets. A repeated substring may
		// belong to an earlier wrapper rather than the selected occurrence.
		// Narrow the span only when its occurrence is unambiguous.
		if start := strings.Index(input.Text, pick.text); start >= 0 && start == strings.LastIndex(input.Text, pick.text) {
			location = Location{Start: start, End: start + len(pick.text)}
		}
	}
	return Finding{
		Canonical: canonical, Key: patternKey("ordinary", strings.ToLower(collapseWS(canonical))),
		Outcome: pick.outcome, RuleID: pick.rule, QualityReasons: reasons,
		Provider: provider, OuterCode: code, DetailCode: leafCode, Locations: []Location{location},
	}
}

type textRule struct {
	id      string
	needle  string
	pattern *regexp.Regexp
}

// The first matching explicit rule wins, before structured pipeline refinements.
var errorTextRules = []textRule{
	{"pipeline.deserialization", "deserializa", regexp.MustCompile(`(?i)Deserializa(?:ti|i)on Error:[^\n]+`)},
	{"pipeline.command_error", "command error:", regexp.MustCompile(`(?i)Command Error:[^\n]+`)},
	{"network.route_host", "route host was never found:", reRouteHostNeverFound},
	{"cluster.operators_unavailable", "cluster operators not available:", reClusterOperatorsUnavailable},
	{"timeout.rate_limiter", "client rate limiter wait returned an error:", reRateLimiterDeadline},
	{"logs.missing_sources", "missing expected log sources", regexp.MustCompile(`(?i)missing expected log sources[^\n]+`)},
	{"logs.gather_failed", "failed to gather logs", regexp.MustCompile(`(?i)failed to gather logs[^\n]+`)},
	{"exporter.service_missing", "failed to get service aro-hcp-exporter/aro-hcp-exporter", regexp.MustCompile(`(?i)failed to get service aro-hcp-exporter/aro-hcp-exporter: services "aro-hcp-exporter" not found`)},
	{"azure.managed_resource_groups", "failed to search for managed resource groups:", regexp.MustCompile(`(?i)failed to search for managed resource groups:[^\n]+`)},
	{"breakglass.session_creation", "failed to create sre breakglass session:", regexp.MustCompile(`(?i)failed to create SRE breakglass session:[^\n]+`)},
	{"azure.error_code", "error code:", reErrorCode},
	{"http.not_found", "response 404:", regexp.MustCompile(`(?i)response 404:[^\n]+`)},
	{"timeout.nodepool_creation", "createnodepoolfromparam", regexp.MustCompile(`(?i)timeout '\d+\.\d+' minutes exceeded during CreateNodePoolFromParam[^\n]*`)},
	{"timeout.nodepool_wait", "failed waiting for nodepool", regexp.MustCompile(`(?i)failed waiting for nodepool[^\n]+(?:updating|to finish creating)[^\n]*`)},
	{"timeout.nodepool_update", "updatenodepoolandwait", regexp.MustCompile(`(?i)UpdateNodePoolAndWait[^\n]+minutes exceeded[^\n]*`)},
	{"timeout.cluster_creation", "createhcpclusterfromparam", regexp.MustCompile(`(?i)timeout '\d+\.\d+' minutes exceeded during CreateHCPClusterFromParam[^\n]*`)},
	{"pipeline.image_mirror", "error running image mirror step", regexp.MustCompile(`(?i)error running Image Mirror Step, failed to execute shell command:[^\n]+`)},
	{"pipeline.helm", "error running helm release deployment step", regexp.MustCompile(`(?i)error running Helm release deployment Step, failed to deploy helm release:[^\n]+`)},
	{"pipeline.shell", "error running shell step", regexp.MustCompile(`(?i)error running Shell Step, failed to execute shell command:[^\n]+`)},
	{"pipeline.arm", "failed to run arm step:", regexp.MustCompile(`(?i)failed to run ARM step:[^\n]+`)},
	{"cluster.provisioning", "cluster provisioning failed", regexp.MustCompile(`(?i)Cluster provisioning failed`)},
	{"operation.interrupted", "interrupted by user", regexp.MustCompile(`(?i)Interrupted by User`)},
}

func selectErrorEvidence(raw, lowered string) evidencePick {
	var pick evidencePick
	for _, rule := range errorTextRules {
		if !strings.Contains(lowered, rule.needle) {
			continue
		}
		if text := rule.pattern.FindString(raw); text != "" {
			pick = matched(text, rule.id)
			break
		}
	}
	if strings.Contains(lowered, "level=") {
		if detail := extractLogfmtStepError(raw); detail != "" && (pick.text == "" || isStructuredStepWrapperPick(pick.text)) {
			pick = matched(detail, "pipeline.logfmt_error")
		}
		if detail := extractLogfmtReleaseStatusDescription(raw); detail != "" && (pick.text == "" || isReleaseStatusWrapperPick(pick.text)) {
			pick = matched(detail, "pipeline.release_status")
		}
	}
	if strings.Contains(lowered, "query candidate graph for") {
		if detail := bestCandidateGraphFailure(raw); detail != "" {
			pick = matched(detail, "upgrade.candidate_graph")
		}
	}
	if strings.Contains(lowered, "image mirror") {
		if detail := bestImageMirrorInnerFailure(raw); detail != "" {
			pick = matched(detail, "pipeline.image_mirror_detail")
		}
	}
	if pick.text != "" {
		return pick
	}
	if match := reProwEntrypointMsg.FindStringSubmatch(raw); len(match) > 1 {
		return matched(strings.TrimSpace(match[1]), "pipeline.prow_entrypoint")
	}
	if detail := bestSignalErrorLine(raw); detail != "" {
		return evidencePick{text: detail, rule: "fallback.error_line", outcome: "fallback"}
	}
	if detail := bestHTTPResponseStatusLine(raw); detail != "" {
		return matched(detail, "http.response_status")
	}
	if parts := reCauseBySplit.Split(raw, -1); len(parts) > 1 {
		return matched(strings.TrimSpace(parts[len(parts)-1]), "error.caused_by")
	}
	lines := splitNonEmptyLines(raw)
	for i := len(lines) - 1; i >= 0; i-- {
		if !isAssertionTail(lines[i]) && !isStructFieldNoiseLine(lines[i]) && !isStatusBannerLine(lines[i]) {
			return evidencePick{text: lines[i], rule: "fallback.last_line", outcome: "fallback"}
		}
	}
	return evidencePick{text: "failure occurred", rule: "fallback.no_actionable_line", outcome: "fallback"}
}
