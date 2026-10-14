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

package cijoboutcomes

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

func TestProwFamilyFor(t *testing.T) {
	for _, tc := range []struct {
		prefix, job, want string
	}{
		{"pr-logs/pull/batch/", "pull-ci-Azure-ARO-HCP-main-e2e-parallel", "batch"},
		{"pr-logs/pull/Azure_ARO-HCP/6670/", "pull-ci-Azure-ARO-HCP-main-e2e-parallel", "presubmit"},
		{"logs/", "periodic-ci-Azure-ARO-HCP-main-e2e-parallel", "periodic"},
		{"logs/", "branch-ci-Azure-ARO-HCP-main-e2e-parallel", "gating"},
		{"logs/", "unknown-job", "unknown"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			url := "https://prow.ci.openshift.org/view/gs/test-platform-results/" + tc.prefix + tc.job + "/123"
			if got := familyFor(url, tc.job); got != tc.want {
				t.Fatalf("familyFor(%q, %q) = %q, want %q", url, tc.job, got, tc.want)
			}
		})
	}
}

func TestProwOutcomeFor(t *testing.T) {
	info, err := snapshot.ParseProwURL(artifactTestURL)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 25, 9, 0, 0, 0, time.FixedZone("offset", 3600))
	finished := started.Add(time.Hour)
	for _, result := range []string{"SUCCESS", "FAILURE", "ABORTED", "ERROR"} {
		t.Run(result, func(t *testing.T) {
			detail := runDetail{StartedAt: started, FinishedAt: started, SvcCluster: "svc", MgmtCluster: "mgmt", ADOBuildID: "00123"}
			got := outcomeForProw(info, prowCompletion{Result: result, FinishedAt: finished}, detail)
			want := ciJobOutcome{
				BuildID: "123", JobName: info.JobName, ProwURL: artifactTestURL, Family: "periodic",
				OverallResult: result, Failed: result != "SUCCESS", StartedAt: started.UTC(), FinishedAt: finished.UTC(),
				SvcCluster: "svc", MgmtCluster: "mgmt", ADOBuildID: "00123",
			}
			if got != want {
				t.Fatalf("outcome = %+v, want %+v", got, want)
			}
		})
	}
	got := outcomeForProw(info, prowCompletion{Result: "SUCCESS", FinishedAt: finished}, runDetail{})
	if !got.StartedAt.IsZero() || got.SvcCluster != "" || got.MgmtCluster != "" || got.ADOBuildID != "" {
		t.Fatalf("absent enrichment must remain zero: %+v", got)
	}
}

func TestProwOutcomeJSON(t *testing.T) {
	for _, adoBuildID := range []string{"181589814", ""} {
		outcome := ciJobOutcome{BuildID: "2100631679885381632", ADOBuildID: adoBuildID}
		payload, err := json.Marshal(outcome)
		if err != nil {
			t.Fatal(err)
		}
		var row map[string]any
		if err := json.Unmarshal(payload, &row); err != nil {
			t.Fatal(err)
		}
		if row["buildId"] != outcome.BuildID || row["adoBuildId"] != adoBuildID {
			t.Fatalf("unexpected build IDs in encoded outcome: %v", row)
		}
		for _, obsolete := range []string{"testFailures", "sippyRelease"} {
			if _, found := row[obsolete]; found {
				t.Fatalf("obsolete field %q in encoded outcome: %v", obsolete, row)
			}
		}
	}
}

func TestTestIDForIsStableAndDistinct(t *testing.T) {
	const name = "installs a cluster"
	first := testIDFor(name)
	if first != testIDFor(name) || first != fmt.Sprintf("%x", sha256.Sum256([]byte(name))) {
		t.Error("the same complete name must always yield its full SHA256 digest")
	}
	if first == testIDFor(name+" ") {
		t.Error("names differing only in trailing space must not collide")
	}
}
