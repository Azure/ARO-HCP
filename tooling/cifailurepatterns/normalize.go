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
	"sort"
	"strings"
)

func cleanCanonical(value string) string {
	text := value
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, `\r`, " ")
	text = strings.ReplaceAll(text, `\n`, " ")
	text = strings.ReplaceAll(text, `\t`, " ")
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = normalizeSwiftRouterChecks(text)
	text = reCleanWildcardDNSRecord.ReplaceAllString(text, "${1}<id>${2}")
	text = reCleanAzureVMReference.ReplaceAllStringFunc(text, normalizeGeneratedVMReference)
	text = reCleanFmtWrap.ReplaceAllString(text, " ")
	text = reCleanHexAddress.ReplaceAllString(text, " ")
	text = reCleanGoFileLine.ReplaceAllString(text, " ")
	text = reCleanUnexpectedPrefix.ReplaceAllString(text, "")
	text = reCleanWrapperPrefix.ReplaceAllString(text, "")
	text = reCleanURL.ReplaceAllString(text, "<url>")
	text = reCleanRedactedSubscription.ReplaceAllString(text, "/subscriptions/<subscription>")
	text = reCleanSubscription.ReplaceAllString(text, "/subscriptions/<subscription>")
	text = reCleanResourceGroupsPathSegment.ReplaceAllString(text, "/resourcegroups/<resource-group>/")
	if (strings.Contains(text, "/dbs/") || strings.Contains(text, "/DBS/")) &&
		(strings.Contains(text, "arohcpci") || strings.Contains(text, "AROHCPCI")) {
		text = reCleanCosmosDBPathSegment.ReplaceAllString(text, "${1}<cosmos-account>${2}")
	}
	text = reCleanVaultNameAlreadyInUse.ReplaceAllString(text, "the vault name '<vault-name>' is already in use.")
	text = reCleanUUID.ReplaceAllString(text, "<uuid>")
	text = reCleanHexLong.ReplaceAllString(text, "<hex>")
	text = reCleanLookupHostOnServer.ReplaceAllString(text, "lookup <host> on <dns-server>")
	text = reCleanResourceGroupQuoted.ReplaceAllString(text, `resourcegroup="<resource-group>"`)
	text = reCleanResourceGroupBare.ReplaceAllString(text, "resource group <resource-group>")
	text = reCleanResourceGroupSingle.ReplaceAllString(text, "resource group '<resource-group>'")
	text = reCleanClusterQuoted.ReplaceAllString(text, `cluster="<cluster>"`)
	text = reCleanClusterPhraseQuoted.ReplaceAllString(text, `cluster "<cluster>"`)
	text = reCleanClusterCreationQuoted.ReplaceAllString(text, `cluster creation "<cluster>"`)
	text = reCleanForClusterPhrase.ReplaceAllString(text, "for cluster <cluster>")
	text = reCleanInClusterPhrase.ReplaceAllString(text, "in cluster <cluster>")
	text = reCleanOnClusterPhrase.ReplaceAllString(text, "on cluster <cluster>")
	text = reCleanHCPClusterPhrase.ReplaceAllString(text, "HCP cluster <cluster>")
	text = reCleanClusterNamespaceName.ReplaceAllString(text, "cluster <resource-group>/<cluster>")
	text = reCleanExternalAuthQuoted.ReplaceAllString(text, `external auth "<external-auth>"`)
	text = reCleanExternalAuthBare.ReplaceAllString(text, "external auth <external-auth>")
	text = reCleanVMQuoted.ReplaceAllString(text, `VM "<vm>"`)
	text = reCleanPodQuoted.ReplaceAllString(text, `pod "<pod>"`)
	text = reCleanNamespaceQuoted.ReplaceAllString(text, `namespace "<namespace>"`)
	text = reCleanServiceAccountPath.ReplaceAllString(text, `service account <namespace>/$1`)
	text = reCleanNodePoolQuoted.ReplaceAllString(text, `nodepool="<nodepool>"`)
	text = reCleanNodePoolAssignment.ReplaceAllString(text, "nodePool=<nodepool>")
	text = reCleanNodePoolPhraseQuoted.ReplaceAllString(text, `node pool "<nodepool>"`)
	text = reCleanNodePoolPhrase.ReplaceAllString(text, "node pool <nodepool>")
	text = reCleanNodePoolBare.ReplaceAllString(text, "nodepool <nodepool>")
	text = reCleanAgentPool.ReplaceAllString(text, "agent pool <nodepool>")
	text = reCleanMachinePhrase.ReplaceAllString(text, "Machine <node>:")
	text = reCleanK8sNodeName.ReplaceAllString(text, "<node>")
	text = reCleanOCPChannel.ReplaceAllString(text, "<ocp-channel>")
	text = reCleanDialTCPAddress.ReplaceAllString(text, "dial tcp <ip>:<port>")
	text = reCleanDialingAddress.ReplaceAllString(text, "dialing <ip>:<port>")
	text = reCleanRouteHost.ReplaceAllString(text, "<route-host>")
	text = reCleanBreakglassPath.ReplaceAllString(text, "/hcpopenshiftclusters/<cluster>/breakglass/<session>/kubeconfig")
	text = reCleanRedactedQuoted.ReplaceAllString(text, `"<redacted-id>"`)
	text = reCleanBracketedIPv6.ReplaceAllString(text, "<ip>")
	text = strings.ReplaceAll(text, "::1", "<ip>")
	text = reCleanIP.ReplaceAllString(text, "<ip>")
	text = reCleanLogfmtTimestamp.ReplaceAllString(text, "")
	text = reCleanJSONTimeField.ReplaceAllString(text, "")
	text = reCleanISOTimestamp.ReplaceAllString(text, "<timestamp>")
	if strings.Contains(text, "/20") && (strings.Contains(text, " AM") || strings.Contains(text, " PM")) {
		text = reCleanHumanTimestamp.ReplaceAllString(text, "<timestamp>")
	}
	text = reCleanHCPApiHost.ReplaceAllString(text, "<hcp-api-host>")
	text = reCleanHCPOsadevHost.ReplaceAllString(text, "<hcp-api-host>")
	text = reCleanOCPNightly.ReplaceAllString(text, "<ocp-nightly>")
	text = reCleanOCPVersion.ReplaceAllString(text, "openshift-v<version>")
	text = reCleanCIOpID.ReplaceAllString(text, "ci-op-<id>")
	text = reCleanTmpTmpDir.ReplaceAllString(text, "/tmp/tmp.<id>")
	text = normalizeQuotedAzureResourcePath(text)
	text = reCleanQuotedOpaqueID.ReplaceAllString(text, "'<id>'")
	text = reCleanK8sLogPrefix.ReplaceAllString(text, "")
	text = reCleanMakeDirectory.ReplaceAllString(text, "")
	text = reTimeoutMinutesExceeded.ReplaceAllString(text, "timeout '<minutes>' minutes exceeded")
	text = reTemplateLineColumn.ReplaceAllString(text, "at line '<line>' and column '<column>'")
	text = reCleanResolveWithin.ReplaceAllString(text, "did not resolve within <duration>")
	text = reCleanSelector.ReplaceAllString(text, `selector "<selector>"`)
	text = reExpectedReadyNodes.ReplaceAllString(text, "expected <count> ready (and schedulable) nodes, found <count>")
	text = reCleanFailEmpty.ReplaceAllString(text, "")
	text = reCleanKlusterletDeploymentQuoted.ReplaceAllString(text, `${1}klusterlet-<cluster>-${2}${3}`)
	text = collapseWS(text)
	text = reGetPodsName.ReplaceAllString(text, "get pod <pod>")
	text = reNotReadyNodes.ReplaceAllString(text, `${1}[<node>]`)
	text = trimUnmatchedTerminalQuote(text)
	text = normalizeGeneratedNames(text)
	return text
}

