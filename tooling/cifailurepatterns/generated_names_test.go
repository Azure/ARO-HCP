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

package cifailurepatterns

import (
	"strings"
	"testing"
)

func TestGeneratedNameFallbackMergeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		merge bool
	}{
		{"delayed-rbac-87tl668hqnnr", "delayed-rbac-7fppc52tbxcc", true},
		{"router-5dbrz", "router-khxf4", true},
		{"cilium-cluster-cilium-np-5dbrz-khxf4", "cilium-cluster-cilium-np-9d89g-q7czc", true},
		// Letter-only suffixes remain ambiguous without a contextual name rule.
		{"router-jglhd", "router-khxf4", false},
		{"2pmeojr923nt08rchn2mn56al24muh61", "2pni671e890elvabbe631mnqcb4pi6te", true},
		{"router-5dbrz", "capi-provider-khxf4", false},
		{"4.20.8", "4.21.8", false},
		{"v20260901preview", "v20261001preview", false},
		{"release-4.20.8-87tl668hqnnr", "release-4.21.8-7fppc52tbxcc", false},
		{"Standard_D4s_v5", "Standard_D8s_v5", false},
		{"eastus2", "westus3", false},
		{"capi-provider", "router", false},
		{"401", "403", false},
		{"aro-hcp.experimental.cluster.max-update-duration", "aro-hcp.experimental.nodepool.max-update-duration", false},
	} {
		t.Run(tc.a+"_"+tc.b, func(t *testing.T) {
			a := Extract(Input{Text: "failed reconciling widget " + tc.a})[0]
			b := Extract(Input{Text: "failed reconciling widget " + tc.b})[0]
			if equal := a.Key == b.Key; equal != tc.merge {
				t.Fatalf("merge=%v want %v: %q / %q", equal, tc.merge, a.Canonical, b.Canonical)
			}
			if tc.merge && !strings.Contains(a.Canonical, "<id>") {
				t.Fatalf("identity survived: %q", a.Canonical)
			}
		})
	}
}

func TestGeneratedNameFallbackPreservesAmbiguousTokens(t *testing.T) {
	for _, s := range []string{"router", "oauth2", "sha256", "worker-12345", "worker-abcdef", "worker-abc12", "node-ipv6", "DeploymentFailed", "Microsoft.RedHatOpenShift", "<resource-group>", "<id>", "4.20.0-0.nightly-multi-2026-09-30-014938"} {
		if got := normalizeGeneratedNames(s); got != s {
			t.Errorf("ambiguous token changed: %q => %q", s, got)
		}
	}
}

func TestGeneratedNameFallbackPathsAndIdempotence(t *testing.T) {
	raw := "failed reconciling widget delayed-rbac-87tl668hqnnr/router-5dbrz: unavailable"
	got := normalizeGeneratedNames(raw)
	want := "failed reconciling widget delayed-rbac-<id>/router-<id>: unavailable"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if twice := normalizeGeneratedNames(got); twice != got {
		t.Fatalf("normalization not idempotent: %q", twice)
	}
}
