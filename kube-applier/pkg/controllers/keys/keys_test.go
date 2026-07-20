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

package keys

import (
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	testSub     = "00000000-0000-0000-0000-000000000000"
	testRG      = "rg"
	testCluster = "mycluster"
)

// fakeApplyCRUD records which parent-scope accessor ApplyDesireKey.CRUD routed to.
type fakeApplyCRUD struct {
	called string
	args   []string
}

func (f *fakeApplyCRUD) ApplyDesiresForCluster(sub, rg, cluster string) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	f.called, f.args = "cluster", []string{sub, rg, cluster}
	return nil, nil
}

func (f *fakeApplyCRUD) ApplyDesiresForNodePool(sub, rg, cluster, np string) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	f.called, f.args = "nodepool", []string{sub, rg, cluster, np}
	return nil, nil
}

func (f *fakeApplyCRUD) ApplyDesiresForCredentialRequest(sub, rg, cluster, cred string) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	f.called, f.args = "credentialRequest", []string{sub, rg, cluster, cred}
	return nil, nil
}

func (f *fakeApplyCRUD) ApplyDesiresForRevocation(sub, rg, cluster, rev string) (database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	f.called, f.args = "revocation", []string{sub, rg, cluster, rev}
	return nil, nil
}

var _ database.KubeApplierApplyDesireCRUD = (*fakeApplyCRUD)(nil)

// fakeReadCRUD records which parent-scope accessor ReadDesireKey.CRUD routed to.
type fakeReadCRUD struct {
	called string
	args   []string
}

func (f *fakeReadCRUD) ReadDesiresForCluster(sub, rg, cluster string) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	f.called, f.args = "cluster", []string{sub, rg, cluster}
	return nil, nil
}

func (f *fakeReadCRUD) ReadDesiresForNodePool(sub, rg, cluster, np string) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	f.called, f.args = "nodepool", []string{sub, rg, cluster, np}
	return nil, nil
}

func (f *fakeReadCRUD) ReadDesiresForCredentialRequest(sub, rg, cluster, cred string) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	f.called, f.args = "credentialRequest", []string{sub, rg, cluster, cred}
	return nil, nil
}

func (f *fakeReadCRUD) ReadDesiresForRevocation(sub, rg, cluster, rev string) (database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	f.called, f.args = "revocation", []string{sub, rg, cluster, rev}
	return nil, nil
}

var _ database.KubeApplierReadDesireCRUD = (*fakeReadCRUD)(nil)

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestApplyDesireKeyScopes exercises every desire parent scope through the full
// resource-ID -> key -> CRUD-routing path. The credential-request and revocation
// scopes are the regression guard: before kube-applier's keys package learned
// those parent types, ApplyDesireKeyFromResourceID errored on them, so the
// apply_desire controller silently dropped credential CSR desires and admin
// credentials never issued.
func TestApplyDesireKeyScopes(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		want       ApplyDesireKey
		wantScope  string
		wantArgs   []string
	}{
		{
			name:       "cluster-scoped",
			resourceID: kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "d1"),
			want:       ApplyDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, Name: "d1"},
			wantScope:  "cluster",
			wantArgs:   []string{testSub, testRG, testCluster},
		},
		{
			name:       "nodepool-scoped",
			resourceID: kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "np", "d2"),
			want:       ApplyDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, NodePoolName: "np", Name: "d2"},
			wantScope:  "nodepool",
			wantArgs:   []string{testSub, testRG, testCluster, "np"},
		},
		{
			name:       "credential-request-scoped",
			resourceID: kubeapplier.ToCredentialRequestScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "cred", "d3"),
			want:       ApplyDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, CredentialRequestName: "cred", Name: "d3"},
			wantScope:  "credentialRequest",
			wantArgs:   []string{testSub, testRG, testCluster, "cred"},
		},
		{
			name:       "revocation-scoped",
			resourceID: kubeapplier.ToRevocationScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "rev", "d4"),
			want:       ApplyDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, RevocationName: "rev", Name: "d4"},
			wantScope:  "revocation",
			wantArgs:   []string{testSub, testRG, testCluster, "rev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := api.Must(azcorearm.ParseResourceID(tc.resourceID))

			got, err := ApplyDesireKeyFromResourceID(id)
			if err != nil {
				t.Fatalf("ApplyDesireKeyFromResourceID(%q) returned error: %v", tc.resourceID, err)
			}
			if got != tc.want {
				t.Fatalf("parsed key mismatch:\n got=%#v\nwant=%#v", got, tc.want)
			}

			// GetResourceID must round-trip back to the same key.
			reparsed, err := ApplyDesireKeyFromResourceID(got.GetResourceID())
			if err != nil {
				t.Fatalf("round-trip ApplyDesireKeyFromResourceID returned error: %v", err)
			}
			if reparsed != tc.want {
				t.Fatalf("round-trip key mismatch:\n got=%#v\nwant=%#v", reparsed, tc.want)
			}

			// CRUD must route to the accessor for the key's parent scope.
			fake := &fakeApplyCRUD{}
			if _, err := got.CRUD(fake); err != nil {
				t.Fatalf("CRUD returned error: %v", err)
			}
			if fake.called != tc.wantScope {
				t.Fatalf("CRUD routed to %q, want %q", fake.called, tc.wantScope)
			}
			if !equalStrings(fake.args, tc.wantArgs) {
				t.Fatalf("CRUD args = %v, want %v", fake.args, tc.wantArgs)
			}
		})
	}
}

