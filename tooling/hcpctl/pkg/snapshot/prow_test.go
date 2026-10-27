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
	"reflect"
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
				GCSBucket: "test-platform-results",
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
				GCSBucket: "test-platform-results",
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
				GCSBucket: "test-platform-results",
			},
			wantIsPR: true,
		},
		{
			name:   "rehearsal PR job",
			rawURL: "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/82457/rehearse-82457-pull-ci-openshift-hypershift-main-e2e-aro-hcp/2081917423216234496",
			wantInfo: &ProwJobInfo{
				URL:       "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/82457/rehearse-82457-pull-ci-openshift-hypershift-main-e2e-aro-hcp/2081917423216234496",
				JobName:   "rehearse-82457-pull-ci-openshift-hypershift-main-e2e-aro-hcp",
				ProwID:    "2081917423216234496",
				GCSPrefix: "pr-logs/pull/openshift_release/82457/rehearse-82457-pull-ci-openshift-hypershift-main-e2e-aro-hcp/2081917423216234496",
				GCSBucket: "test-platform-results",
			},
			wantIsPR: true,
		},
		{
			name:   "public bucket URL",
			rawURL: "https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
			wantInfo: &ProwJobInfo{
				URL:       "https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
				JobName:   "periodic-ci-Azure-ARO-HCP-main-e2e-parallel",
				ProwID:    "1234567890",
				GCSPrefix: "logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/1234567890",
				GCSBucket: "test-platform-results-public",
			},
			wantIsPR: false,
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
			if got.GCSBucket != tt.wantInfo.GCSBucket {
				t.Errorf("GCSBucket = %q, want %q", got.GCSBucket, tt.wantInfo.GCSBucket)
			}
			if got.IsPullRequest() != tt.wantIsPR {
				t.Errorf("IsPullRequest() = %v, want %v", got.IsPullRequest(), tt.wantIsPR)
			}
		})
	}
}

func TestManagementClusterNames(t *testing.T) {
	for _, tt := range []struct {
		testName string
		input    string
		count    int
		want     []string
	}{
		{
			testName: "multiple stamps expanded",
			input:    "ci01-j123-mgmt-1",
			count:    2,
			want:     []string{"ci01-j123-mgmt-1", "ci01-j123-mgmt-2"},
		},
		{
			testName: "single stamp unchanged",
			input:    "dev-j123-mgmt-1",
			count:    1,
			want:     []string{"dev-j123-mgmt-1"},
		},
		{
			testName: "count not set",
			input:    "dev-j123-mgmt-1",
			count:    0,
			want:     []string{"dev-j123-mgmt-1"},
		},
		{
			testName: "configured stamp is not first",
			input:    "ci01-j123-mgmt-2",
			count:    3,
			want:     []string{"ci01-j123-mgmt-1", "ci01-j123-mgmt-2", "ci01-j123-mgmt-3"},
		},
		{
			testName: "no stamp index suffix",
			input:    "ci01-j123-mgmt",
			count:    2,
			want:     []string{"ci01-j123-mgmt"},
		},
		{
			testName: "non-numeric suffix",
			input:    "ci01-j123-mgmt-x",
			count:    2,
			want:     []string{"ci01-j123-mgmt-x"},
		},
		{
			testName: "empty name",
			input:    "",
			count:    2,
			want:     nil,
		},
	} {
		t.Run(tt.testName, func(t *testing.T) {
			got := managementClusterNames(tt.input, tt.count)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("managementClusterNames(%q, %d) = %v, want %v", tt.input, tt.count, got, tt.want)
			}
		})
	}
}

func TestParseConfigManagementClusterNames(t *testing.T) {
	cfg := []byte(`
kusto:
  kustoName: hcp-dev-us-2
  location: eastus2
svc:
  aks:
    name: ci01-j123-svc
mgmt:
  aks:
    name: ci01-j123-mgmt-1
  stamps:
    count: 2
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if parsed.ManagementClusterName != "ci01-j123-mgmt-1" {
		t.Errorf("ManagementClusterName = %q, want ci01-j123-mgmt-1", parsed.ManagementClusterName)
	}
	want := []string{"ci01-j123-mgmt-1", "ci01-j123-mgmt-2"}
	if !reflect.DeepEqual(parsed.ManagementClusterNames, want) {
		t.Errorf("ManagementClusterNames = %v, want %v", parsed.ManagementClusterNames, want)
	}
}

func TestClearClusterFilters(t *testing.T) {
	// Regression test: fetchNonPRJobConfig disables cluster filtering via
	// clearClusterFilters. A leftover ManagementClusterNames slice would
	// produce a management-only filter that drops service-cluster records.
	cfg := &ProwJobConfig{
		ServiceClusterName:     "ci01-j123-svc",
		ManagementClusterName:  "ci01-j123-mgmt-1",
		ManagementClusterNames: []string{"ci01-j123-mgmt-1", "ci01-j123-mgmt-2"},
	}
	cfg.clearClusterFilters()

	if cfg.ServiceClusterName != "" {
		t.Errorf("ServiceClusterName = %q, want empty", cfg.ServiceClusterName)
	}
	if cfg.ManagementClusterName != "" {
		t.Errorf("ManagementClusterName = %q, want empty", cfg.ManagementClusterName)
	}
	if cfg.ManagementClusterNames != nil {
		t.Errorf("ManagementClusterNames = %v, want nil", cfg.ManagementClusterNames)
	}

	data := queryData{
		ServiceClusterName:     cfg.ServiceClusterName,
		ManagementClusterNames: cfg.ManagementClusterNames,
	}
	if got := data.ClusterFilterValues(); got != "" {
		t.Errorf("ClusterFilterValues() after clearClusterFilters = %q, want empty (no cluster filter)", got)
	}
}

func TestClusterFilterValues(t *testing.T) {
	for _, tt := range []struct {
		testName string
		data     queryData
		want     string
	}{
		{
			testName: "service plus all mgmt stamps",
			data: queryData{
				ServiceClusterName:     "ci01-j123-svc",
				ManagementClusterNames: []string{"ci01-j123-mgmt-1", "ci01-j123-mgmt-2"},
			},
			want: "'ci01-j123-svc', 'ci01-j123-mgmt-1', 'ci01-j123-mgmt-2'",
		},
		{
			testName: "mgmt names only still produces a filter",
			data: queryData{
				ManagementClusterNames: []string{"ci01-j123-mgmt-1"},
			},
			want: "'ci01-j123-mgmt-1'",
		},
		{
			testName: "no names produces no filter",
			data:     queryData{},
			want:     "",
		},
	} {
		t.Run(tt.testName, func(t *testing.T) {
			if got := tt.data.ClusterFilterValues(); got != tt.want {
				t.Errorf("ClusterFilterValues() = %q, want %q", got, tt.want)
			}
		})
	}
}
