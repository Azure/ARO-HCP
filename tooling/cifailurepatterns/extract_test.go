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
	"reflect"
	"strings"
	"testing"
)

func TestExtractionOutcomesAndRules(t *testing.T) {
	tests := []struct {
		name      string
		input     Input
		outcome   string
		rule      string
		canonical string
	}{
		{"missing", Input{}, "missing_evidence", "input.empty", "failure occurred"},
		{"whitespace", Input{Text: " \n\t"}, "missing_evidence", "input.empty", "failure occurred"},
		{"diagnostic", Input{Text: "ordinary diagnostic text"}, "fallback", "fallback.last_line", "ordinary diagnostic text"},
		{"error heuristic", Input{Text: "widget failed in an unfamiliar way"}, "fallback", "fallback.error_line", "widget failed in an unfamiliar way"},
		{"route", Input{Text: "route host was never found: no such host"}, "matched", "network.route_host", "route host was never found: no such host"},
		{"http", Input{Text: "RESPONSE 502: 502 Bad Gateway\nERROR CODE UNAVAILABLE"}, "matched", "http.response_status", "RESPONSE 502: 502 Bad Gateway"},
		{"deserialization precedence", Input{Text: "Command Error: exit status 2\nDeserialization Error: no output from command"}, "matched", "pipeline.deserialization", "Deserialization Error: no output from command"},
		{"logfmt refinement", Input{Text: `time=2026-10-01T00:00:00Z level=error msg="Step errored." err="error running Shell Step, failed to execute shell command: login failed"`}, "matched", "pipeline.logfmt_error", "error running Shell Step, failed to execute shell command: login failed"},
		{"assertion", Input{Text: "Expected success, but got an error:\n    widget cannot be created"}, "matched", "assertion.context", "widget cannot be created"},
		{"bare deadline", Input{Text: "context deadline exceeded", TestName: "poll credentials"}, "matched", "timeout.deadline", "poll credentials: context deadline exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := Extract(tt.input)
			if len(findings) != 1 {
				t.Fatalf("got %d findings, want one", len(findings))
			}
			got := findings[0]
			if got.Outcome != tt.outcome || got.RuleID != tt.rule || got.Canonical != tt.canonical {
				t.Fatalf("got %+v; want outcome=%s rule=%s canonical=%q", got, tt.outcome, tt.rule, tt.canonical)
			}
			if got.Key == "" {
				t.Fatal("missing semantic key")
			}
			if tt.outcome != "matched" && len(got.QualityReasons) == 0 {
				t.Fatal("weak evidence must carry a quality reason")
			}
		})
	}
}

func TestIdentityPreservesLongEvidenceAndTestNames(t *testing.T) {
	prefix := "failed to reconcile " + strings.Repeat("persistent operation detail ", 30)
	a := Extract(Input{Text: prefix + "permission denied"})[0]
	b := Extract(Input{Text: prefix + "quota exceeded"})[0]
	if a.Key == b.Key || !strings.HasSuffix(a.Canonical, "permission denied") || !strings.HasSuffix(b.Canonical, "quota exceeded") {
		t.Fatalf("long failure causes were truncated or merged: %+v / %+v", a, b)
	}
	name := strings.Repeat("long test context ", 30)
	a = Extract(Input{Text: "Cluster provisioning failed", TestName: name + "first"})[0]
	b = Extract(Input{Text: "Cluster provisioning failed", TestName: name + "second"})[0]
	if a.Key == b.Key {
		t.Fatal("long test-name distinctions were truncated")
	}
}

func TestFindingIsIndependentOfOtherInputs(t *testing.T) {
	input := Input{Text: "route host was never found: no such host", TestName: "first"}
	before := Extract(input)
	Extract(Input{Text: "unrelated failure"})
	after := Extract(input)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("extraction depends on neighboring inputs: %+v / %+v", before, after)
	}
	otherContext := input
	otherContext.TestName = "second"
	otherContext.Source = "rp-upgrade"
	if before[0].Key != Extract(otherContext)[0].Key {
		t.Fatal("specific ordinary evidence must not depend on test name or reporting source")
	}
}

