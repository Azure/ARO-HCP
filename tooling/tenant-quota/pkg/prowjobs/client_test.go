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

package prowjobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClientListJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prowjobs.js" {
			t.Fatalf("path = %q, want /prowjobs.js", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(`var allBuilds = {
  "items": [{
    "metadata": {"name": "prowjob-id"},
    "spec": {
      "type": "presubmit",
      "job": "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
      "refs": {"org": "Azure", "repo": "ARO-HCP"}
    },
    "status": {
      "state": "success",
      "startTime": "2026-08-03T10:00:00Z",
      "completionTime": "2026-08-03T11:00:00Z",
      "url": "gs://test-platform-results/example",
      "build_id": "123"
    }
  }]
};`))
	}))
	t.Cleanup(server.Close)

	client := NewHTTPClient(server.URL, time.Second)
	jobs, err := client.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}

	job := jobs[0]
	if job.Spec.Job != "pull-ci-Azure-ARO-HCP-main-e2e-parallel" {
		t.Fatalf("job name = %q", job.Spec.Job)
	}
	if job.Spec.Refs == nil || job.Spec.Refs.Org != "Azure" || job.Spec.Refs.Repo != "ARO-HCP" {
		t.Fatalf("refs = %#v", job.Spec.Refs)
	}
	if job.Status.BuildID != "123" || job.Status.State != "success" {
		t.Fatalf("status = %#v", job.Status)
	}
}

func TestHTTPClientListJobsRejectsNonSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	client := NewHTTPClient(server.URL, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := client.ListJobs(ctx); err == nil {
		t.Fatal("ListJobs() error = nil, want error")
	}
}

func TestIsTerminalState(t *testing.T) {
	for _, state := range []string{"success", "failure", "error"} {
		if !IsTerminalState(state) {
			t.Errorf("IsTerminalState(%q) = false", state)
		}
	}

	for _, state := range []string{"", "pending", "triggered", "aborted"} {
		if IsTerminalState(state) {
			t.Errorf("IsTerminalState(%q) = true", state)
		}
	}
}
