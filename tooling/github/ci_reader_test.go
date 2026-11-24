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
	"fmt"
	"net/http"
	"strings"
	"testing"

	gh "github.com/google/go-github/v69/github"
)

func TestCIReaderKeepsMetadataCredential(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/owner/repo/pulls/7" {
			if r.Header.Get("Authorization") != "Bearer metadata-token" {
				http.Error(w, `{"message":"metadata credential required"}`, http.StatusForbidden)
				return
			}
			respond(t, w, testPull())
			return
		}
		if r.Header.Get("Authorization") != "Bearer ci-token" {
			http.Error(w, `{"message":"CI credential required"}`, http.StatusForbidden)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"id":1,"name":"verify","head_sha":%q,"status":"completed","conclusion":"success"}]}`, testHead)
		default:
			http.NotFound(w, r)
		}
	})
	client.api = client.api.WithAuthToken("metadata-token")
	client.ci = client.api
	ciReader := gh.NewClient(nil).WithAuthToken("ci-token")
	ciReader.BaseURL = client.api.BaseURL
	separate, err := client.WithCIReader(ciReader)
	if err != nil {
		t.Fatal(err)
	}
	pull, err := separate.PullRequest(context.Background(), 7)
	if err != nil || pull.Author != "robot" {
		t.Fatalf("metadata read lost its credential: pull=%+v err=%v", pull, err)
	}
	ci, err := separate.CI(context.Background(), testHead)
	if err != nil || len(ci.Checks) != 1 || ci.Checks[0].Conclusion != "success" {
		t.Fatalf("CI evidence did not use its read credential: ci=%+v err=%v", ci, err)
	}
	if _, err := client.CI(context.Background(), testHead); err == nil {
		t.Fatal("configuring the clone silently changed the original client's credential")
	}
}
