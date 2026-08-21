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

package gatherobservability

import (
	"testing"
)

func TestParseCategories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		wantErr bool
		wantLen int
	}{
		{
			name: "valid config",
			content: `categories:
- name: "known-error-rate"
  policy: ignore
  reason: "Known during provisioning"
  match:
  - name: "BackendOperationErrorRate"
- name: "controller-churn"
  policy: ignore
  reason: "Controller churn known"
  match:
  - name: "BackendController.*"
`,
			wantLen: 2,
		},
		{
			name:    "empty list",
			content: "categories: []\n",
			wantLen: 0,
		},
		{
			name:    "missing name",
			content: "categories:\n- policy: ignore\n  reason: \"some reason\"\n  match:\n  - name: \"SomeAlert\"\n",
			wantErr: true,
		},
		{
			name:    "missing reason",
			content: "categories:\n- name: \"cat\"\n  policy: ignore\n  match:\n  - name: \"SomeAlert\"\n",
			wantErr: true,
		},
		{
			name:    "missing policy",
			content: "categories:\n- name: \"cat\"\n  reason: \"some reason\"\n  match:\n  - name: \"SomeAlert\"\n",
			wantErr: true,
		},
		{
			name:    "unknown policy",
			content: "categories:\n- name: \"cat\"\n  policy: \"nuke-it\"\n  reason: \"some reason\"\n  match:\n  - name: \"SomeAlert\"\n",
			wantErr: true,
		},
		{
			name:    "invalid yaml",
			content: "not: [valid: yaml",
			wantErr: true,
		},
		{
			name:    "invalid name regex",
			content: "categories:\n- name: \"cat\"\n  policy: ignore\n  reason: \"bad regex\"\n  match:\n  - name: \"[invalid\"\n",
			wantErr: true,
		},
		{
			name: "invalid label regex",
			content: `categories:
- name: "cat"
  policy: ignore
  reason: "test"
  match:
  - name: "SomeAlert"
    labels:
      name: "[invalid"
`,
			wantErr: true,
		},
		{
			name: "unknown workspace",
			content: `categories:
- name: "cat"
  policy: ignore
  reason: "test"
  match:
  - workspace: "prod"
    name: "SomeAlert"
`,
			wantErr: true,
		},
		{
			name: "fail-over-threshold without threshold",
			content: `categories:
- name: "cat"
  policy: fail-over-threshold
  reason: "test"
  match:
  - name: "SomeAlert"
`,
			wantErr: true,
		},
		{
			name: "fail-over-threshold with minFirings",
			content: `categories:
- name: "cat"
  policy: fail-over-threshold
  reason: "test"
  threshold:
    minFirings: 3
  match:
  - name: "SomeAlert"
`,
			wantLen: 1,
		},
		{
			name: "empty match not on last category",
			content: `categories:
- name: "catch-all-too-early"
  policy: fail
  reason: "test"
  match: []
- name: "never-reached"
  policy: ignore
  reason: "test"
  match:
  - name: "SomeAlert"
`,
			wantErr: true,
		},
		{
			name: "empty match on last category is a valid catch-all",
			content: `categories:
- name: "specific"
  policy: ignore
  reason: "test"
  match:
  - name: "SomeAlert"
- name: "catch-all"
  policy: fail
  reason: "test"
  match: []
`,
			wantLen: 2,
		},
		{
			name: "with labels",
			content: `categories:
- name: "delete-controller-churn"
  policy: ignore
  reason: "Known for delete controllers"
  match:
  - name: "BackendControllerRetryHotLoop"
    labels:
      name: "operation.*delete"
`,
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseCategories([]byte(tt.content))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != tt.wantLen {
				t.Errorf("got %d categories, want %d", len(result), tt.wantLen)
			}
		})
	}
}