func TestRawEvidenceLocations(t *testing.T) {
	raw := "prefix: café\r\nroute host was never found: no such host\r\nfooter"
	finding := Extract(Input{Text: raw})[0]
	if len(finding.Locations) != 1 {
		t.Fatalf("expected selected evidence location, got %+v", finding.Locations)
	}
	span := finding.Locations[0]
	if got := raw[span.Start:span.End]; got != "route host was never found: no such host\r" {
		t.Fatalf("location must address original bytes, got %q", got)
	}
	if len(Extract(Input{})[0].Locations) != 0 {
		t.Fatal("missing input must not invent an evidence location")
	}
}

func TestAzureMetadataAndQuotedDetails(t *testing.T) {
	raw := `GET https://management.example.test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test
ERROR CODE: DeploymentFailed
{"error":{"code":"DeploymentFailed","details":[{"code":"InvalidRequestContent","message":"Invalid value: \"setting\": unrecognized experimental tag"}]}}`
	finding := Extract(Input{Text: raw})[0]
	if finding.RuleID != "azure.detail" || finding.Outcome != "matched" || finding.Provider != "Microsoft.RedHatOpenShift" || finding.OuterCode != "DeploymentFailed" || finding.DetailCode != "InvalidRequestContent" {
		t.Fatalf("unexpected Azure provenance: %+v", finding)
	}
	if !strings.Contains(finding.Canonical, `Invalid value: "setting": unrecognized experimental tag`) {
		t.Fatalf("JSON string detail was lost: %q", finding.Canonical)
	}
	plain := Extract(Input{Text: "widget failed"})[0]
	if plain.Provider != "" || plain.OuterCode != "" || plain.DetailCode != "" {
		t.Fatalf("invented Azure metadata: %+v", plain)
	}
}

func TestAzureMessageDoesNotLeakBetweenSiblingCodes(t *testing.T) {
	raw := `{"error":{"code":"DeploymentFailed","details":[{"code":"FirstFailure"},{"code":"SecondFailure","message":"second operation was denied"}]}}`
	decoded := decodeEscapedErrorPayload(raw)
	if message := extractAzureMessageForCode(decoded, "FirstFailure", collectAzureCodeHits(decoded)); message != "" {
		t.Fatalf("attributed sibling message to absent detail: %q", message)
	}
}

func TestUnavailableReplicaListsPreserveAllComponents(t *testing.T) {
	prefix := "UnavailableReplicas: [a, b, c, d, e, f, g, h, "
	a := normalizeUnavailableReplicas(prefix + "router]")
	b := normalizeUnavailableReplicas(prefix + "etcd]")
	if a == b || !strings.Contains(a, "router") || !strings.Contains(b, "etcd") {
		t.Fatalf("component lists overmerged: %q / %q", a, b)
	}
}

func TestUnknownSourceDoesNotInventAlertSubjects(t *testing.T) {
	findings := Extract(Input{Text: `Labels: alertname="KubePodNotReady", pod="etcd-0"`})
	if len(findings) != 1 || findings[0].Subject != nil || strings.HasPrefix(findings[0].RuleID, "alert.") {
		t.Fatalf("blank source was guessed as observability: %+v", findings)
	}
}

func TestAssertionContextWithoutListedMatcher(t *testing.T) {
	raw := "failed waiting for the hosted cluster deployment to stabilize\nExpected\n    <string>: pending\nto be equivalent to\n    <string>: ready"
	got := Extract(Input{Text: raw})[0]
	if got.Canonical != "failed waiting for the hosted cluster deployment to stabilize" || got.RuleID != "assertion.context" {
		t.Fatalf("lost assertion context: %+v", got)
	}
}

func TestGenericProvisioningRetainsQualityAfterContextualization(t *testing.T) {
	got := Extract(Input{
		Text:     "fail [test.go:158]: Cluster provisioning failed",
		TestName: "admin credential lifecycle should provision a cluster",
	})[0]
	if got.Canonical != "admin credential lifecycle should provision a cluster: Cluster provisioning failed" {
		t.Fatalf("lost generic test context: %+v", got)
	}
	if !reflect.DeepEqual(got.QualityReasons, []string{"generic_evidence"}) {
		t.Fatalf("context must not hide weak evidence: %+v", got)
	}
}

func TestNonemptyNoiseIsFallbackRatherThanMissing(t *testing.T) {
	got := Extract(Input{Text: "test.go:42"})[0]
	if got.Canonical != "failure occurred" || got.Outcome != "fallback" || got.RuleID != "fallback.no_actionable_line" {
		t.Fatalf("nonempty but unusable input must be an explicit fallback: %+v", got)
	}
}

