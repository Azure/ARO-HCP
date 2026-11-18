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
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	alertTestIdentity  = regexp.MustCompile(`\[([^\]]+)\]\s+alert (.+?) does not fire`)
	alertFiringStart   = regexp.MustCompile(`(?m)^[ \t]*Firing[ \t]+[0-9]+:`)
	alertSummaryCount  = regexp.MustCompile(`alert fired ([0-9]+) time\(s\)(?: \(([0-9]+) unknown, ([0-9]+) known\))?`)
	alertCosmosAccount = regexp.MustCompile(`\barohcpci[0-9]+-rp-j[0-9]+\b`)
	alertLabelPair     = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*("(?:[^"\\]|\\.)*")`)
	alertOrdinalPod    = regexp.MustCompile(`^([a-z0-9](?:[-a-z0-9.]*[a-z0-9])?)-[0-9]+$`)
	alertReplicaPod    = regexp.MustCompile(`^([a-z0-9](?:[-a-z0-9.]*[a-z0-9])?)-[a-f0-9]{8,10}-[a-z0-9]{5}$`)
	alertResourceName  = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9.]*[a-z0-9])?$`)
)

// alertObservation retains original values and byte offsets. Parsing never
// normalizes a name or uses arbitrary labels as identity dimensions.
type alertObservation struct {
	scope        string
	name         string
	labels       map[string]string
	description  string
	location     Location
	reasons      []string
	observedPods []string
}

func extractAlerts(input Input) ([]Finding, bool) {
	if input.Source != "observability" {
		return nil, false
	}
	scope, name := "", ""
	if match := alertTestIdentity.FindStringSubmatch(input.TestName); match != nil {
		scope, name = match[1], match[2]
	}
	starts := alertFiringStart.FindAllStringIndex(input.Text, -1)
	if name == "" && len(starts) == 0 && !strings.Contains(input.Text, "Labels:") {
		return nil, false
	}
	observations := make([]alertObservation, 0, max(1, len(starts)))
	if len(starts) == 0 {
		observation := parseAlertBlock(input.Text, Location{Start: 0, End: len(input.Text)}, scope, name)
		observation.reasons = append(observation.reasons, "missing_firing_blocks")
		observations = append(observations, observation)
	}
	for i, start := range starts {
		end := len(input.Text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		observations = append(observations, parseAlertBlock(input.Text[start[0]:end], Location{Start: start[0], End: end}, scope, name))
	}
	coverageGap := false
	knownExcluded := false
	if match := alertSummaryCount.FindStringSubmatch(input.Text); match != nil {
		expected := match[1]
		if match[2] != "" {
			// The producer puts known firings in system-out, not failure output.
			expected = match[2]
			known, err := strconv.Atoi(match[3])
			knownExcluded = err == nil && known > 0
		}
		count, err := strconv.Atoi(expected)
		coverageGap = err != nil || count > len(starts)
	}
	findings := make([]Finding, 0, len(observations))
	byKey := make(map[string]int, len(observations))
	for _, observation := range observations {
		finding := extractAlertFinding(observation)
		finding.QualityReasons = append(finding.QualityReasons, observation.reasons...)
		if coverageGap {
			finding.QualityReasons = append(finding.QualityReasons, "incomplete_firing_coverage")
		}
		if knownExcluded {
			finding.QualityReasons = append(finding.QualityReasons, "known_firings_excluded")
		}
		if strings.TrimSpace(input.Text) == "" {
			finding.Outcome = "missing_evidence"
		}
		if strings.TrimSpace(input.Text) != "" {
			finding.Locations = []Location{observation.location}
		}
		if index, found := byKey[finding.Key]; found {
			existing := &findings[index]
			existing.Locations = append(existing.Locations, finding.Locations...)
			existing.QualityReasons = alertUnique(append(existing.QualityReasons, finding.QualityReasons...))
			if existing.Subject != nil && finding.Subject != nil {
				existing.Subject.ObservedNames = alertUnique(append(existing.Subject.ObservedNames, finding.Subject.ObservedNames...))
			}
			continue
		}
		finding.QualityReasons = alertUnique(finding.QualityReasons)
		byKey[finding.Key] = len(findings)
		findings = append(findings, finding)
	}
	return findings, true
}

func parseAlertBlock(text string, location Location, scope, name string) alertObservation {
	observation := alertObservation{scope: scope, name: name, labels: map[string]string{}, location: location}
	hasLabels := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if description, ok := strings.CutPrefix(line, "Description:"); ok {
			observation.description = strings.TrimSpace(description)
		}
		labels, ok := strings.CutPrefix(line, "Labels:")
		if !ok {
			continue
		}
		hasLabels = true
		remaining := strings.TrimSpace(labels)
		if remaining == "" {
			observation.reasons = append(observation.reasons, "missing_alert_labels")
		}
		for remaining != "" {
			match := alertLabelPair.FindStringSubmatch(remaining)
			if match == nil {
				observation.reasons = append(observation.reasons, "malformed_alert_labels")
				break
			}
			value, err := strconv.Unquote(match[2])
			if err != nil {
				observation.reasons = append(observation.reasons, "malformed_alert_labels")
				break
			}
			if previous, exists := observation.labels[match[1]]; exists && previous != value {
				observation.reasons = append(observation.reasons, "ambiguous_alert_labels")
			}
			observation.labels[match[1]] = value
			if match[1] == "pod" && value != "" {
				observation.observedPods = append(observation.observedPods, value)
			}
			remaining = strings.TrimSpace(remaining[len(match[0]):])
			if remaining == "" {
				break
			}
			if remaining[0] != ',' {
				observation.reasons = append(observation.reasons, "malformed_alert_labels")
				break
			}
			remaining = strings.TrimSpace(remaining[1:])
			if remaining == "" {
				observation.reasons = append(observation.reasons, "malformed_alert_labels")
			}
		}
	}
	if !hasLabels {
		observation.reasons = append(observation.reasons, "missing_alert_labels")
	}
	// Per-firing explicit identity wins over the parent test's context. A
	// mismatch is visible rather than silently attributing a mixed block.
	if labelName := observation.labels["alertname"]; labelName != "" {
		if name != "" && name != labelName {
			observation.reasons = append(observation.reasons, "alert_name_conflicts_with_test")
		}
		observation.name = labelName
	}
	if labelScope := observation.labels["scope"]; labelScope != "" {
		if scope != "" && scope != labelScope {
			observation.reasons = append(observation.reasons, "alert_scope_conflicts_with_test")
		}
		observation.scope = labelScope
	}
	if observation.scope == "" {
		observation.scope = "unknown"
		observation.reasons = append(observation.reasons, "missing_alert_scope")
	}
	if observation.name == "" {
		observation.name = "unknown"
		observation.reasons = append(observation.reasons, "missing_alert_name")
	}
	return observation
}

func extractAlertFinding(observation alertObservation) Finding {
	switch observation.name {
	case "KubePodNotReady":
		return extractPodNotReadyAlert(observation)
	default:
		return extractGenericAlert(observation)
	}
}

func extractPodNotReadyAlert(observation alertObservation) Finding {
	subject := &Subject{Kind: "unknown", Name: "unknown"}
	finding := Finding{RuleID: "alert.kube_pod_not_ready", Outcome: "fallback", Subject: subject}
	subject.ObservedNames = alertUnique(observation.observedPods)
	switch {
	case slices.Contains(observation.reasons, "malformed_alert_labels"):
		finding.QualityReasons = []string{"malformed_subject_evidence"}
	case slices.Contains(observation.reasons, "ambiguous_alert_labels"):
		finding.QualityReasons = []string{"ambiguous_subject_evidence"}
	default:
		kind, name, reason := alertPodSubject(observation.labels)
		if reason != "" {
			finding.QualityReasons = []string{reason}
		}
		if name != "" {
			subject.Kind, subject.Name = kind, name
			finding.Outcome = "matched"
		}
	}
	finding.Key = alertIdentityKey(observation, subject.Kind, subject.Name)
	finding.Canonical = "alert [" + observation.scope + "] " + observation.name + " fired for " + subject.Kind + " " + subject.Name
	return finding
}

// Owner labels precede the pod-name fallback. Only these two explicit owner
// pairs are consumed; app, namespace and arbitrary labels are not ownership.
// Replica and ordinal suffixes are grouping heuristics, not verified ownership.
// Unrecognized names remain exact.
func alertPodSubject(labels map[string]string) (string, string, string) {
	ownerKind, ownerName := "", ""
	for _, pair := range [][2]string{{"workload_type", "workload"}, {"owner_kind", "owner_name"}} {
		kind, name := labels[pair[0]], labels[pair[1]]
		if kind == "" && name == "" {
			continue
		}
		if kind == "" || name == "" || !alertResourceName.MatchString(name) {
			return "", "", "malformed_subject_evidence"
		}
		kind = strings.ToLower(kind)
		switch kind {
		case "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob":
		default:
			return "", "", "unsupported_owner_kind"
		}
		if ownerName != "" && (ownerName != name || ownerKind != kind) {
			return "", "", "ambiguous_subject_evidence"
		}
		ownerKind, ownerName = kind, name
	}
	if ownerName != "" {
		return ownerKind, ownerName, ""
	}
	pod := labels["pod"]
	if pod == "" {
		return "", "", "missing_subject_evidence"
	}
	if !alertResourceName.MatchString(pod) {
		return "", "", "malformed_subject_evidence"
	}
	if name := normalizeAlertPodName(pod); name != pod {
		return "pod", name, "name_normalized_without_verified_owner"
	}
	return "pod", pod, "unrecognized_pod_name_preserved"
}

// normalizeAlertPodName strips one recognized suffix pattern, never recursively.
func normalizeAlertPodName(pod string) string {
	if match := alertReplicaPod.FindStringSubmatch(pod); match != nil {
		return match[1]
	}
	if match := alertOrdinalPod.FindStringSubmatch(pod); match != nil {
		return match[1]
	}
	return pod
}

func extractGenericAlert(observation alertObservation) Finding {
	observation.name = normalizeCosmosAlertName(observation.name)
	return Finding{
		Canonical:      "alert [" + observation.scope + "] " + observation.name + " fired",
		Key:            alertIdentityKey(observation, "", ""),
		Outcome:        "fallback",
		RuleID:         "alert.generic",
		QualityReasons: []string{"coarse_alert_grouping"},
	}
}

// Only Cosmos DB alert names have the generated account suffix contract.
// Unrelated alert names that happen to contain the same text remain exact.
func normalizeCosmosAlertName(name string) string {
	if !strings.HasPrefix(name, "Cosmos DB ") {
		return name
	}
	return alertCosmosAccount.ReplaceAllString(name, "<cosmos-account>")
}

func alertIdentityKey(observation alertObservation, kind, name string) string {
	return "alert:" + strconv.Quote(observation.scope) + ":" + strconv.Quote(observation.name) + ":" + strconv.Quote(kind) + ":" + strconv.Quote(name)
}

func alertUnique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}
