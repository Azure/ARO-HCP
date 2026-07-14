// Copyright 2025 Microsoft Corporation
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

package snapshot

import (
	"testing"
)

func TestParseProwURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		wantInfo *ProwJobInfo
		wantErr  bool
		wantIsPR bool
	}{
		{
			name:   "periodic job",
			rawURL: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
			wantInfo: &ProwJobInfo{
				URL:       "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
				JobName:   "periodic-ci-Azure-ARO-HCP-main-e2e-parallel",
				ProwID:    "1234567890",
				GCSPrefix: "logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
			},
			wantIsPR: false,
		},
		{
			name:   "PR job",
			rawURL: "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/9999/pull-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
			wantInfo: &ProwJobInfo{
				URL:       "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/9999/pull-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
				JobName:   "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
				ProwID:    "1234567890",
				GCSPrefix: "pr-logs/pull/Azure_ARO-HCP/9999/pull-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
			},
			wantIsPR: true,
		},
		{
			name:   "batch job",
			rawURL: "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2074470816258461696",
			wantInfo: &ProwJobInfo{
				URL:       "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2074470816258461696",
				JobName:   "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
				ProwID:    "2074470816258461696",
				GCSPrefix: "pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2074470816258461696",
			},
			wantIsPR: true,
		},
		{
			name:    "no logs segment",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/something-else",
			wantErr: true,
		},
		{
			name:    "pr-logs missing pull",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/notpull/foo/bar/baz/123",
			wantErr: true,
		},
		{
			name:    "pr-logs too few segments",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull",
			wantErr: true,
		},
		{
			name:    "pr-logs PR path too short",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/9999/pull-ci-job",
			wantErr: true,
		},
		{
			name:    "pr-logs batch path too short",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/batch/pull-ci-job",
			wantErr: true,
		},
		{
			name:    "logs path too short",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/some-job",
			wantErr: true,
		},
		{
			name:    "pr-logs invalid prow ID",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/9999/pull-ci-job/not-a-number",
			wantErr: true,
		},
		{
			name:    "batch invalid prow ID",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/batch/pull-ci-job/not-a-number",
			wantErr: true,
		},
		{
			name:    "logs invalid prow ID",
			rawURL:  "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/some-job/not-a-number",
			wantErr: true,
		},
		{
			name:    "invalid URL",
			rawURL:  "://not-a-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProwURL(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseProwURL(%q) = %+v, want error", tt.rawURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProwURL(%q) returned unexpected error: %v", tt.rawURL, err)
			}
			if got.URL != tt.wantInfo.URL {
				t.Errorf("URL = %q, want %q", got.URL, tt.wantInfo.URL)
			}
			if got.JobName != tt.wantInfo.JobName {
				t.Errorf("JobName = %q, want %q", got.JobName, tt.wantInfo.JobName)
			}
			if got.ProwID != tt.wantInfo.ProwID {
				t.Errorf("ProwID = %q, want %q", got.ProwID, tt.wantInfo.ProwID)
			}
			if got.GCSPrefix != tt.wantInfo.GCSPrefix {
				t.Errorf("GCSPrefix = %q, want %q", got.GCSPrefix, tt.wantInfo.GCSPrefix)
			}
			if got.IsPullRequest() != tt.wantIsPR {
				t.Errorf("IsPullRequest() = %v, want %v", got.IsPullRequest(), tt.wantIsPR)
			}
		})
	}
}
