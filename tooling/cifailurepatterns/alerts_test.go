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
	"slices"
	"strings"
	"testing"
)

const alertPodTest = "[aro-hcp-observability] [hcp] alert KubePodNotReady does not fire"

func alertFixture(labels string) string {
	return "Firing 1:\n  State: Fired (not resolved)\n  Labels: " + labels + "\n  Description: Pod is not ready.\n"
}

func TestAlertPodSubjectBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		merge bool
	}{
		{"etcd ordinals", `pod="etcd-0"`, `pod="etcd-1"`, true},
		{"router replicas", `pod="router-767bb4bbcd-xqtnw"`, `pod="router-767bb4bbcd-abc12"`, true},
		{"distinct workloads", `pod="etcd-0"`, `pod="router-767bb4bbcd-xqtnw"`, false},
		{"namespace excluded", `pod="etcd-0", namespace="hcp-one"`, `pod="etcd-0", namespace="hcp-two"`, true},
		{"generic ordinals", `pod="database-0"`, `pod="database-1"`, true},
		{"generic replicas across revisions", `pod="api-767bb4bbcd-xqtnw"`, `pod="api-abcdef0123-abc12"`, true},
		{"short router suffix preserved", `pod="router-123-abc12"`, `pod="router-124-abc12"`, false},
		{"different ordinal owners", `pod="database-0"`, `pod="cache-0"`, false},
		{"different replica owners", `pod="api-767bb4bbcd-xqtnw"`, `pod="worker-767bb4bbcd-xqtnw"`, false},
		{"single generated suffix preserved", `pod="worker-abc12"`, `pod="worker-def34"`, false},
		{"numeric owner preserved", `pod="database-shard-2-0"`, `pod="database-shard-3-0"`, false},
		{"owner precedes name", `pod="api-one", owner_kind="Deployment", owner_name="api"`, `pod="api-two", owner_kind="Deployment", owner_name="api"`, true},
		{"owner kinds split", `pod="api-one", owner_kind="Deployment", owner_name="api"`, `pod="api-two", owner_kind="StatefulSet", owner_name="api"`, false},
		{"owner label conventions", `pod="api-one", owner_kind="Deployment", owner_name="api"`, `pod="api-two", workload_type="deployment", workload="api"`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, _ := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: alertFixture(test.left)})
			right, _ := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: alertFixture(test.right)})
			if len(left) != 1 || len(right) != 1 {
				t.Fatalf("unexpected findings: %+v / %+v", left, right)
			}
			if (left[0].Key == right[0].Key) != test.merge {
				t.Fatalf("merge=%v: %q / %q", test.merge, left[0].Key, right[0].Key)
			}
		})
	}
}

func TestAlertPodNormalizedSubjects(t *testing.T) {
	tests := []struct {
		pod  string
		want string
	}{
		{"api-server-767bb4bbcd-xqtnw", "api-server"},
		{"database-shard-12", "database-shard"},
		{"database-shard-2-0", "database-shard-2"},
		{"api-0-767bb4bbcd-xqtnw", "api-0"},
		{"worker-abc12", "worker-abc12"},
		{"custom-pod", "custom-pod"},
		{"api-767bb4bbcd-abcdef", "api-767bb4bbcd-abcdef"},
		{"api-767bb4bbcz-abc12", "api-767bb4bbcz-abc12"},
	}
	for _, test := range tests {
		t.Run(test.pod, func(t *testing.T) {
			findings, _ := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: alertFixture(`pod="` + test.pod + `"`)})
			if len(findings) != 1 {
				t.Fatalf("unexpected findings: %+v", findings)
			}
			finding := findings[0]
			if finding.Subject == nil || finding.Subject.Kind != "pod" || finding.Subject.Name != test.want {
				t.Fatalf("expected pod subject %q: %+v", test.want, finding)
			}
			if !slices.Equal(finding.Subject.ObservedNames, []string{test.pod}) {
				t.Fatalf("lost original pod: %+v", finding.Subject)
			}
			if slices.Contains(finding.QualityReasons, "name_normalized_without_verified_owner") != (test.want != test.pod) {
				t.Fatalf("incorrect normalization evidence: %+v", finding)
			}
		})
	}
}

