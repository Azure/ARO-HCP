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

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

func testStatus(id int64) *gh.RepoStatus {
	return &gh.RepoStatus{ID: &id, Context: gh.Ptr("verify"), State: gh.Ptr("success"), CreatedAt: &gh.Timestamp{}}
}

func testCheck(id int64) *gh.CheckRun {
	return &gh.CheckRun{ID: &id, Name: gh.Ptr("verify"), HeadSHA: gh.Ptr(testHead), Status: gh.Ptr("completed"), Conclusion: gh.Ptr("success")}
}

func TestCICollectsOnlyPaginatedHeadBoundCI(t *testing.T) {
	statusPages, checkPages := 0, 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if r.URL.Query().Get("per_page") != "100" || (page != "1" && page != "2") {
			t.Errorf("unexpected pagination %s", r.URL)
		}
		id := int64(1)
		if page == "1" {
			w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path))
		} else {
			id = 2
		}
		switch r.URL.Path {
		case "/repos/owner/repo/commits/" + testHead + "/statuses":
			statusPages++
			respond(t, w, []*gh.RepoStatus{testStatus(id)})
		case "/repos/owner/repo/commits/" + testHead + "/check-runs":
			checkPages++
			if r.URL.Query().Get("filter") != "latest" {
				t.Errorf("CI did not request latest check runs: %s", r.URL)
			}
			respond(t, w, &gh.ListCheckRunsResults{Total: gh.Ptr(2), CheckRuns: []*gh.CheckRun{testCheck(id)}})
		default:
			t.Errorf("CI fetched non-CI evidence: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ci, err := client.CI(context.Background(), testHead)
	if err != nil || len(ci.Statuses) != 2 || len(ci.Checks) != 2 || statusPages != 2 || checkPages != 2 {
		t.Fatalf("ci=%+v err=%v statuses=%d checks=%d", ci, err, statusPages, checkPages)
	}
}

func TestCIRejectsInvalidSHAWithoutRequests(t *testing.T) {
	requests := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	})
	for _, head := range []string{"", "aaaaaaa", strings.Repeat("a", 39), strings.Repeat("a", 41), strings.Repeat("g", 40)} {
		if _, err := client.CI(context.Background(), head); err == nil {
			t.Errorf("accepted invalid head %q", head)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid SHAs made %d requests", requests)
	}
}

func TestCIRejectsIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"null statuses", "null status", "duplicate status", "status context", "status timestamp", "null checks", "null check", "missing total", "negative total", "count mismatch", "changed total", "duplicate check", "foreign head", "missing check name", "missing check status", "skipped status page", "repeated check page"} {
		t.Run(mode, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/statuses") {
					status := testStatus(1)
					items := []*gh.RepoStatus{}
					switch mode {
					case "null statuses":
						items = nil
					case "null status":
						items = append(items, nil)
					case "duplicate status":
						items = append(items, status)
						if r.URL.Query().Get("page") == "1" {
							w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path))
						}
					case "status context":
						status.Context = nil
						items = append(items, status)
					case "status timestamp":
						status.CreatedAt = nil
						items = append(items, status)
					case "skipped status page":
						w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=3>; rel=\"next\"", r.Host, r.URL.Path))
					}
					respond(t, w, items)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/check-runs") {
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				check := testCheck(1)
				result := &gh.ListCheckRunsResults{Total: gh.Ptr(1), CheckRuns: []*gh.CheckRun{check}}
				switch mode {
				case "null checks":
					result.CheckRuns = nil
				case "null check":
					result.CheckRuns[0] = nil
				case "missing total":
					result.Total = nil
				case "negative total":
					result.Total = gh.Ptr(-1)
				case "count mismatch":
					result.Total = gh.Ptr(2)
				case "changed total", "duplicate check":
					result.Total = gh.Ptr(2)
					if r.URL.Query().Get("page") == "1" {
						w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=2>; rel=\"next\"", r.Host, r.URL.Path))
					} else if mode == "changed total" {
						result.Total = gh.Ptr(3)
					}
				case "foreign head":
					check.HeadSHA = gh.Ptr(testBase)
				case "missing check name":
					check.Name = nil
				case "missing check status":
					check.Status = nil
				case "repeated check page":
					w.Header().Set("Link", fmt.Sprintf("<http://%s%s?page=1>; rel=\"next\"", r.Host, r.URL.Path))
				}
				respond(t, w, result)
			})
			if _, err := client.CI(context.Background(), testHead); !errors.Is(err, ErrEvidenceIncomplete) {
				t.Fatalf("got %v, want incomplete evidence", err)
			}
		})
	}
}