func TestRepeatedEvidenceUsesConservativeLocation(t *testing.T) {
	raw := "msg: \"widget failed in an unfamiliar way\"\nwidget failed in an unfamiliar way"
	got := Extract(Input{Text: raw})[0]
	if got.Canonical != "widget failed in an unfamiliar way" || got.RuleID != "fallback.error_line" {
		t.Fatalf("expected the final actionable line, got %+v", got)
	}
	if !reflect.DeepEqual(got.Locations, []Location{{Start: 0, End: len(raw)}}) {
		t.Fatalf("repeated text must not claim the earlier wrapper's location: %+v", got.Locations)
	}
}

func TestAzureRepeatedCodeMessagesRemainOccurrenceSpecific(t *testing.T) {
	for _, tt := range []struct {
		name         string
		lastMessage  string
		wantLast     string
		wantSelected string
	}{
		{"distinct messages", `,"message":"second operation was denied"`, "second operation was denied", "second operation was denied"},
		{"missing last message", "", "", "first operation was denied"},
		{"generic last message", `,"message":"Internal server error."`, "Internal server error.", "first operation was denied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"error":{"code":"DeploymentFailed","details":[{"code":"RepeatedFailure","message":"first operation was denied"},{"code":"RepeatedFailure"` + tt.lastMessage + `}]}}`
			decoded := decodeEscapedErrorPayload(raw)
			hits := collectAzureCodeHits(decoded)
			if got := extractAzureMessageForCode(decoded, "RepeatedFailure", hits); got != tt.wantLast {
				t.Fatalf("latest occurrence borrowed another message: got %q, want %q", got, tt.wantLast)
			}
			if got := structuredAzureMessages(raw)["RepeatedFailure"]; got.Count != 2 {
				t.Fatalf("missing-message occurrences must remain represented: %+v", got)
			}
			finding := Extract(Input{Text: raw})[0]
			if finding.DetailCode != "RepeatedFailure" || !strings.Contains(finding.Canonical, "detail message "+tt.wantSelected) {
				t.Fatalf("expected last informative occurrence's own message, got %+v", finding)
			}
		})
	}
}

func TestAzureRepeatedQuotedCauses(t *testing.T) {
	extract := func(cause, tail string) Finding {
		raw := `{"error":{"code":"DeploymentFailed","details":[{"code":"RepeatedFailure","message":"Invalid value: \"setting\", ` + cause + `"},{"code":"RepeatedFailure"` + tail + `}]}}`
		return Extract(Input{Text: raw})[0]
	}
	for _, tail := range []string{"", `,"message":""`, `,"message":"Internal server error."`} {
		denied := extract("permission denied", tail)
		quota := extract("quota exceeded", tail)
		if !strings.Contains(denied.Canonical, `"setting", permission denied`) || !strings.Contains(quota.Canonical, `"setting", quota exceeded`) || denied.Key == quota.Key {
			t.Fatalf("quoted causes lost with tail %q: %+v / %+v", tail, denied, quota)
		}
		if same := extract("permission denied", tail); same.Key != denied.Key {
			t.Fatal("equivalent causes split")
		}
	}
	last := extract("permission denied", `,"message":"Invalid value: \"setting\", quota exceeded"`)
	if !strings.Contains(last.Canonical, `"setting", quota exceeded`) {
		t.Fatalf("last informative occurrence lost: %+v", last)
	}
}

