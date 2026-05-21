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

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestMatchesGroupSuffix(t *testing.T) {
	tests := []struct {
		group string
		want  bool
	}{
		{"hypershift.openshift.io", true},
		{"cluster.x-k8s.io", true},
		{"infrastructure.cluster.x-k8s.io", true},
		{"work.open-cluster-management.io", true},
		{"open-cluster-management.io", true},
		{"agent-install.openshift.io", true},
		{"capi-provider.agent-install.openshift.io", true},
		{"multicluster.openshift.io", true},
		{"multitenancy.acn.azure.com", true},
		{"", false},
		{"apps", false},
		{"openshift.io", false},
		{"fake-open-cluster-management.io", false},
		{"notcluster.x-k8s.io", false},
		{"x-k8s.io", false},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			if got := matchesGroupSuffix(tt.group); got != tt.want {
				t.Errorf("matchesGroupSuffix(%q) = %v, want %v", tt.group, got, tt.want)
			}
		})
	}
}

func TestSupportsListWatch(t *testing.T) {
	tests := []struct {
		name  string
		verbs metav1.Verbs
		want  bool
	}{
		{"list and watch", metav1.Verbs{"get", "list", "watch"}, true},
		{"only list and watch", metav1.Verbs{"list", "watch"}, true},
		{"only list", metav1.Verbs{"list"}, false},
		{"only watch", metav1.Verbs{"watch"}, false},
		{"neither", metav1.Verbs{"get", "create"}, false},
		{"empty", metav1.Verbs{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := metav1.APIResource{Verbs: tt.verbs}
			if got := supportsListWatch(r); got != tt.want {
				t.Errorf("supportsListWatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiscoverGVRs(t *testing.T) {
	w := &ResourceWatcher{
		discoveryClient: &fakeDiscovery{
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "hypershift.openshift.io/v1beta1",
					APIResources: []metav1.APIResource{
						{Name: "hostedclusters", Verbs: metav1.Verbs{"get", "list", "watch"}},
						{Name: "hostedclusters/status", Verbs: metav1.Verbs{"get", "list", "watch"}},
						{Name: "nodepools", Verbs: metav1.Verbs{"get", "list", "watch"}},
					},
				},
				{
					GroupVersion: "work.open-cluster-management.io/v1",
					APIResources: []metav1.APIResource{
						{Name: "manifestworks", Verbs: metav1.Verbs{"get", "list", "watch"}},
						{Name: "appliedmanifestworks", Verbs: metav1.Verbs{"get", "list"}}, // no watch
					},
				},
				{
					GroupVersion: "apps/v1",
					APIResources: []metav1.APIResource{
						{Name: "deployments", Verbs: metav1.Verbs{"get", "list", "watch"}},
					},
				},
				{
					GroupVersion: "multitenancy.acn.azure.com/v1alpha1",
					APIResources: []metav1.APIResource{
						{Name: "podnetworkconfigs", Verbs: metav1.Verbs{"list", "watch"}},
					},
				},
			},
		},
	}

	gvrs, err := w.discoverGVRs()
	if err != nil {
		t.Fatalf("discoverGVRs() error = %v", err)
	}

	expected := []schema.GroupVersionResource{
		{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"},
		{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "nodepools"},
		{Group: "work.open-cluster-management.io", Version: "v1", Resource: "manifestworks"},
		{Group: "multitenancy.acn.azure.com", Version: "v1alpha1", Resource: "podnetworkconfigs"},
	}

	if len(gvrs) != len(expected) {
		t.Fatalf("got %d GVRs, want %d: %v", len(gvrs), len(expected), gvrs)
	}

	for i, gvr := range gvrs {
		if gvr != expected[i] {
			t.Errorf("gvrs[%d] = %v, want %v", i, gvr, expected[i])
		}
	}
}

// fakeDiscovery implements ServerResourceDiscoverer for testing.
type fakeDiscovery struct {
	resources []*metav1.APIResourceList
}

func (f *fakeDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, f.resources, nil
}
