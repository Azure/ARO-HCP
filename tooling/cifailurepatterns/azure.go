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
	"encoding/json"
	"sort"
	"strings"
)

type azureCodeHit struct {
	Code  string
	Index int
}

func providerAnchor(text string) string {
	pathMatches := reProviderPath.FindAllStringSubmatch(text, -1)
	for i := len(pathMatches) - 1; i >= 0; i-- {
		if len(pathMatches[i]) < 2 {
			continue
		}
		candidate := strings.TrimSpace(pathMatches[i][1])
		if candidate == "" || isIgnoredProvider(candidate) {
			continue
		}
		return candidate
	}

	textMatches := reProviderText.FindAllStringSubmatch(text, -1)
	for i := len(textMatches) - 1; i >= 0; i-- {
		if len(textMatches[i]) < 2 {
			continue
		}
		candidate := strings.TrimSpace(textMatches[i][1])
		if candidate == "" || isIgnoredProvider(candidate) {
			continue
		}
		return candidate
	}
	return ""
}

func isIgnoredProvider(value string) bool {
	switch value {
	case "Microsoft.Resources", "Microsoft.Azure.ARO":
		return true
	default:
		return strings.HasPrefix(value, "Microsoft.Azure.ARO.HCP")
	}
}
func extractLeafAzureDetail(text string, rootCode string) (string, string) {
	decoded := decodeEscapedErrorPayload(text)
	hits := collectAzureCodeHits(decoded)
	if len(hits) == 0 {
		return "", ""
	}

	messages := structuredAzureMessages(text)
	// The root fallback selects the last decoded root occurrence directly.
	// Plain ERROR CODE banners do not introduce another JSON message.
	rootStructured := messages[rootCode]
	remaining := make(map[string]int)
	for _, hit := range hits {
		remaining[hit.Code]++
	}
	for code, message := range messages {
		if message.Count > 1 && message.Count != remaining[code] {
			// Mixed structured and plain-text occurrences cannot be aligned safely.
			delete(messages, code)
		}
	}
	root := strings.ToLower(strings.TrimSpace(rootCode))
	fallbackCode := ""
	genericFallbackCode := ""
	for i := len(hits) - 1; i >= 0; i-- {
		code := strings.TrimSpace(hits[i].Code)
		lowered := strings.ToLower(code)
		remaining[code]--
		if lowered == "" || lowered == root {
			continue
		}
		if lowered == "resourcedeploymentfailure" || lowered == "deploymentfailed" {
			continue
		}
		if isLikelyTruncatedAzureCode(code, hits) {
			continue
		}

		message := extractAzureMessageAtHit(decoded, hits, i)
		if structured := messages[code]; structured.Count == 1 {
			message = structured.Message
		} else if index := remaining[code]; index >= 0 && index < len(structured.Occurrences) {
			message = structured.Occurrences[index]
		}
		message = summarizeAzureDetailMessage(message)
		message = appendAzureIdentityErrorDescription(message, decoded)
		if _, generic := genericCodes[lowered]; generic {
			if message != "" {
				return code, message
			}
			if genericFallbackCode == "" {
				genericFallbackCode = code
			}
			continue
		}
		if message != "" {
			return code, message
		}
		if fallbackCode == "" {
			fallbackCode = code
		}
	}
	if fallbackCode != "" {
		return fallbackCode, ""
	}
	if genericFallbackCode != "" {
		return genericFallbackCode, ""
	}
	rootMessage := extractAzureMessageForCode(decoded, rootCode, hits)
	if rootStructured.Count > 0 {
		rootMessage = rootStructured.Message
	}
	rootMessage = summarizeAzureDetailMessage(rootMessage)
	rootMessage = appendAzureIdentityErrorDescription(rootMessage, decoded)
	if rootMessage != "" {
		return "", rootMessage
	}
	return "", ""
}

func rootAzureErrorCode(text string) string {
	if match := reErrorCode.FindStringSubmatch(text); len(match) > 1 {
		code := strings.TrimSpace(match[1])
		if code != "" {
			return code
		}
	}
	if code := structuredAzureRootCode(text); code != "" {
		return code
	}
	hits := collectAzureCodeHits(decodeEscapedErrorPayload(text))
	for _, hit := range hits {
		code := strings.TrimSpace(hit.Code)
		if code == "" {
			continue
		}
		return code
	}
	return ""
}