func TestAlertMixedSubjectsAndRawLocations(t *testing.T) {
	blocks := []string{
		alertFixture(`alertname="KubePodNotReady", pod="etcd-0", namespace="hcp-one"`),
		alertFixture(`alertname="KubePodNotReady", pod="router-767bb4bbcd-xqtnw"`),
		alertFixture(`alertname="KubePodNotReady", pod="etcd-1", namespace="hcp-two"`),
		alertFixture(`alertname="OtherAlert", pod="unrelated"`),
	}
	prefix := "diagnostic π\nalert fired 4 time(s)\n"
	text := prefix + strings.Join(blocks, "")
	findings, ok := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: text})
	if !ok || len(findings) != 3 {
		t.Fatalf("expected etcd, router and generic: %+v", findings)
	}
	etcd := findings[0]
	if etcd.Subject.Name != "etcd" || !slices.Equal(etcd.Subject.ObservedNames, []string{"etcd-0", "etcd-1"}) || len(etcd.Locations) != 2 {
		t.Fatalf("lost merged evidence: %+v", etcd)
	}
	if etcd.Locations[0].Start != len(prefix) || text[etcd.Locations[0].Start:etcd.Locations[0].End] != blocks[0] || text[etcd.Locations[1].Start:etcd.Locations[1].End] != blocks[2] {
		t.Fatalf("raw byte locations not preserved: %+v", etcd.Locations)
	}
	if findings[2].RuleID != "alert.generic" || !slices.Contains(findings[2].QualityReasons, "alert_name_conflicts_with_test") {
		t.Fatalf("mixed alert was misattributed: %+v", findings[2])
	}
	alone, _ := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: blocks[0]})
	if alone[0].Key != etcd.Key {
		t.Fatal("identity depends on neighboring subjects")
	}
}

func TestAlertWeakEvidenceRemainsVisible(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		reason string
	}{
		{"missing labels", "Firing 1:\n Description: Pod etcd-0 is not ready.\n", "missing_alert_labels"},
		{"missing pod", alertFixture(`namespace="hcp-one"`), "missing_subject_evidence"},
		{"malformed labels", alertFixture(`pod="etcd-0`), "malformed_alert_labels"},
		{"trailing garbage", alertFixture(`pod="etcd-0" garbage`), "malformed_subject_evidence"},
		{"duplicate pod", alertFixture(`pod="etcd-0", pod="router-767bb4bbcd-xqtnw"`), "ambiguous_subject_evidence"},
		{"invalid pod", alertFixture(`pod="not a pod"`), "malformed_subject_evidence"},
		{"partial owner", alertFixture(`pod="etcd-0", owner_name="etcd"`), "malformed_subject_evidence"},
		{"conflicting owner", alertFixture(`pod="etcd-0", owner_kind="StatefulSet", owner_name="etcd", workload_type="deployment", workload="api"`), "ambiguous_subject_evidence"},
		{"unsupported format", "unexpected alert format", "missing_firing_blocks"},
		{"summary only", "alert fired 4 time(s)", "incomplete_firing_coverage"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, ok := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: test.text})
			if !ok || len(findings) != 1 {
				t.Fatalf("weak evidence disappeared: %+v", findings)
			}
			finding := findings[0]
			if finding.Subject.Kind != "unknown" || finding.Outcome != "fallback" || !slices.Contains(finding.QualityReasons, test.reason) {
				t.Fatalf("expected unknown with %s: %+v", test.reason, finding)
			}
			if test.name == "duplicate pod" && len(finding.Subject.ObservedNames) != 2 {
				t.Fatalf("ambiguous observed names lost: %+v", finding.Subject)
			}
		})
	}
}

