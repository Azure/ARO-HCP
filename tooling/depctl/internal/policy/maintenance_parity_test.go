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

package policy

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
)

func TestRetryHoldRequiresExactUneditedTrustedBody(t *testing.T) {
	body := "/hold\n\nRevision " + headSHA + " was retested 3 times: holding\n"
	for _, tc := range []struct {
		name, author, body, updated string
		retry                       bool
	}{
		{"exact", MergeBot, body, timeFixture, true},
		{"prefix", MergeBot, "explanation\n" + body, timeFixture, false},
		{"suffix", MergeBot, body + "extra", timeFixture, false},
		{"human", "reviewer", body, timeFixture, false},
		{"other bot", Robot, body, timeFixture, false},
		{"edited", MergeBot, body, timeLater, false},
		{"fractionally edited", MergeBot, body, "2026-11-05T15:00:00.000001Z", false},
		{"unknown sha", MergeBot, strings.ReplaceAll(body, headSHA, strings.Repeat("b", 40)), timeFixture, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holds, _, err := ActiveHolds([]forge.Comment{{ID: 1, Author: tc.author, Body: tc.body, CreatedAt: timeFixture, UpdatedAt: tc.updated}}, map[string]bool{headSHA: true})
			if err != nil || len(holds) != 1 || (holds[0].Kind == "retry-exhaustion") != tc.retry {
				t.Fatalf("holds=%v err=%v", holds, err)
			}
		})
	}
}

func TestHoldReplayCancellationAndDuplicateSemantics(t *testing.T) {
	hold := forge.Comment{ID: 1, Author: "reviewer", Body: "/hold", CreatedAt: timeFixture, UpdatedAt: timeFixture}
	for _, author := range []string{"reviewer", "other-reviewer", Robot, MergeBot} {
		cancel := forge.Comment{ID: 2, Author: author, Body: "/UNHOLD", CreatedAt: timeLater, UpdatedAt: timeLater}
		holds, pending, err := ActiveHolds([]forge.Comment{cancel, hold, hold}, map[string]bool{headSHA: true})
		want := 1
		if author == "reviewer" {
			want = 0
		}
		if err != nil || len(holds) != want || pending {
			t.Fatalf("author=%s holds=%v pending=%v err=%v", author, holds, pending, err)
		}
	}
	for _, body := range []string{"/hold\n/hold cancel", "/hold\n/remove-hold", "/hold\n/UnHoLd", "/hold\n/HOLD CANCEL reason"} {
		holds, _, err := ActiveHolds([]forge.Comment{{ID: 1, Author: "reviewer", Body: body, CreatedAt: timeFixture, UpdatedAt: timeFixture}}, nil)
		if err != nil || len(holds) != 0 {
			t.Fatalf("same-comment cancellation precedence: %q holds=%v err=%v", body, holds, err)
		}
	}
	changed := hold
	changed.Body = "/hold cancel"
	if _, _, err := ActiveHolds([]forge.Comment{hold, changed}, nil); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	// An earlier cancellation never clears a later human command.
	early := forge.Comment{ID: 2, Author: "reviewer", Body: "/hold cancel", CreatedAt: "2026-11-04T15:00:00Z", UpdatedAt: "2026-11-04T15:00:00Z"}
	holds, _, err := ActiveHolds([]forge.Comment{hold, early}, nil)
	if err != nil || len(holds) != 1 {
		t.Fatalf("holds=%v err=%v", holds, err)
	}
}

func TestTrustedHoldHistoryRejectsConflictsAndHumanRelabels(t *testing.T) {
	trusted := forge.HoldEvent{ID: 1, Event: "labeled", Actor: ProwBot, CreatedAt: timeFixture}
	for _, tc := range []struct {
		name          string
		events        []forge.HoldEvent
		trusted, fail bool
	}{
		{"empty", nil, false, false},
		{"trusted", []forge.HoldEvent{trusted, trusted}, true, false},
		{"human", []forge.HoldEvent{trusted, {ID: 2, Event: "labeled", Actor: "reviewer", CreatedAt: timeLater}}, false, false},
		{"removed", []forge.HoldEvent{trusted, {ID: 2, Event: "unlabeled", Actor: ProwBot, CreatedAt: timeLater}}, false, false},
		{"conflict", []forge.HoldEvent{trusted, {ID: 1, Event: "unlabeled", Actor: ProwBot, CreatedAt: timeFixture}}, false, true},
		{"invalid time", []forge.HoldEvent{{ID: 1, Event: "labeled", Actor: ProwBot, CreatedAt: "2026-01-01"}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TrustedHoldLabel(tc.events)
			if got != tc.trusted || (err != nil) != tc.fail {
				t.Fatalf("trusted=%v err=%v", got, err)
			}
		})
	}
}

func TestCILatestStatusTieAndNonPositiveConclusions(t *testing.T) {
	for _, statuses := range [][]forge.Status{
		{{ID: 1, Context: "job", State: "failure", CreatedAt: timeFixture}, {ID: 2, Context: "job", State: "success", CreatedAt: timeLater}},
		{{ID: 2, Context: "job", State: "success", CreatedAt: timeFixture}, {ID: 1, Context: "job", State: "failure", CreatedAt: timeFixture}},
	} {
		ci, err := CIEvidence(statuses, nil, headSHA)
		if err != nil || len(ci.Positive) != 1 || len(ci.Blocked) != 0 {
			t.Fatalf("ci=%v err=%v", ci, err)
		}
	}
	for _, conclusion := range []string{"success", "neutral", "skipped", "failure", "error", "timed_out", "cancelled", "action_required", "startup_failure", "unknown", ""} {
		ci, err := CIEvidence(nil, []forge.Check{{Name: "job", HeadSHA: headSHA, Status: "completed", Conclusion: conclusion}}, headSHA)
		if err != nil {
			t.Fatal(err)
		}
		positive := 0
		if conclusion == "success" {
			positive = 1
		}
		blocked := 1
		if conclusion == "success" || conclusion == "neutral" || conclusion == "skipped" {
			blocked = 0
		}
		if len(ci.Positive) != positive || len(ci.Blocked) != blocked {
			t.Fatalf("conclusion=%q ci=%v", conclusion, ci)
		}
	}
}

func TestMaintenanceKindsRejectsUnknownScopes(t *testing.T) {
	for _, kind := range []string{"", "upgrade", "dependency", "tool", "non-compiler"} {
		if _, err := MaintenanceKinds(kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := MaintenanceKinds("images"); err == nil {
		t.Fatal("unknown scope accepted")
	}
}