func TestCategorizeAlerts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		categoriesYAML string
		alerts         []alert
	}{
		{
			name: "basic",
			categoriesYAML: `categories:
- name: "error-rate-known"
  policy: ignore
  reason: "error rate known"
  match:
  - name: "BackendOperationErrorRate"
- name: "controller-churn"
  policy: warn
  reason: "controller churn"
  match:
  - name: "BackendController.*"
`,
			alerts: []alert{
				{Alert: alertData{Name: "BackendOperationErrorRate"}},
				{Alert: alertData{Name: "BackendControllerRetryHotLoop"}},
				{Alert: alertData{Name: "BackendControllerQueueDepthHigh"}},
				{Alert: alertData{Name: "SomethingUnknown"}},
				{Alert: alertData{Name: "AnotherUnknown"}},
			},
		},
		{
			name: "no_categories",
			alerts: []alert{
				{Alert: alertData{Name: "SomeAlert"}},
			},
		},
		{
			name: "exact_match_only",
			categoriesYAML: `categories:
- name: "exact-backend"
  policy: ignore
  reason: "exact match only"
  match:
  - name: "Backend"
`,
			alerts: []alert{
				{Alert: alertData{Name: "Backend"}},
				{Alert: alertData{Name: "BackendOperationErrorRate"}},
			},
		},
		{
			name: "first_match_wins",
			categoriesYAML: `categories:
- name: "first"
  policy: warn
  reason: "first pattern"
  match:
  - name: "Backend.*"
- name: "second"
  policy: ignore
  reason: "second pattern"
  match:
  - name: "BackendControllerRetryHotLoop"
`,
			alerts: []alert{
				{Alert: alertData{Name: "BackendControllerRetryHotLoop"}},
			},
		},
		{
			name: "label_matching",
			categoriesYAML: `categories:
- name: "delete-controller"
  policy: ignore
  reason: "known for delete controller"
  match:
  - name: "BackendControllerRetryHotLoop"
    labels:
      name: "operationnodepooldelete"
`,
			alerts: []alert{
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationnodepooldelete", "severity": "warning"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationcreate", "severity": "warning"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: nil,
				}},
			},
		},
		{
			name: "label_regex",
			categoriesYAML: `categories:
- name: "delete-controller-churn"
  policy: ignore
  reason: "known for delete controllers"
  match:
  - name: "BackendControllerRetryHotLoop"
    labels:
      name: "operation.*delete"
`,
			alerts: []alert{
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationnodepooldelete"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationclusterdelete"},
				}},
				{Alert: alertData{
					Name:   "BackendControllerRetryHotLoop",
					Labels: map[string]string{"name": "operationcreate"},
				}},
			},
		},
		{
			name: "workspace_scoping",
			categoriesYAML: `categories:
- name: "svc-node-issue"
  policy: fail
  reason: "svc node issue"
  match:
  - workspace: svc
    name: "KubeNodeNotReady"
- name: "hcp-node-issue"
  policy: fail-over-threshold
  reason: "hcp node issue"
  threshold:
    minFirings: 2
  match:
  - workspace: hcp
    name: "KubeNodeNotReady"
`,
			alerts: []alert{
				{Alert: alertData{Name: "KubeNodeNotReady"}, Metadata: alertMetadata{MonitoringWorkspaceType: "svc"}},
				{Alert: alertData{Name: "KubeNodeNotReady"}, Metadata: alertMetadata{MonitoringWorkspaceType: "hcp"}},
				{Alert: alertData{Name: "KubeNodeNotReady"}, Metadata: alertMetadata{MonitoringWorkspaceType: "infra"}},
			},
		},
		{
			name: "catch_all",
			categoriesYAML: `categories:
- name: "specific"
  policy: ignore
  reason: "specific reason"
  match:
  - name: "SomeSpecificAlert"
- name: "catch-all"
  policy: fail
  reason: "unclassified"
  match: []
`,
			alerts: []alert{
				{Alert: alertData{Name: "SomeSpecificAlert"}},
				{Alert: alertData{Name: "SomethingElseEntirely"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var categories []category
			if tt.categoriesYAML != "" {
				categories = mustParseCategories(t, tt.categoriesYAML)
			}
			categorized := categorizeAlerts(tt.alerts, categories)
			CompareWithFixture(t, categorized)
		})
	}
}