// Keep the full verifier reason: identity is not a bounded timeout prefix.
// Scope and diagnostic fields here belong to the SWIFT verifier contract, not
// to arbitrary regions, counters, node names or timestamps elsewhere.
func normalizeSwiftRouterChecks(text string) string {
	if !strings.Contains(text, "VerifySwiftRouterChecks ") {
		return text
	}
	text = reSwiftTimeout.ReplaceAllString(text, "${1}<duration>:")
	text = reSwiftScope.ReplaceAllString(text, "<environment>/<region>/<management-cluster>/<namespace>")
	text = reSwiftNode.ReplaceAllString(text, "node=<node>")
	text = reSwiftSummaryIdentity.ReplaceAllString(text, "${1}=<id>")
	text = reSwiftReplicaCount.ReplaceAllString(text, "${1}=<count>")
	return text
}

func normalizeGeneratedVMReference(reference string) string {
	start := strings.LastIndexByte(reference, '/') + 1
	name := reference[start:]
	end := len(name)
	// CAPI VM references carry two generateName suffixes. The explicit VM
	// path permits letter-only suffixes without weakening the global guard.
	for range 2 {
		dash := strings.LastIndexByte(name[:end], '-')
		if dash < 1 || !kubernetesGeneratedSuffix(name[dash+1:end]) {
			return reference
		}
		end = dash
	}
	return reference[:start+end] + "-<id>"
}

