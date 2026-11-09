// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cifailures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

func TestReportFindingsEqualDirectExtractor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input cifailurepatterns.Input
	}{
		{"missing", cifailurepatterns.Input{}},
		{"whitespace", cifailurepatterns.Input{Text: " \r\n\t "}},
		{"fallback", cifailurepatterns.Input{Text: "ordinary diagnostic text", Source: "rp-upgrade"}},
		{"raw locations", cifailurepatterns.Input{Text: "prefix: café\r\nroute host was never found: no such host\r\nfooter", Source: "e2e"}},
		{"test context", cifailurepatterns.Input{Text: "context deadline exceeded", TestName: "poll credentials", Source: "rp-provision"}},
		{"azure provenance", cifailurepatterns.Input{Text: "GET https://management.example.test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test\nERROR CODE: DeploymentFailed\n" + `{"error":{"code":"DeploymentFailed","details":[{"code":"InvalidRequestContent","message":"Invalid value: \"setting\": unrecognized experimental tag"}]}}`, Source: "rp-provision"}},
		{"alert subjects", cifailurepatterns.Input{
			Source:   "observability",
			TestName: "[aro-hcp-observability] [hcp] alert KubePodNotReady does not fire",
			Text:     "diagnostic π\nalert fired 3 time(s)\nFiring 1:\n  Labels: alertname=\"KubePodNotReady\", pod=\"etcd-0\"\nFiring 2:\n  Labels: alertname=\"KubePodNotReady\", pod=\"router-767bb4bbcd-xqtnw\"\nFiring 3:\n  Labels: alertname=\"KubePodNotReady\", pod=\"etcd-1\"\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := testBundle()
			observation := bundle.Observations[0]
			observation.Text, observation.TestName, observation.Source = tc.input.Text, tc.input.TestName, tc.input.Source
			observation.StartedAt = bundle.Metadata.Window.From.Add(time.Minute)
			observation.SetupFinishTime = observation.StartedAt.Add(time.Minute)
			observation.TestStartTime = observation.SetupFinishTime.Add(time.Minute)
			observation.CleanupStartTime = observation.TestStartTime.Add(time.Minute)
			observation.FinishedAt = observation.CleanupStartTime.Add(time.Minute)
			observation.NameVariants = 3
			observation.Fingerprint = Fingerprint(observation)
			bundle.Observations = []Observation{observation}
			report, err := BuildReport(bundle)
			if err != nil {
				t.Fatal(err)
			}
			checkPatternReferences(t, report)
			if len(report.Results) != 1 {
				t.Fatalf("expected one result, got %+v", report.Results)
			}
			result := report.Results[0]
			if !reflect.DeepEqual(result.Observation, observation) {
				t.Fatalf("observation changed: got %+v, want %+v", result.Observation, observation)
			}
			want := cifailurepatterns.Extract(tc.input)
			if !reflect.DeepEqual(result.Findings, want) {
				t.Fatalf("findings differ from direct extraction: got %+v, want %+v", result.Findings, want)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 2 || fields["observation"] == nil || fields["findings"] == nil {
				t.Fatalf("result must contain only observation and findings: %s", encoded)
			}
			var decoded Result
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, result) {
				t.Fatalf("JSON lost observation or findings: got %+v, want %+v", decoded, result)
			}
		})
	}
}

