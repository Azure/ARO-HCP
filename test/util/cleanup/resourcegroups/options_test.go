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

package resourcegroups

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultOptionsUseBoundedConcurrency(t *testing.T) {
	t.Parallel()

	if got := DefaultOptions().Concurrency; got != DefaultConcurrency {
		t.Fatalf("expected default concurrency %d, got %d", DefaultConcurrency, got)
	}
}

func TestValidateRejectsInvalidConcurrency(t *testing.T) {
	t.Parallel()

	options := DefaultOptions()
	options.DeleteExpired = true
	options.Concurrency = 0

	if _, err := options.Validate(); err == nil {
		t.Fatal("expected zero concurrency to be rejected")
	}
}

func TestJobIDOptions(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		jobID     string
		set       bool
		tracked   bool
		expired   bool
		groups    []string
		wantError string
	}{
		{name: "job only", jobID: "123"},
		{name: "tracked and job", jobID: "123", tracked: true},
		{name: "explicit empty", set: true, wantError: "--job-id must not be blank"},
		{name: "whitespace", jobID: " \t\n", wantError: "--job-id must not be blank"},
		{name: "no selector", wantError: "is required"},
		{name: "expired and job", jobID: "123", expired: true, wantError: "mutually exclusive"},
		{name: "explicit and job", jobID: "123", groups: []string{"group"}, wantError: "mutually exclusive"},
		{name: "expired and tracked", expired: true, tracked: true, wantError: "mutually exclusive"},
		{name: "explicit and tracked", groups: []string{"group"}, tracked: true, wantError: "mutually exclusive"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			options := DefaultOptions()
			options.JobID, options.JobIDSet = testCase.jobID, testCase.set
			options.Tracked, options.DeleteExpired, options.ResourceGroups = testCase.tracked, testCase.expired, testCase.groups
			_, err := options.Validate()
			if testCase.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("expected %q, got %v", testCase.wantError, err)
			}
		})
	}
}

func TestCompleteTrackedJobID(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		jobID            string
		marker           bool
		missingDirectory bool
		wantError        string
	}{
		{name: "empty markers with job", jobID: "123"},
		{name: "empty markers without job", wantError: "no tracked-resource-group_"},
		{name: "read failure with job", jobID: "123", missingDirectory: true, wantError: "reading tracked path"},
		{name: "tracked marker with job", jobID: "123", marker: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			options := DefaultOptions()
			options.Tracked, options.JobID, options.SharedDir = true, testCase.jobID, t.TempDir()
			if testCase.missingDirectory {
				options.SharedDir = filepath.Join(options.SharedDir, "missing")
			}
			if testCase.marker {
				if err := os.WriteFile(filepath.Join(options.SharedDir, "tracked-resource-group_Group"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			validated, err := options.Validate()
			if err != nil {
				t.Fatal(err)
			}
			completed, err := validated.Complete()
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("expected %q, got %v", testCase.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if completed.JobID != testCase.jobID {
				t.Fatalf("job ID changed: %q", completed.JobID)
			}
			if testCase.marker && (len(completed.ResourceGroups) != 1 || completed.ResourceGroups[0] != "Group") {
				t.Fatalf("unexpected groups: %v", completed.ResourceGroups)
			}
		})
	}
}
