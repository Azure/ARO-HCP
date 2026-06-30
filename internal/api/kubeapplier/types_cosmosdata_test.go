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

package kubeapplier

import (
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	testIDPrefix                  = "/subscriptions/00000000-0000-0000-0000-000000000001/resourcegroups/myrg"
	testClusterIDPrefix           = testIDPrefix + "/providers/microsoft.redhatopenshift/hcpopenshiftclusters/mycluster"
	testNodePoolIDPrefix          = testClusterIDPrefix + "/nodepools/mynodepool"
	testCredentialRequestIDPrefix = testClusterIDPrefix + "/systemadmincredentialrequests/mycred"
)

func TestResourceIDStrings(t *testing.T) {
	const (
		sub     = "00000000-0000-0000-0000-000000000001"
		rg      = "myRG"
		cluster = "myCluster"
		np      = "myNodePool"
		cred    = "myCred"
		name    = "myDesire"
	)
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "ApplyDesire under cluster",
			got:  ToClusterScopedApplyDesireResourceIDString(sub, rg, cluster, name),
			want: testClusterIDPrefix + "/applydesires/mydesire",
		},
		{
			name: "ApplyDesire under nodepool",
			got:  ToNodePoolScopedApplyDesireResourceIDString(sub, rg, cluster, np, name),
			want: testNodePoolIDPrefix + "/applydesires/mydesire",
		},
		{
			name: "ReadDesire under cluster",
			got:  ToClusterScopedReadDesireResourceIDString(sub, rg, cluster, name),
			want: testClusterIDPrefix + "/readdesires/mydesire",
		},
		{
			name: "ReadDesire under nodepool",
			got:  ToNodePoolScopedReadDesireResourceIDString(sub, rg, cluster, np, name),
			want: testNodePoolIDPrefix + "/readdesires/mydesire",
		},
		{
			name: "ApplyDesire under credential request",
			got:  ToCredentialRequestScopedApplyDesireResourceIDString(sub, rg, cluster, cred, name),
			want: testCredentialRequestIDPrefix + "/applydesires/mydesire",
		},
		{
			name: "DeleteDesire under credential request",
			got:  ToCredentialRequestScopedDeleteDesireResourceIDString(sub, rg, cluster, cred, name),
			want: testCredentialRequestIDPrefix + "/deletedesires/mydesire",
		},
		{
			name: "ReadDesire under credential request",
			got:  ToCredentialRequestScopedReadDesireResourceIDString(sub, rg, cluster, cred, name),
			want: testCredentialRequestIDPrefix + "/readdesires/mydesire",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got  %q\nwant %q", tc.got, tc.want)
			}
			if _, err := azcorearm.ParseResourceID(tc.got); err != nil {
				t.Errorf("returned ID does not parse as a valid azcorearm.ResourceID: %v", err)
			}
		})
	}
}

func TestResourceIDsParseToExpectedTypes(t *testing.T) {
	const (
		sub     = "00000000-0000-0000-0000-000000000001"
		rg      = "myRG"
		cluster = "myCluster"
		np      = "myNodePool"
		cred    = "myCred"
		name    = "myDesire"
	)
	cases := []struct {
		name     string
		idStr    string
		wantType string
	}{
		{
			name:     "cluster-scoped ApplyDesire",
			idStr:    ToClusterScopedApplyDesireResourceIDString(sub, rg, cluster, name),
			wantType: ClusterScopedApplyDesireResourceType.String(),
		},
		{
			name:     "nodepool-scoped ApplyDesire",
			idStr:    ToNodePoolScopedApplyDesireResourceIDString(sub, rg, cluster, np, name),
			wantType: NodePoolScopedApplyDesireResourceType.String(),
		},
		{
			name:     "cluster-scoped ReadDesire",
			idStr:    ToClusterScopedReadDesireResourceIDString(sub, rg, cluster, name),
			wantType: ClusterScopedReadDesireResourceType.String(),
		},
		{
			name:     "nodepool-scoped ReadDesire",
			idStr:    ToNodePoolScopedReadDesireResourceIDString(sub, rg, cluster, np, name),
			wantType: NodePoolScopedReadDesireResourceType.String(),
		},
		{
			name:     "credential-request-scoped ApplyDesire",
			idStr:    ToCredentialRequestScopedApplyDesireResourceIDString(sub, rg, cluster, cred, name),
			wantType: CredentialRequestScopedApplyDesireResourceType.String(),
		},
		{
			name:     "credential-request-scoped DeleteDesire",
			idStr:    ToCredentialRequestScopedDeleteDesireResourceIDString(sub, rg, cluster, cred, name),
			wantType: CredentialRequestScopedDeleteDesireResourceType.String(),
		},
		{
			name:     "credential-request-scoped ReadDesire",
			idStr:    ToCredentialRequestScopedReadDesireResourceIDString(sub, rg, cluster, cred, name),
			wantType: CredentialRequestScopedReadDesireResourceType.String(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := azcorearm.ParseResourceID(tc.idStr)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			gotType := id.ResourceType.String()
			if !equalFold(gotType, tc.wantType) {
				t.Errorf("type = %q want %q (case-insensitive)", gotType, tc.wantType)
			}
		})
	}
}

// equalFold compares strings case-insensitively. ResourceType.String() preserves original
// casing (e.g. "Microsoft.RedHatOpenShift/...") while ID strings are lower-cased on the way out.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca := a[i]
		cb := b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
