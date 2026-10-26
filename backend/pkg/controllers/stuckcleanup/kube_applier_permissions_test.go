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

package stuckcleanup

import (
	"os"
	"path/filepath"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/yaml"
)

const clusterRolePath = "../../../../kube-applier/deploy/templates/clusterrole.yaml"

// TestKubeApplierDeletableResourcesMatchesClusterRole keeps the mirror honest.
//
// kubeApplierDeletableResources is a hand-maintained copy of the kube-applier's
// ClusterRole, used to annotate findings with whether a remaining resource could
// even be deleted today. A copy drifts, and a drifted copy produces a report
// that quietly misstates what the mutation release would be able to do — so the
// real manifest is the source of truth and this test is the enforcement.
func TestKubeApplierDeletableResourcesMatchesClusterRole(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(clusterRolePath))
	if err != nil {
		t.Fatalf("failed to read the kube-applier ClusterRole at %s: %v", clusterRolePath, err)
	}

	clusterRole := &rbacv1.ClusterRole{}
	if err := yaml.Unmarshal(raw, clusterRole); err != nil {
		t.Fatalf("failed to parse the kube-applier ClusterRole: %v", err)
	}

	actual := map[string]map[string]bool{}
	for _, rule := range clusterRole.Rules {
		if !hasVerb(rule.Verbs, "delete") {
			continue
		}
		for _, group := range rule.APIGroups {
			if actual[group] == nil {
				actual[group] = map[string]bool{}
			}
			for _, resource := range rule.Resources {
				actual[group][resource] = true
			}
		}
	}

	for group, resources := range actual {
		for resource := range resources {
			if !kubeApplierCanDelete(group, resource) {
				t.Errorf("ClusterRole grants delete on %q/%q but kubeApplierDeletableResources does not; findings will understate what can be retired",
					group, resource)
			}
		}
	}

	for group, resources := range kubeApplierDeletableResources {
		for resource := range resources {
			if !actual[group][resource] {
				t.Errorf("kubeApplierDeletableResources claims delete on %q/%q but the ClusterRole does not grant it; findings will overstate what can be retired",
					group, resource)
			}
		}
	}
}

// The two cases the design calls out explicitly: PDBs are a known root cause of
// wedged namespaces and CAPZ objects are likely finalizer holders, and neither
// is currently deletable. If either becomes permitted, the mutation release's
// scope changes, so pin them.
func TestKnownBlockersAreNotYetDeletable(t *testing.T) {
	for _, tt := range []struct{ group, resource string }{
		{"policy", "poddisruptionbudgets"},
		{"cluster.x-k8s.io", "machines"},
		{"infrastructure.cluster.x-k8s.io", "azuremachines"},
		{"apps", "deployments"},
		{"", "pods"},
	} {
		if kubeApplierCanDelete(tt.group, tt.resource) {
			t.Errorf("kubeApplierCanDelete(%q, %q) = true; if the ClusterRole really was expanded, update the mutation-release scope too",
				tt.group, tt.resource)
		}
	}
}

func TestKubeApplierCanDeleteHonorsWildcard(t *testing.T) {
	if !kubeApplierCanDelete("hypershift.openshift.io", "hostedclusters") {
		t.Error("expected the hypershift.openshift.io wildcard rule to permit any resource")
	}
	if !kubeApplierCanDelete("", "namespaces") {
		t.Error("expected core/namespaces to be deletable")
	}
	if kubeApplierCanDelete("", "services") {
		t.Error("core/services is granted get/list/watch only and must not report as deletable")
	}
}

func hasVerb(verbs []string, want string) bool {
	for _, verb := range verbs {
		if verb == want || verb == "*" {
			return true
		}
	}
	return false
}