// TestDefaultCategoriesConfig validates the actual production
// alert-categories/categories.yaml (embedded as defaultCategoriesData) --
// unlike the other tests here, which exercise the matching engine against
// small ad hoc configs. It parses successfully, has a trailing catch-all,
// and correctly discriminates the ARO-28187 blast-radius tiers on a
// representative sample of real-shaped alerts.
func TestDefaultCategoriesConfig(t *testing.T) {
	t.Parallel()

	categories, err := parseCategories(defaultCategoriesData)
	if err != nil {
		t.Fatalf("failed to parse embedded alert-categories/categories.yaml: %v", err)
	}
	if len(categories) == 0 {
		t.Fatal("expected at least one category")
	}
	last := categories[len(categories)-1]
	if last.name != "catch-all" || len(last.rules) != 0 {
		t.Fatalf("expected the last category to be an empty-match catch-all, got %+v", last)
	}

	tests := []struct {
		name       string
		alert      alert
		wantName   string
		wantTier   int
		wantPolicy string
	}{
		{
			name: "hcp_controlplane_namespace_pod",
			alert: alert{
				Alert:    alertData{Name: "KubePodNotReady", Labels: map[string]string{"namespace": "ocm-arohcpdev-abc123-primary", "pod": "kube-apiserver-0"}},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceHcp},
			},
			wantName:   "customer-visible-cluster-outage",
			wantTier:   4,
			wantPolicy: policyFail,
		},
		{
			name: "hcp_hostedcluster_namespace_pod",
			alert: alert{
				Alert:    alertData{Name: "KubeDeploymentReplicasMismatch", Labels: map[string]string{"namespace": "ocm-arohcpdev-abc123"}},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceHcp},
			},
			wantName:   "non-customer-visible-cluster-outage",
			wantTier:   5,
			wantPolicy: policyWarn,
		},
		{
			name: "svc_frontend_pod",
			alert: alert{
				Alert:    alertData{Name: "KubePodNotReady", Labels: map[string]string{"namespace": "aro-hcp", "pod": "aro-hcp-frontend-abc123"}},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceSvc},
			},
			wantName:   "customer-visible-region-outage",
			wantTier:   1,
			wantPolicy: policyFail,
		},
		{
			name: "svc_backend_pod_shares_namespace_with_frontend",
			alert: alert{
				Alert:    alertData{Name: "KubePodNotReady", Labels: map[string]string{"namespace": "aro-hcp", "pod": "aro-hcp-backend-abc123"}},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceSvc},
			},
			wantName:   "non-customer-visible-region-outage",
			wantTier:   2,
			wantPolicy: policyFail,
		},
		{
			name: "hcp_kube_applier",
			alert: alert{
				Alert:    alertData{Name: "KubeDeploymentReplicasMismatch", Labels: map[string]string{"namespace": "kube-applier"}},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceHcp},
			},
			wantName:   "management-cluster-outage",
			wantTier:   3,
			wantPolicy: policyFailOverThreshold,
		},
		{
			name: "capi_provider_pod_is_expected_noise_not_tier4",
			alert: alert{
				Alert:    alertData{Name: "KubePodNotReady", Labels: map[string]string{"namespace": "ocm-arohcpdev-abc123-primary", "pod": "capi-provider-xyz"}},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceHcp},
			},
			wantName:   "expected-noise-capi-provider-pods",
			wantTier:   0,
			wantPolicy: policyIgnore,
		},
		{
			name: "unclassified_alert_falls_to_catch_all",
			alert: alert{
				Alert:    alertData{Name: "SomeBrandNewAlertNobodyHasSeenYet"},
				Metadata: alertMetadata{MonitoringWorkspaceType: workspaceInfra},
			},
			wantName:   "catch-all",
			wantTier:   1,
			wantPolicy: policyFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := categorizeAlerts([]alert{tt.alert}, categories)[0]
			if got.Metadata.Category != tt.wantName {
				t.Errorf("Category = %q, want %q", got.Metadata.Category, tt.wantName)
			}
			if got.Metadata.CategoryTier != tt.wantTier {
				t.Errorf("CategoryTier = %d, want %d", got.Metadata.CategoryTier, tt.wantTier)
			}
			if got.Metadata.CategoryPolicy != tt.wantPolicy {
				t.Errorf("CategoryPolicy = %q, want %q", got.Metadata.CategoryPolicy, tt.wantPolicy)
			}
		})
	}
}