func TestReportRetainsDuplicateOccurrencesAndContext(t *testing.T) {
	bundle := testBundle()
	original := bundle.Observations[0]
	renamed := original
	renamed.TestName = "late-arriving corrected test name"
	ambiguous := original
	ambiguous.NameVariants = 7
	unnamed := original
	unnamed.TestName = ""
	otherRun := original
	otherRun.BuildID = "run-2"
	otherRun.Fingerprint = Fingerprint(otherRun)
	bundle.Observations = []Observation{renamed, original, ambiguous, original, unnamed, otherRun}
	want := make(map[Observation]int)
	for _, observation := range bundle.Observations {
		want[observation]++
		if observation.BuildID == original.BuildID && observation.Fingerprint != original.Fingerprint {
			t.Fatal("test contexts must share an immutable input fingerprint")
		}
	}
	report, err := BuildReport(bundle)
	if err != nil {
		t.Fatal(err)
	}
	checkPatternReferences(t, report)
	if report.Summary.InputCount != 6 || report.Summary.AffectedRunCount != 2 || report.Summary.SourceInputCounts["e2e"] != 6 {
		t.Fatalf("repeated input occurrences or exact run counts were lost: %+v", report.Summary)
	}
	if len(report.Results) != len(bundle.Observations) {
		t.Fatalf("lost duplicate or context-specific occurrences: got %d, want %d", len(report.Results), len(bundle.Observations))
	}
	got := make(map[Observation]int)
	for _, result := range report.Results {
		got[result.Observation]++
		observation := result.Observation
		findings := cifailurepatterns.Extract(cifailurepatterns.Input{Text: observation.Text, TestName: observation.TestName, Source: observation.Source})
		if !reflect.DeepEqual(result.Findings, findings) {
			t.Fatalf("context-specific findings were merged: got %+v, want %+v", result.Findings, findings)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("occurrence population changed: got %+v, want %+v", got, want)
	}
}

func TestReportPreservesLongEvidenceAndEveryInput(t *testing.T) {
	bundle := testBundle()
	original := bundle.Observations[0]
	bundle.Observations = nil
	const total = 257
	for i := range total {
		observation := original
		observation.TestID = fmt.Sprintf("test-%03d", i)
		observation.Source = fmt.Sprintf("source-%03d", i)
		observation.TestName = strings.Repeat("long test context 界 ", 100) + fmt.Sprint(i)
		observation.Text = "  diagnostic π\r\nfailed to reconcile " + strings.Repeat("persistent operation detail 界 ", 150) + fmt.Sprintf("permission denied %03d\r\n\t  ", i)
		observation.Fingerprint = Fingerprint(observation)
		bundle.Observations = append(bundle.Observations, observation)
	}
	report, err := BuildReport(bundle)
	if err != nil {
		t.Fatal(err)
	}
	checkPatternReferences(t, report)
	if report.Summary.PatternCount != total || len(report.Patterns) != total {
		t.Fatalf("patterns were capped: got %d, want %d", len(report.Patterns), total)
	}
	if len(report.Results) != total {
		t.Fatalf("inputs were capped: got %d, want %d", len(report.Results), total)
	}
	want := make(map[string]Observation, total)
	for _, observation := range bundle.Observations {
		want[observation.TestID] = observation
	}
	for _, result := range report.Results {
		observation, exists := want[result.Observation.TestID]
		if !exists || !reflect.DeepEqual(result.Observation, observation) {
			t.Fatalf("long input was omitted, duplicated, or clipped: %+v", result.Observation)
		}
		findings := cifailurepatterns.Extract(cifailurepatterns.Input{Text: observation.Text, TestName: observation.TestName, Source: observation.Source})
		if !reflect.DeepEqual(result.Findings, findings) {
			t.Fatalf("long finding was clipped or changed: got %+v, want %+v", result.Findings, findings)
		}
		delete(want, observation.TestID)
	}
	if len(want) != 0 {
		t.Fatalf("omitted %d inputs", len(want))
	}
}

func TestReportPreservesAllAlertFindingsNamesAndLocations(t *testing.T) {
	bundle := testBundle()
	observation := bundle.Observations[0]
	observation.Source = "observability"
	observation.TestName = "[aro-hcp-observability] [hcp] alert KubePodNotReady does not fire"
	var evidence strings.Builder
	evidence.WriteString("diagnostic π\nalert fired 232 time(s)\n")
	for i := range 25 {
		fmt.Fprintf(&evidence, "Firing %d:\n  Labels: alertname=\"KubePodNotReady\", pod=\"etcd-%d\"\n", i+1, i)
	}
	for i := range 207 {
		fmt.Fprintf(&evidence, "Firing %d:\n  Labels: alertname=\"KubePodNotReady\", pod=\"component-%03d-0\"\n", i+26, i)
	}
	observation.Text = evidence.String()
	observation.Fingerprint = Fingerprint(observation)
	bundle.Observations = []Observation{observation}
	want := cifailurepatterns.Extract(cifailurepatterns.Input{Text: observation.Text, TestName: observation.TestName, Source: observation.Source})
	if len(want) <= 200 || want[0].Subject == nil || len(want[0].Subject.ObservedNames) != 25 || len(want[0].Locations) != 25 {
		t.Fatalf("fixture must exercise uncapped findings, names, and locations: %+v", want)
	}
	report, err := BuildReport(bundle)
	if err != nil {
		t.Fatal(err)
	}
	checkPatternReferences(t, report)
	if report.Summary.InputCount != 1 || report.Summary.FindingCount != len(want) ||
		report.Summary.AffectedRunCount != 1 || report.Summary.PatternCount != len(want) {
		t.Fatalf("multi-finding input totals were conflated: %+v", report.Summary)
	}
	if len(report.Results) != 1 || !reflect.DeepEqual(report.Results[0].Observation, observation) || !reflect.DeepEqual(report.Results[0].Findings, want) {
		t.Fatalf("alert findings, order, names, or locations changed: %+v", report.Results)
	}
}

func TestReportOrdersByCompleteSerializedObservation(t *testing.T) {
	bundle := testBundle()
	original := bundle.Observations[0]
	original.StartedAt = bundle.Metadata.Window.From.Add(time.Minute)
	original.FinishedAt = original.StartedAt.Add(time.Minute)
	original.Fingerprint = Fingerprint(original)
	renamed := original
	renamed.TestName = "another context"
	ambiguous := original
	ambiguous.NameVariants = 9
	startedZone := original
	startedZone.StartedAt = original.StartedAt.In(time.FixedZone("east", 3600))
	finishedZone := original
	finishedZone.FinishedAt = original.FinishedAt.In(time.FixedZone("west", -3600))
	bundle.Observations = []Observation{ambiguous, startedZone, original, renamed, finishedZone, original}
	want := make([]string, 0, len(bundle.Observations))
	for _, observation := range bundle.Observations {
		if Fingerprint(observation) != original.Fingerprint {
			t.Fatal("sort fixture must vary context without varying the fingerprint")
		}
		encoded, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, string(encoded))
	}
	slices.Sort(want)
	var first []byte
	for permutation := range 3 {
		before, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		report, err := BuildReport(bundle)
		if err != nil {
			t.Fatal(err)
		}
		checkPatternReferences(t, report)
		got := make([]string, 0, len(report.Results))
		for _, result := range report.Results {
			encoded, err := json.Marshal(result.Observation)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, string(encoded))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d ignored exact serialized context: got %v, want %v", permutation, got, want)
		}
		after, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("report reordered or changed the input bundle")
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if permutation == 0 {
			first = encoded
		}
		if !bytes.Equal(first, encoded) {
			t.Fatal("input order changed report JSON")
		}
		slices.Reverse(bundle.Observations)
		if permutation == 1 {
			bundle.Observations = append(bundle.Observations[1:], bundle.Observations[0])
		}
	}
}
