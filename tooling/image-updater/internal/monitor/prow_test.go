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

package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Pins the behaviors the monitor gains by reusing the tenant-quota client:
// JS-prefixed payload decoding and retrying transient server errors.
func TestNewProwClientDecodesJobsAndRetriesTransientFailures(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prowjobs.js" {
			t.Errorf("request path = %q, want /prowjobs.js", r.URL.Path)
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		// prowjobs.js is served as JavaScript and may carry a non-JSON prefix.
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(")]}'\n" + `{"items":[{"spec":{"job":"periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"},"status":{"state":"success","build_id":"123"}}]}`))
	}))
	defer server.Close()

	client := NewProwClient(server.URL, 10*time.Second)
	jobs, err := client.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	if jobs[0].Spec.Job != "periodic-ci-Azure-ARO-HCP-main-image-updater-tooling" {
		t.Fatalf("job name = %q", jobs[0].Spec.Job)
	}
	if jobs[0].Status.BuildID != "123" {
		t.Fatalf("build ID = %q", jobs[0].Status.BuildID)
	}
	if requests.Load() < 2 {
		t.Fatalf("server saw %d requests, want >= 2 (first attempt retried)", requests.Load())
	}
}

func TestNewProwClientRejectsEmptyBaseURL(t *testing.T) {
	client := NewProwClient("  ", 10*time.Second)
	if _, err := client.ListJobs(context.Background()); err == nil {
		t.Fatal("expected error for empty Prow base URL")
	}
}