func structuredAzureRootCode(text string) string {
	for {
		node, rest, ok := nextAzureJSONError(text)
		if !ok {
			return ""
		}
		if code := strings.TrimSpace(node.Code); code != "" {
			return code
		}
		if len(node.Error) > 0 {
			if code := structuredAzureRootCode(string(node.Error)); code != "" {
				return code
			}
		}
		text = rest
	}
}

func collectAzureCodeHits(text string) []azureCodeHit {
	out := make([]azureCodeHit, 0)
	errorCodeMatches := reErrorCode.FindAllStringSubmatchIndex(text, -1)
	for _, match := range errorCodeMatches {
		if len(match) < 4 {
			continue
		}
		code := strings.TrimSpace(text[match[2]:match[3]])
		if code == "" {
			continue
		}
		out = append(out, azureCodeHit{
			Code:  code,
			Index: match[0],
		})
	}

	codeFieldMatches := reCodeField.FindAllStringSubmatchIndex(text, -1)
	for _, match := range codeFieldMatches {
		if len(match) < 4 {
			continue
		}
		code := strings.TrimSpace(text[match[2]:match[3]])
		if code == "" {
			continue
		}
		out = append(out, azureCodeHit{
			Code:  code,
			Index: match[0],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].Code < out[j].Code
	})
	return out
}

func decodeEscapedErrorPayload(text string) string {
	out := text
	for range 2 {
		out = strings.ReplaceAll(out, `\\r\\n`, "\n")
		out = strings.ReplaceAll(out, `\\n`, "\n")
		out = strings.ReplaceAll(out, `\\t`, " ")
		out = strings.ReplaceAll(out, `\\\"`, `"`)
		out = strings.ReplaceAll(out, `\r\n`, "\n")
		out = strings.ReplaceAll(out, `\n`, "\n")
		out = strings.ReplaceAll(out, `\t`, " ")
		out = strings.ReplaceAll(out, `\"`, `"`)
		// Go's json.Marshal HTML-escapes '<', '>' and '&' as \u003c, \u003e
		// and \u0026 by default; several ClusterService error payloads carry
		// this literal escaping (e.g. "\u003cno_message\u003e"). Decode it so
		// downstream detail extraction sees the real placeholder text.
		out = strings.ReplaceAll(out, `\u003c`, "<")
		out = strings.ReplaceAll(out, `\u003e`, ">")
		out = strings.ReplaceAll(out, `\u0026`, "&")
	}
	return out
}

func isLikelyTruncatedAzureCode(code string, hits []azureCodeHit) bool {
	candidate := strings.ToLower(strings.TrimSpace(code))
	if candidate == "" {
		return true
	}
	for _, hit := range hits {
		other := strings.ToLower(strings.TrimSpace(hit.Code))
		if other == "" || len(other) <= len(candidate) {
			continue
		}
		if strings.HasPrefix(other, candidate) {
			return true
		}
	}
	return false
}

func extractAzureMessageForCode(text string, code string, hits []azureCodeHit) string {
	targetCode := strings.TrimSpace(code)
	if targetCode == "" {
		return ""
	}
	for i := len(hits) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(hits[i].Code), targetCode) {
			continue
		}
		return extractAzureMessageAtHit(text, hits, i)
	}
	return ""
}

// extractAzureMessageAtHit never searches a different occurrence of the code.
// In particular, an absent message must not borrow one from a sibling error.
func extractAzureMessageAtHit(text string, hits []azureCodeHit, index int) string {
	start := hits[index].Index
	end := len(text)
	if index+1 < len(hits) {
		end = hits[index+1].Index
	}
	match := reAzureMessage.FindStringSubmatchIndex(text[start:end])
	if len(match) < 4 {
		return ""
	}
	return strings.TrimSpace(text[start+match[2] : start+match[3]])
}