// TestReadDesireKeyScopes is the ReadDesire parallel of TestApplyDesireKeyScopes.
func TestReadDesireKeyScopes(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		want       ReadDesireKey
		wantScope  string
		wantArgs   []string
	}{
		{
			name:       "cluster-scoped",
			resourceID: kubeapplier.ToClusterScopedReadDesireResourceIDString(testSub, testRG, testCluster, "r1"),
			want:       ReadDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, Name: "r1"},
			wantScope:  "cluster",
			wantArgs:   []string{testSub, testRG, testCluster},
		},
		{
			name:       "nodepool-scoped",
			resourceID: kubeapplier.ToNodePoolScopedReadDesireResourceIDString(testSub, testRG, testCluster, "np", "r2"),
			want:       ReadDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, NodePoolName: "np", Name: "r2"},
			wantScope:  "nodepool",
			wantArgs:   []string{testSub, testRG, testCluster, "np"},
		},
		{
			name:       "credential-request-scoped",
			resourceID: kubeapplier.ToCredentialRequestScopedReadDesireResourceIDString(testSub, testRG, testCluster, "cred", "r3"),
			want:       ReadDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, CredentialRequestName: "cred", Name: "r3"},
			wantScope:  "credentialRequest",
			wantArgs:   []string{testSub, testRG, testCluster, "cred"},
		},
		{
			name:       "revocation-scoped",
			resourceID: kubeapplier.ToRevocationScopedReadDesireResourceIDString(testSub, testRG, testCluster, "rev", "r4"),
			want:       ReadDesireKey{SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, RevocationName: "rev", Name: "r4"},
			wantScope:  "revocation",
			wantArgs:   []string{testSub, testRG, testCluster, "rev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := api.Must(azcorearm.ParseResourceID(tc.resourceID))

			got, err := ReadDesireKeyFromResourceID(id)
			if err != nil {
				t.Fatalf("ReadDesireKeyFromResourceID(%q) returned error: %v", tc.resourceID, err)
			}
			if got != tc.want {
				t.Fatalf("parsed key mismatch:\n got=%#v\nwant=%#v", got, tc.want)
			}

			reparsed, err := ReadDesireKeyFromResourceID(got.GetResourceID())
			if err != nil {
				t.Fatalf("round-trip ReadDesireKeyFromResourceID returned error: %v", err)
			}
			if reparsed != tc.want {
				t.Fatalf("round-trip key mismatch:\n got=%#v\nwant=%#v", reparsed, tc.want)
			}

			fake := &fakeReadCRUD{}
			if _, err := got.CRUD(fake); err != nil {
				t.Fatalf("CRUD returned error: %v", err)
			}
			if fake.called != tc.wantScope {
				t.Fatalf("CRUD routed to %q, want %q", fake.called, tc.wantScope)
			}
			if !equalStrings(fake.args, tc.wantArgs) {
				t.Fatalf("CRUD args = %v, want %v", fake.args, tc.wantArgs)
			}
		})
	}
}
