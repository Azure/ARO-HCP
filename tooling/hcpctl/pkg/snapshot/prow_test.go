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

package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeTestName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "already clean",
			input: "TestHCPCreation",
			want:  "TestHCPCreation",
		},
		{
			name:  "spaces replaced",
			input: "Test HCP Creation",
			want:  "Test_HCP_Creation",
		},
		{
			name:  "brackets and spaces",
			input: "[sig-hypershift] TestHCPCreation should create a cluster",
			want:  "_sig-hypershift__TestHCPCreation_should_create_a_cluster",
		},
		{
			name:  "dashes and underscores preserved",
			input: "Test-HCP_Creation",
			want:  "Test-HCP_Creation",
		},
		{
			name:  "dots replaced",
			input: "Test.HCP.Creation",
			want:  "Test_HCP_Creation",
		},
		{
			name:  "slashes replaced",
			input: "TestHCP/Creation",
			want:  "TestHCP_Creation",
		},
		{
			name:  "real example",
			input: "Customer should be able to create a no-CNI private cluster with a private key vault, a nodepool and install cilium CNI successfully",
			want:  "Customer_should_be_able_to_create_a_no-CNI_private_cluster_with_a_private_key_vault__a_nodepool_and_install_cilium_CNI_successfully",
		},
		{
			name:  "really long example",
			input: "Customer should be able to create a no-CNI private cluster with a private key vault, a nodepool and install cilium CNI successfully using v20251223preview API and OpenShift candidate channel for 5.4 really fast",
			want:  "Customer_should_be_able_to_create_a_no-CNI_private_cluster_with_a_private_key_vault__a_nodepool_and_install_cilium_CNI_successfully_using_v20251223preview_API_and_OpenShift_candidate_channel_for_5_4_r",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SanitizeTestName(tc.input))
		})
	}
}