func TestGomegaAzureResponsesPreserveCodesAndDetails(t *testing.T) {
	wrap := func(code, body string) string {
		return "Expected success, but got an error:\n    <*azcore.ResponseError>: \n    GET https://management.example.test/subscriptions/test/resource\n    --------------------------------------------------------------------------------\n    RESPONSE 403: Forbidden\n    ERROR CODE: " + code + "\n    --------------------------------------------------------------------------------\n    " + body
	}
	for _, test := range []struct {
		name, code, body, detail string
	}{
		{"authorization", "AuthorizationFailed", `{"error":{"code":"AuthorizationFailed","message":"permission denied"}}`, ""},
		{"throttling", "SubscriptionRequestsThrottled", `{"error":{"code":"SubscriptionRequestsThrottled","message":"quota exceeded"}}`, ""},
		{"nested authorization", "DeploymentFailed", `{"error":{"code":"DeploymentFailed","details":[{"code":"AuthorizationFailed","message":"permission denied"}]}}`, "AuthorizationFailed"},
		{"nested throttling", "DeploymentFailed", `{"error":{"code":"DeploymentFailed","details":[{"code":"SubscriptionRequestsThrottled","message":"quota exceeded"}]}}`, "SubscriptionRequestsThrottled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := Extract(Input{Text: strings.TrimPrefix(wrap(test.code, test.body), "Expected success, but got an error:\n    <*azcore.ResponseError>: \n    ")})[0]
			wrapped := Extract(Input{Text: wrap(test.code, test.body)})[0]
			if wrapped.OuterCode != test.code || wrapped.DetailCode != test.detail || !strings.Contains(wrapped.Canonical, test.code) {
				t.Fatalf("wrapped response lost Azure evidence: %+v", wrapped)
			}
			if wrapped.Key != plain.Key {
				t.Fatalf("Gomega wrapper changed Azure grouping: %+v / %+v", wrapped, plain)
			}
		})
	}
	a := Extract(Input{Text: wrap("AuthorizationFailed", "")})[0]
	b := Extract(Input{Text: wrap("SubscriptionRequestsThrottled", "")})[0]
	if a.Key == b.Key {
		t.Fatalf("distinct wrapped response codes merged: %+v / %+v", a, b)
	}
}

func TestGomegaHTTPResponsesPreserveStatus(t *testing.T) {
	const prefix = "Expected success, but got an error:\n    <*azcore.ResponseError>:\n    "
	var previous Finding
	for _, status := range []string{"502: 502 Bad Gateway", "503: 503 Service Unavailable"} {
		for _, method := range []string{"GET", "POST", "PATCH"} {
			plain := method + " https://management.example.test/resource\nRESPONSE " + status + "\nERROR CODE UNAVAILABLE\nResponse contained no body"
			wrapped := Extract(Input{Text: prefix + plain})[0]
			unwrapped := Extract(Input{Text: plain})[0]
			if !strings.Contains(wrapped.Canonical, "RESPONSE "+status) || wrapped.Key != unwrapped.Key {
				t.Fatalf("wrapper lost status or changed grouping: %+v / %+v", wrapped, unwrapped)
			}
			if previous.Key != "" && strings.HasPrefix(status, "503:") && wrapped.Key == previous.Key {
				t.Fatalf("different HTTP statuses merged: %+v / %+v", wrapped, previous)
			}
			if strings.HasPrefix(status, "502:") {
				previous = wrapped
			}
			meaningful := "failed waiting for hosted cluster readiness"
			withContext := Extract(Input{Text: prefix + meaningful + "\n" + plain})[0]
			withoutResponse := Extract(Input{Text: prefix + meaningful})[0]
			if withContext.Key != withoutResponse.Key {
				t.Fatalf("HTTP status displaced meaningful assertion context: %+v / %+v", withContext, withoutResponse)
			}
		}
	}
}

func TestRepeatedAzureRootMessagesPreserveQuotedCauses(t *testing.T) {
	response := func(cause string) string {
		return "ERROR CODE: InvalidRequestContent\n" + `{"error":{"code":"InvalidRequestContent","message":"Invalid value: \"setting\", ` + cause + `"}}`
	}
	var previous Finding
	for _, cause := range []string{"permission denied", "quota exceeded"} {
		single := Extract(Input{Text: response(cause)})[0]
		repeated := Extract(Input{Text: response(cause) + "\n" + response(cause)})[0]
		if !strings.Contains(repeated.Canonical, `"setting", `+cause) || repeated.Key != single.Key {
			t.Fatalf("repeated root lost quoted cause: %+v / %+v", repeated, single)
		}
		if previous.Key != "" && previous.Key == repeated.Key {
			t.Fatalf("distinct repeated root causes merged: %+v / %+v", previous, repeated)
		}
		previous = repeated
	}
	last := Extract(Input{Text: response("permission denied") + "\n" + response("quota exceeded")})[0]
	if !strings.Contains(last.Canonical, `"setting", quota exceeded`) || strings.Contains(last.Canonical, "permission denied") {
		t.Fatalf("wrong root occurrence selected: %+v", last)
	}
	for _, tail := range []string{`{"error":{"code":"InvalidRequestContent"}}`, `{"error":{"code":"InvalidRequestContent","message":""}}`} {
		missing := Extract(Input{Text: response("permission denied") + "\nERROR CODE: InvalidRequestContent\n" + tail})[0]
		if strings.Contains(missing.Canonical, "permission denied") || strings.Contains(missing.Canonical, "detail message") {
			t.Fatalf("root without a message borrowed an earlier occurrence: %+v", missing)
		}
	}
}

