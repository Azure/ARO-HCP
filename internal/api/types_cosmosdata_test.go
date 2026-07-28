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

package api

import (
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// TestToResourceIDStringsAreCanonical asserts that every ToXxxResourceIDString
// helper produces an ARM resource ID that round-trips through the ARM parser
// to the same lowercased string. Equivalently: the helper must produce the
// same string the informer cache stores under (which is
// strings.ToLower(resourceID.String()) for the canonical ResourceID).
//
// The ToServiceProviderNodePoolResourceIDString helper used to fail this test
// because it embedded the provider namespace twice; this test now guards
// against the same class of bug for every other helper.
func TestToResourceIDStringsAreCanonical(t *testing.T) {
	const (
		sub     = "00000000-0000-0000-0000-000000000001"
		rg      = "myrg"
		cluster = "mycluster"
		np      = "mynodepool"
		ea      = "myextauth"
		opName  = "myop"
		mcc     = "mymcc"
		ac      = "myadmincred"
	)

	tests := []struct {
		name string
		got  string
	}{
		{"ResourceGroup", ToResourceGroupResourceIDString(sub, rg)},
		{"Cluster", ToClusterResourceIDString(sub, rg, cluster)},
		{"NodePool", ToNodePoolResourceIDString(sub, rg, cluster, np)},
		{"ExternalAuth", ToExternalAuthResourceIDString(sub, rg, cluster, ea)},
		{"ServiceProviderCluster", ToServiceProviderClusterResourceIDString(sub, rg, cluster)},
		{"Operation", ToOperationResourceIDString(sub, opName)},
		{"ManagementClusterContent", ToManagementClusterContentResourceIDString(sub, rg, cluster, mcc)},
		{"ServiceProviderNodePool", ToServiceProviderNodePoolResourceIDString(sub, rg, cluster, np)},
		{"AdminCredential", ToAdminCredentialResourceIDString(sub, rg, cluster, ac)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// ResourceGroup IDs aren't full ARM resource IDs (they have no
			// /providers segment), but the rest must parse and round-trip.
			if tc.name == "ResourceGroup" {
				want := "/subscriptions/" + sub + "/resourcegroups/" + rg
				if tc.got != want {
					t.Fatalf("got %q\nwant %q", tc.got, want)
				}
				return
			}

			parsed, err := azcorearm.ParseResourceID(tc.got)
			if err != nil {
				t.Fatalf("helper produced an ID that does not parse: %v\ngot: %q", err, tc.got)
			}
			canonical := strings.ToLower(parsed.String())
			if tc.got != canonical {
				t.Fatalf("helper output is not canonical (informer cache lookups will miss):\nhelper:    %q\ncanonical: %q", tc.got, canonical)
			}
		})
	}
}