func summarizeAzureDetailMessage(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(trimmed), "clientcertificatecredential authentication failed") {
		if idx := strings.Index(strings.ToLower(trimmed), "clientcertificatecredential authentication failed"); idx >= 0 {
			prefix := trimmed[:idx+len("ClientCertificateCredential authentication failed")]
			return cleanCanonical(strings.TrimRight(prefix, " .") + ".")
		}
	}

	loweredRaw := strings.ToLower(trimmed)
	if strings.Contains(loweredRaw, `"code":`) ||
		strings.Contains(loweredRaw, `\"code\"`) ||
		strings.Contains(loweredRaw, `"status":`) ||
		strings.Contains(loweredRaw, `\"status\"`) {
		return ""
	}
	if strings.Count(trimmed, "{")+strings.Count(trimmed, "}") >= 2 {
		return ""
	}
	if match := reInvalidTemplateParameter.FindStringSubmatch(trimmed); len(match) > 1 {
		return `Deployment template validation failed: template parameter "` + strings.TrimSpace(match[1]) + `" is not provided.`
	}
	if strings.Contains(strings.ToLower(trimmed), "access is denied because of the deny assignment") {
		if match := reDenyAssignmentAction.FindStringSubmatch(trimmed); len(match) > 1 {
			return `Access denied by deny assignment for action "` + strings.TrimSpace(match[1]) + `".`
		}
		return "Access denied by deny assignment."
	}
	if strings.Contains(strings.ToLower(trimmed), "error while processing request for association ") {
		summary := "Error while processing network security perimeter association."
		if match := reNetworkAssociationError.FindStringSubmatch(trimmed); len(match) > 1 {
			summary = "Error while processing network security perimeter association: " + strings.TrimSpace(match[1])
		}
		return cleanCanonical(summary)
	}
	if deletionSummary := summarizeClusterServiceDeletionMessage(trimmed); deletionSummary != "" {
		return deletionSummary
	}
	if nodePoolSummary := summarizeClusterServiceNodePoolMessage(trimmed); nodePoolSummary != "" {
		return nodePoolSummary
	}
	if hostedClusterSummary := summarizeHostedClusterMessage(trimmed); hostedClusterSummary != "" {
		return hostedClusterSummary
	}

	normalized := cleanCanonical(trimmed)
	normalized = reQuotaRequiredAvailable.ReplaceAllString(normalized, "required <count>, available <count>")
	normalizedLower := strings.ToLower(normalized)
	if strings.Contains(normalizedLower, "requested") && strings.Contains(normalizedLower, "remaining") {
		normalized = reQuotaRequestedRemaining.ReplaceAllString(normalized, "requested <count>, remaining <count>")
	}
	if strings.Contains(normalizedLower, "please try again after") {
		normalized = reRetryAfterSeconds.ReplaceAllString(normalized, "Please try again after '<seconds>' seconds")
	}
	lowered := strings.ToLower(normalized)
	if idx := strings.Index(lowered, "allocation failed."); idx >= 0 {
		normalized = strings.TrimSpace(normalized[idx:])
		lowered = strings.ToLower(normalized)
	}
	for _, generic := range []string{
		"at least one resource deployment operation failed",
		"the resource write operation failed to complete successfully",
		"operation failed due to an internal server error",
		"internal server error",
	} {
		if strings.Contains(lowered, generic) {
			return ""
		}
	}

	if idx := strings.Index(lowered, " for more details,"); idx >= 0 {
		normalized = strings.TrimSpace(normalized[:idx])
		lowered = strings.ToLower(normalized)
	}
	if idx := strings.Index(lowered, " refer to "); idx >= 0 {
		normalized = strings.TrimSpace(normalized[:idx])
	}
	for _, marker := range []string{" please see ", " please wait for it to finish", " you may also use "} {
		if idx := strings.Index(strings.ToLower(normalized), marker); idx >= 0 {
			normalized = strings.TrimSpace(normalized[:idx])
		}
	}

	return normalized
}

func summarizeHostedClusterMessage(message string) string {
	if !strings.Contains(strings.ToLower(message), "[hypershifthostedcluster]") {
		return ""
	}
	normalized := cleanCanonical(message)
	normalized = reHostedClusterVersion.ReplaceAllString(normalized, `${1}<version>${2}`)
	normalized = reHostedClusterElapsed.ReplaceAllString(normalized, `${1}<duration>${2}`)
	hasUnavailableReplicas := strings.Contains(strings.ToLower(normalized), "unavailablereplicas:")
	parts := strings.Split(normalized, ";")
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if hasUnavailableReplicas && strings.Contains(strings.ToLower(part), "componentsnotavailable: waiting for components to be available:") {
			if marker := strings.Index(strings.ToLower(part), "componentsnotavailable:"); marker >= 0 {
				part = strings.TrimSpace(part[:marker+len("ComponentsNotAvailable")])
			}
		} else {
			part = normalizeCommaSeparatedDetail(part, "waiting for components to be available:")
		}
		part = normalizeUnavailableReplicas(part)
		parts[index] = part
	}
	return collapseExtraPartialVersionClauses(strings.Join(parts, "; "))
}

func collapseExtraPartialVersionClauses(value string) string {
	return collapseWS(reExtraPartialVersionClause.ReplaceAllString(value, ""))
}

