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

package ocadminspect

import "testing"

func TestResourcePlural(t *testing.T) {
	tests := map[string]string{
		"Pod":           "pods",
		"ConfigMap":     "configmaps",
		"Service":       "services",
		"Endpoints":     "endpoints",
		"Ingress":       "ingresses",
		"NetworkPolicy": "networkpolicies",
		"Deployment":    "deployments",
		"HostedCluster": "hostedclusters",
		"EgressQoS":     "egressqoses",
		"Namespace":     "namespaces",
		"NodePool":      "nodepools",
		"MachineSet":    "machinesets",
	}
	for kind, want := range tests {
		if got := ResourcePlural(kind); got != want {
			t.Errorf("ResourcePlural(%q) = %q, want %q", kind, got, want)
		}
	}
}

func crdResource(group, kind, plural, singular string, shortNames []string, namespaced bool) Resource {
	names := map[string]any{
		"kind":     kind,
		"plural":   plural,
		"singular": singular,
	}
	if len(shortNames) > 0 {
		sn := make([]any, len(shortNames))
		for i, s := range shortNames {
			sn[i] = s
		}
		names["shortNames"] = sn
	}
	scope := "Cluster"
	if namespaced {
		scope = "Namespaced"
	}
	return Resource{
		APIVersion: "apiextensions.k8s.io/v1",
		Kind:       "CustomResourceDefinition",
		Name:       plural + "." + group,
		Object: map[string]any{
			"spec": map[string]any{
				"group": group,
				"names": names,
				"scope": scope,
			},
		},
	}
}

func TestCRDNameResolver_ResolvesFromCapturedCRD(t *testing.T) {
	resources := []Resource{
		crdResource("hypershift.openshift.io", "NodePool", "nodepools", "nodepool", []string{"np"}, true),
		{Kind: "NodePool", Name: "worker-1"}, // a non-CRD resource, should be ignored
	}
	resolver := NewCRDNameResolver(resources)

	names, ok := resolver.Resolve("hypershift.openshift.io", "NodePool")
	if !ok {
		t.Fatalf("Resolve() ok = false, want true")
	}
	if names.Plural != "nodepools" || names.Singular != "nodepool" || !names.Namespaced {
		t.Errorf("Resolve() = %+v, want plural=nodepools singular=nodepool namespaced=true", names)
	}
	if len(names.ShortNames) != 1 || names.ShortNames[0] != "np" {
		t.Errorf("Resolve().ShortNames = %v, want [np]", names.ShortNames)
	}

	if got := resolver.Plural("hypershift.openshift.io", "NodePool"); got != "nodepools" {
		t.Errorf("Plural() = %q, want %q (from captured CRD, not the heuristic)", got, "nodepools")
	}
}

func TestCRDNameResolver_FallsBackToHeuristicWhenNoCRDCaptured(t *testing.T) {
	resolver := NewCRDNameResolver(nil)

	if _, ok := resolver.Resolve("hypershift.openshift.io", "HostedCluster"); ok {
		t.Fatalf("Resolve() ok = true with no captured CRDs, want false")
	}
	if got := resolver.Plural("hypershift.openshift.io", "HostedCluster"); got != "hostedclusters" {
		t.Errorf("Plural() = %q, want heuristic result %q", got, "hostedclusters")
	}
}

func TestCRDNameResolver_NilResolverFallsBackSafely(t *testing.T) {
	var resolver *CRDNameResolver
	if got := resolver.Plural("cluster.x-k8s.io", "MachineSet"); got != "machinesets" {
		t.Errorf("Plural() on nil resolver = %q, want heuristic result %q", got, "machinesets")
	}
}

func TestCRDNameResolver_SkipsMalformedCRD(t *testing.T) {
	resources := []Resource{
		{Kind: "CustomResourceDefinition", Name: "broken", Object: map[string]any{"spec": map[string]any{"group": "example.com"}}},
	}
	resolver := NewCRDNameResolver(resources)
	if _, ok := resolver.Resolve("example.com", ""); ok {
		t.Fatalf("Resolve() should not find an entry for a CRD missing spec.names")
	}
}