func kubernetesGeneratedSuffix(value string) bool {
	if len(value) != 5 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("bcdfghjklmnpqrstvwxz2456789", c) {
			return false
		}
	}
	return true
}

func trimUnmatchedTerminalQuote(value string) string {
	text := strings.TrimSpace(value)
	if strings.Count(text, `"`)%2 == 0 {
		return text
	}
	if strings.HasSuffix(text, `"`) {
		return strings.TrimSpace(strings.TrimSuffix(text, `"`))
	}
	if strings.HasPrefix(text, `"`) {
		return strings.TrimSpace(strings.TrimPrefix(text, `"`))
	}
	return text
}

func normalizeExtractedCanonical(value string) string {
	normalized := normalizeTimedOutAfterDuration(value)
	normalized = stripTransportURLPrefixForTLSMismatch(normalized)
	normalized = stripReleaseFailureWrapper(normalized)
	normalized = stripUnhandledErrorWrapper(normalized)
	normalized = normalizeCleanupWorkflowDeletion(normalized)
	normalized = stripDefaultVMSizeWrapper(normalized)
	return normalized
}

func prepareCanonicalText(value string) string {
	normalized := collapseWS(value)
	normalized = normalizeOperationTimeout(normalized)
	normalized = stripReleaseFailureWrapper(normalized)
	normalized = normalizeServerPatchTimeouts(normalized)
	normalized = stripDefaultVMSizeWrapper(normalized)
	normalized = normalizeBreakglassKubeconfigFailure(normalized)
	return normalized
}

func normalizeTimedOutAfterDuration(value string) string {
	trimmed := strings.TrimSpace(value)
	if !reTimedOutAfterDuration.MatchString(trimmed) {
		return trimmed
	}
	return "Timed out after <duration>s."
}

func normalizeOperationTimeout(value string) string {
	trimmed := strings.TrimSpace(value)
	match := reOperationTimeout.FindStringSubmatch(trimmed)
	if len(match) < 2 {
		return trimmed
	}
	result := match[1] + " timed out after <minutes> minutes"
	if strings.Contains(strings.ToLower(trimmed), "context deadline exceeded") {
		result += "; context deadline exceeded"
	}
	return result
}

func contextualizeCanonicalWithTestName(canonical string, testName string) string {
	current := strings.TrimSpace(canonical)
	name := strings.TrimSpace(testName)
	if current == "" || name == "" {
		return current
	}
	if !isGenericAssertionCanonical(current) &&
		!isGenericTimedOutCanonical(current) &&
		!isGenericProvisioningCanonical(current) &&
		!strings.EqualFold(current, "context deadline exceeded") &&
		!strings.EqualFold(current, "failure occurred") &&
		!strings.EqualFold(current, "Interrupted by User") {
		return current
	}
	return collapseWS(name + ": " + current)
}

func isGenericAssertionCanonical(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "assertion failed: expected values to equal")
}

func isGenericTimedOutCanonical(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "Timed out after <duration>s.")
}