func normalizeCommaSeparatedDetail(value string, marker string) string {
	lowered := strings.ToLower(value)
	index := strings.Index(lowered, marker)
	if index < 0 {
		return value
	}
	start := index + len(marker)
	items := sortedUniqueCommaList(value[start:])
	if len(items) == 0 {
		return value
	}
	return strings.TrimSpace(value[:start]) + " " + strings.Join(items, ", ")
}

func normalizeUnavailableReplicas(value string) string {
	const marker = "unavailablereplicas:"
	lowered := strings.ToLower(value)
	index := strings.Index(lowered, marker)
	if index < 0 {
		return value
	}
	start := index + len(marker)
	payload := strings.Trim(strings.TrimSpace(value[start:]), "[]")
	items := sortedUniqueCommaList(payload)
	if len(items) == 0 {
		return value
	}
	for i, item := range items {
		if match := reUnavailableWorkload.FindStringSubmatch(item); len(match) > 1 {
			items[i] = strings.TrimSpace(match[1])
		}
	}
	sort.Strings(items)
	items = compactStrings(items)
	return strings.TrimSpace(value[:start]) + " [" + strings.Join(items, ", ") + "]"
}

func sortedUniqueCommaList(value string) []string {
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	sort.Strings(items)
	return compactStrings(items)
}

func summarizeClusterServiceNodePoolMessage(message string) string {
	normalized := collapseWS(message)
	lowered := strings.ToLower(normalized)
	if !strings.Contains(lowered, "did not complete before the deadline") {
		return ""
	}
	if !strings.Contains(lowered, "node pool") && !strings.Contains(lowered, "nodepool") {
		return ""
	}
	if strings.Contains(lowered, "cluster deletion did not complete") ||
		strings.Contains(lowered, "cluster creation did not complete") {
		return ""
	}

	normalized = reNodePoolReplicasWantAutoscale.ReplaceAllString(normalized, "replicas is <count>, want >= <count> (autoscaling min)")
	normalized = reNodePoolReplicasWant.ReplaceAllString(normalized, "replicas is <count>, want <count>")
	normalized = reNodePoolMachineCounts.ReplaceAllString(normalized, "<count> of <count> machines")
	normalized = reCleanMachinePhrase.ReplaceAllString(normalized, "Machine <node>:")
	normalized = collapseDuplicateMachineStatus(normalized)
	return cleanCanonical(normalized)
}

func collapseDuplicateMachineStatus(text string) string {
	const marker = "Machine <node>:"
	lowerMarker := strings.ToLower(marker)
	lower := strings.ToLower(text)
	var b strings.Builder
	i := 0
	for {
		idx := strings.Index(lower[i:], lowerMarker)
		if idx < 0 {
			b.WriteString(text[i:])
			return b.String()
		}
		idx += i
		b.WriteString(text[i:idx])
		seen := make(map[string]struct{})
		j := idx
		wrote := false
		for j < len(lower) && strings.HasPrefix(lower[j:], lowerMarker) {
			end := indexMachineStatusEnd(lower, j+len(marker))
			reason := collapseWS(text[j:end])
			key := strings.ToLower(reason)
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				if wrote {
					b.WriteByte(' ')
				}
				wrote = true
				b.WriteString(reason)
			}
			j = end
			for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
		}
		i = j
	}
}

func indexMachineStatusEnd(lower string, from int) int {
	end := len(lower)
	if semi := strings.Index(lower[from:], ";"); semi >= 0 && from+semi < end {
		end = from + semi
	}
	if next := strings.Index(lower[from:], "machine <node>:"); next >= 0 && from+next < end {
		end = from + next
	}
	return end
}