func TestShortAzureMessagesPreserveCauses(t *testing.T) {
	extract := func(message string, nested bool) Finding {
		body := `{"code":"InvalidRequestContent","message":"` + message + `"}`
		if nested {
			body = `{"code":"DeploymentFailed","details":[` + body + `]}`
		}
		return Extract(Input{Text: `{"error":` + body + `}`})[0]
	}
	for _, nested := range []bool{false, true} {
		var previous Finding
		for _, message := range []string{"permission denied", "quota exceeded", "Forbidden"} {
			got := extract(message, nested)
			if !strings.Contains(got.Canonical, "detail message "+message) {
				t.Fatalf("short cause discarded (nested=%t): %+v", nested, got)
			}
			if previous.Key != "" && previous.Key == got.Key {
				t.Fatalf("different short causes merged: %+v / %+v", previous, got)
			}
			previous = got
		}
		for _, message := range []string{"Internal server error.", "At least one resource deployment operation failed."} {
			if got := extract(message, nested); strings.Contains(got.Canonical, "detail message") {
				t.Fatalf("boilerplate became specific evidence: %+v", got)
			}
		}
	}
}

func TestMatchErrorUsesActualErrorNotExpectedRegexp(t *testing.T) {
	keys := make(map[string]string)
	for _, expected := range []string{"expected operation failure", "^a different expectation$"} {
		for _, actual := range []string{"permission denied", "connection refused"} {
			raw := "Expected\n    <*errors.errorString>:\n    " + actual + "\nto match error\n    <*matchers.MatchRegexpMatcher>: {\n        Regexp: \"" + expected + "\",\n    }"
			got := Extract(Input{Text: raw})[0]
			if !strings.Contains(got.Canonical, actual) || strings.Contains(got.Canonical, expected) {
				t.Fatalf("expected matcher replaced actual error: %+v", got)
			}
			if previous := keys[actual]; previous != "" && previous != got.Key {
				t.Fatalf("changing only the expected regexp split actual error %q", actual)
			}
			keys[actual] = got.Key
		}
	}
	if keys["permission denied"] == keys["connection refused"] {
		t.Fatal("different actual errors merged because they shared an expected regexp")
	}
}

func TestAzureOuterCodeIgnoresJSONPropertyOrder(t *testing.T) {
	var key string
	for _, body := range []string{
		`{"code":"DeploymentFailed","details":[{"code":"AuthorizationFailed","message":"permission denied"}]}`,
		`{"details":[{"code":"AuthorizationFailed","message":"permission denied"}],"code":"DeploymentFailed"}`,
		`{"details":[{"message":"permission denied","code":"AuthorizationFailed"}],"code":"DeploymentFailed"}`,
	} {
		for _, raw := range []string{body, `{"error":` + body + `}`} {
			got := Extract(Input{Text: raw})[0]
			if got.OuterCode != "DeploymentFailed" || got.DetailCode != "AuthorizationFailed" || !strings.Contains(got.Canonical, "permission denied") {
				t.Fatalf("JSON field order changed Azure evidence: %+v", got)
			}
			if key != "" && key != got.Key {
				t.Fatalf("equivalent JSON error representations split: %q / %q", key, got.Key)
			}
			key = got.Key
		}
	}
	fallback := Extract(Input{Text: `SDK error fields: "code":"DeploymentFailed", "code":"AuthorizationFailed", "message":"permission denied", "target":"setting"`})[0]
	if fallback.OuterCode != "DeploymentFailed" || fallback.DetailCode != "AuthorizationFailed" || !strings.Contains(fallback.Canonical, "permission denied") {
		t.Fatalf("non-JSON textual fallback lost evidence: %+v", fallback)
	}
}