func TestAlertIncompleteFiringCoverage(t *testing.T) {
	tests := []struct {
		summary       string
		gap           bool
		knownExcluded bool
	}{
		{"alert fired 4 time(s)", true, false},
		{"alert fired 4 time(s) (2 unknown, 2 known)", false, true},
		{"alert fired 4 time(s) (3 unknown, 1 known)", true, true},
		{"alert fired 2 time(s)", false, false},
	}
	for _, test := range tests {
		t.Run(test.summary, func(t *testing.T) {
			text := test.summary + "\n" + alertFixture(`pod="etcd-0"`) + alertFixture(`pod="etcd-1"`)
			findings, _ := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: text})
			if len(findings) != 1 || len(findings[0].Locations) != 2 || len(findings[0].Subject.ObservedNames) != 2 {
				t.Fatalf("must attribute only the two observed blocks: %+v", findings)
			}
			reasons := findings[0].QualityReasons
			if slices.Contains(reasons, "incomplete_firing_coverage") != test.gap || slices.Contains(reasons, "known_firings_excluded") != test.knownExcluded {
				t.Fatalf("unexpected coverage reasons: %v", reasons)
			}
		})
	}
}

func TestAlertScopeAndSource(t *testing.T) {
	input := Input{Source: "observability", TestName: alertPodTest, Text: alertFixture(`pod="etcd-0"`)}
	hcp, _ := extractAlerts(input)
	input.TestName = strings.Replace(alertPodTest, "[hcp]", "[svc]", 1)
	svc, _ := extractAlerts(input)
	if hcp[0].Key == svc[0].Key {
		t.Fatal("scope must distinguish identity")
	}
	input.TestName = ""
	input.Text = alertFixture(`alertname="KubePodNotReady", scope="hcp", pod="etcd-0"`)
	labels, _ := extractAlerts(input)
	if labels[0].Key != hcp[0].Key {
		t.Fatal("explicit label context must supply absent test context")
	}
	for _, source := range []string{"", "e2e", "rp-provision"} {
		input.Source = source
		if _, ok := extractAlerts(input); ok {
			t.Fatalf("guessed alert source for %q", source)
		}
	}
}

func TestGenericAlertGroupingIsExplicitlyCoarse(t *testing.T) {
	text := alertFixture(`alertname="UnknownAlert", scope="svc", pod="first"`) + alertFixture(`alertname="UnknownAlert", scope="svc", pod="second"`)
	findings, _ := extractAlerts(Input{Source: "observability", Text: text})
	if len(findings) != 1 || findings[0].Subject != nil || findings[0].Outcome != "fallback" || !slices.Contains(findings[0].QualityReasons, "coarse_alert_grouping") || len(findings[0].Locations) != 2 {
		t.Fatalf("generic rule must not group on arbitrary labels: %+v", findings)
	}
}

func TestAlertEmptyEvidence(t *testing.T) {
	for _, text := range []string{"", " \t\n"} {
		findings, ok := extractAlerts(Input{Source: "observability", TestName: alertPodTest, Text: text})
		if !ok || len(findings) != 1 || findings[0].Outcome != "missing_evidence" || len(findings[0].Locations) != 0 {
			t.Fatalf("missing evidence must not have a location: %+v", findings)
		}
	}
}

func TestCosmosAlertAccountNormalization(t *testing.T) {
	tests := []struct {
		prefix string
		merge  bool
	}{
		{"Cosmos DB Normalized RU Consumption High - ", true},
		{"Unrelated Alert - ", false},
	}
	for _, test := range tests {
		t.Run(test.prefix, func(t *testing.T) {
			extract := func(account string) Finding {
				findings, _ := extractAlerts(Input{
					Source:   "observability",
					TestName: "[aro-hcp-observability] [infra] alert " + test.prefix + account + " does not fire",
					Text:     "alert fired 1 time(s)",
				})
				return findings[0]
			}
			left := extract("arohcpci01-rp-j5016704")
			right := extract("arohcpci02-rp-j0055808")
			if (left.Key == right.Key) != test.merge {
				t.Fatalf("unexpected account grouping: %q / %q", left.Key, right.Key)
			}
			if test.merge && !strings.Contains(left.Canonical, "<cosmos-account>") {
				t.Fatalf("generated account retained in canonical: %s", left.Canonical)
			}
		})
	}
}