func isGenericProvisioningCanonical(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "Cluster provisioning failed")
}

func stripTransportURLPrefixForTLSMismatch(value string) string {
	trimmed := strings.TrimSpace(value)
	lowered := strings.ToLower(trimmed)
	const marker = `tls: failed to verify certificate:`
	if strings.HasPrefix(lowered, `get "<url>`) {
		if idx := strings.Index(lowered, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[idx:])
		}
	}
	return trimmed
}

func stripReleaseFailureWrapper(value string) string {
	trimmed := reStampPrefix.ReplaceAllString(strings.TrimSpace(value), "")
	lowered := strings.ToLower(trimmed)
	const helmPrefix = "error running helm release deployment step, failed to deploy helm release:"
	if strings.HasPrefix(lowered, helmPrefix) {
		return strings.TrimSpace(trimmed[len(helmPrefix):])
	}
	if strings.HasPrefix(lowered, `release "`) {
		if idx := strings.Index(lowered, `" failed: `); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(`" failed: `):])
		}
	}
	return trimmed
}

func normalizeCleanupWorkflowDeletion(value string) string {
	trimmed := strings.TrimSpace(value)
	lowered := strings.ToLower(trimmed)
	if !strings.Contains(lowered, "ordered cleanup workflow failed for") || !strings.Contains(lowered, "failed deleting") {
		return trimmed
	}

	normalized := reCleanupWorkflowTarget.ReplaceAllString(trimmed, `${1}<cleanup-target>${3}`)
	normalized = reCleanupWorkflowResourceName.ReplaceAllString(normalized, `${1}<cleanup-resource>${3}`)
	normalized = reCleanupWorkflowMethodURL.ReplaceAllString(normalized, ": <url>")
	return collapseWS(normalized)
}

func stripDefaultVMSizeWrapper(value string) string {
	trimmed := strings.TrimSpace(value)
	lowered := strings.ToLower(trimmed)
	if !strings.HasPrefix(lowered, "failed to resolve default vm size for node pool ") {
		return trimmed
	}
	if idx := strings.Index(lowered, `selector "`); idx >= 0 {
		return strings.TrimSpace(trimmed[idx:])
	}
	return trimmed
}

func normalizeBreakglassKubeconfigFailure(value string) string {
	trimmed := strings.TrimSpace(value)
	lowered := strings.ToLower(trimmed)
	if !strings.Contains(lowered, "failed to get ready session kubeconfig from ") ||
		!strings.Contains(lowered, "timeout waiting for session to become ready") {
		return trimmed
	}
	summary := "breakglass session kubeconfig not ready: timeout waiting for session to become ready"
	if strings.Contains(lowered, "hostedcontrolplane exists but is not ready") {
		summary += "; HostedControlPlane exists but is not ready"
	}
	return summary
}

func normalizeServerPatchTimeouts(value string) string {
	matches := reServerPatchTimeout.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return value
	}
	configMaps := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			configMaps = append(configMaps, strings.TrimSpace(match[1]))
		}
	}
	sort.Strings(configMaps)
	configMaps = compactStrings(configMaps)
	return "server timed out while patching configmaps: " + strings.Join(configMaps, ", ")
}

func stripUnhandledErrorWrapper(value string) string {
	trimmed := strings.TrimSpace(value)
	lowered := strings.ToLower(trimmed)
	if !strings.Contains(lowered, "unhandled error") {
		return trimmed
	}
	if idx := strings.Index(lowered, `err="`); idx >= 0 {
		return trimUnmatchedTerminalQuote(strings.TrimSpace(trimmed[idx+len(`err="`):]))
	}
	return trimmed
}

func normalizeQuotedAzureResourcePath(value string) string {
	return reCleanQuotedAzureResourcePath.ReplaceAllStringFunc(value, func(match string) string {
		inner := strings.Trim(match, `'`)
		parts := strings.Split(inner, "/")
		if len(parts) < 3 {
			return match
		}
		parts[len(parts)-1] = "<resource>"
		return "'" + strings.Join(parts, "/") + "'"
	})
}