func summarizeClusterServiceDeletionMessage(message string) string {
	normalized := collapseWS(message)
	lowered := strings.ToLower(normalized)
	if !strings.Contains(lowered, "cluster deletion did not complete before the deadline") {
		return ""
	}

	summary := []string{"cluster deletion did not complete before the deadline"}
	descendants := make([]string, 0)
	for _, rawPart := range strings.Split(normalized, ";") {
		part := strings.TrimSpace(rawPart)
		partLower := strings.ToLower(part)
		switch {
		case strings.Contains(partLower, "[clusterservicedeletion]") &&
			strings.Contains(partLower, "clusterservice cluster") &&
			strings.Contains(partLower, "still exists"):
			summary = append(summary, "[clusterServiceDeletion] ClusterService cluster still exists")
		case strings.Contains(partLower, "[clusterservicestatus]"):
			if match := reClusterServiceState.FindStringSubmatch(part); len(match) > 1 {
				summary = append(summary, `[clusterServiceStatus] state is "`+strings.ToLower(strings.TrimSpace(match[1]))+`"`)
			}
		case strings.Contains(partLower, "[hostedcluster]") && strings.Contains(partLower, "still exists"):
			summary = append(summary, "[hostedCluster] HostedCluster still exists")
		case strings.Contains(partLower, "[descendantresources]"):
			if idx := strings.Index(partLower, "remaining resources:"); idx >= 0 {
				for _, resource := range strings.Split(part[idx+len("remaining resources:"):], ",") {
					resource = reLeadingResourceCount.ReplaceAllString(strings.TrimSpace(resource), "")
					if slash := strings.LastIndex(resource, "/"); slash >= 0 {
						resource = resource[slash+1:]
					}
					if resource != "" {
						descendants = append(descendants, canonicalResourceKind(resource))
					}
				}
			}
		case strings.Contains(partLower, "[resourceteardown]"):
			part = reDeletionResourceCount.ReplaceAllString(part, "")
			summary = append(summary, collapseWS(part))
		case strings.Contains(partLower, "cluster deletion did not complete before the deadline"):
			continue
		default:
			part = reClusterServiceClusterRef.ReplaceAllString(part, "<cluster-id>")
			part = reDeletionDispatchedAt.ReplaceAllString(part, "")
			if part != "" {
				summary = append(summary, part)
			}
		}
	}
	if len(descendants) > 0 {
		sort.Strings(descendants)
		descendants = compactStrings(descendants)
		summary = append(summary, "[descendantResources] remaining resources: "+strings.Join(descendants, ", "))
	}
	return strings.Join(summary, "; ")
}

func canonicalResourceKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "nodepools":
		return "nodePools"
	case "serviceprovidernodepools":
		return "serviceProviderNodePools"
	case "serviceproviderclusters":
		return "serviceProviderClusters"
	default:
		return strings.TrimSpace(value)
	}
}

func appendAzureIdentityErrorDescription(message string, text string) string {
	if !strings.Contains(strings.ToLower(message), "clientcertificatecredential authentication failed") {
		return message
	}
	match := reAzureErrorDescription.FindStringSubmatch(text)
	if len(match) < 2 {
		return message
	}
	description := strings.TrimSpace(match[1])
	for _, marker := range []string{" Trace ID:", " Correlation ID:", " Timestamp:"} {
		if idx := strings.Index(description, marker); idx >= 0 {
			description = strings.TrimSpace(description[:idx])
		}
	}
	description = strings.TrimRight(description, " .") + "."
	if description == "." || strings.Contains(strings.ToLower(message), strings.ToLower(description)) {
		return message
	}
	return strings.TrimRight(message, " ;") + "; " + description
}

// structuredAzureMessages retains decoded messages in occurrence order, including
// empty messages, so repeated codes never borrow another occurrence's evidence.
func structuredAzureMessages(text string) map[string]structuredAzureMessage {
	messages := make(map[string]structuredAzureMessage)
	collectStructuredAzureMessages(text, messages)
	return messages
}

type structuredAzureMessage struct {
	Message     string
	Count       int
	Occurrences []string
}

type azureJSONError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Error   json.RawMessage   `json:"error"`
	Details []json.RawMessage `json:"details"`
}

func collectStructuredAzureMessages(text string, messages map[string]structuredAzureMessage) {
	for {
		node, rest, ok := nextAzureJSONError(text)
		if !ok {
			return
		}
		if node.Code != "" {
			message := messages[node.Code]
			message.Message = node.Message
			message.Count++
			message.Occurrences = append(message.Occurrences, node.Message)
			messages[node.Code] = message
		}
		if len(node.Error) > 0 {
			collectStructuredAzureMessages(string(node.Error), messages)
		}
		for _, detail := range node.Details {
			collectStructuredAzureMessages(string(detail), messages)
		}
		if strings.Contains(node.Message, "{") {
			collectStructuredAzureMessages(node.Message, messages)
		}
		text = rest
	}
}

func nextAzureJSONError(text string) (azureJSONError, string, bool) {
	for {
		start := strings.IndexByte(text, '{')
		if start < 0 {
			return azureJSONError{}, "", false
		}
		text = text[start:]
		decoder := json.NewDecoder(strings.NewReader(text))
		var node azureJSONError
		if err := decoder.Decode(&node); err != nil {
			text = text[1:]
			continue
		}
		return node, text[int(decoder.InputOffset()):], true
	}
}
