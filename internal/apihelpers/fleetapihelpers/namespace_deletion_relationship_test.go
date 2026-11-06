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

package fleetapihelpers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	clusterID       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 32 lowercase alphanumerics
	hcNamespace     = "ocm-production-" + clusterID
	cpNamespace     = hcNamespace + "-hcp1"
	hostedClusterNm = "hcp1"
)

func TestClassifyNamespace(t *testing.T) {
	for _, tc := range []struct {
		name              string
		namespaceName     string
		hostedClusterName string
		envIdentifier     string
		wantRole          NamespaceRole
		wantHCNamespace   string
		wantCPNamespace   string
		wantErr           bool
	}{
		{
			name:              "hosted cluster namespace",
			namespaceName:     hcNamespace,
			hostedClusterName: hostedClusterNm,
			wantRole:          NamespaceRoleHostedCluster,
			wantHCNamespace:   hcNamespace,
			wantCPNamespace:   cpNamespace,
		},
		{
			name:              "control plane namespace",
			namespaceName:     cpNamespace,
			hostedClusterName: hostedClusterNm,
			wantRole:          NamespaceRoleControlPlane,
			wantHCNamespace:   hcNamespace,
			wantCPNamespace:   cpNamespace,
		},
		{
			// HyperShift normalizes dots to dashes when building the control
			// plane namespace, so the parser must apply the same rule rather
			// than assume the simpler helper form.
			name:              "domain prefix containing a dot is normalized",
			namespaceName:     hcNamespace + "-my-cluster",
			hostedClusterName: "my.cluster",
			wantRole:          NamespaceRoleControlPlane,
			wantHCNamespace:   hcNamespace,
			wantCPNamespace:   hcNamespace + "-my-cluster",
		},
		{
			name:              "environment identifier may contain dashes",
			namespaceName:     "ocm-my-env-" + clusterID,
			hostedClusterName: hostedClusterNm,
			wantRole:          NamespaceRoleHostedCluster,
			wantHCNamespace:   "ocm-my-env-" + clusterID,
			wantCPNamespace:   "ocm-my-env-" + clusterID + "-hcp1",
		},
		{
			name:              "environment identifier is enforced when supplied",
			namespaceName:     hcNamespace,
			hostedClusterName: hostedClusterNm,
			envIdentifier:     "staging",
			wantErr:           true,
		},
		{
			name:              "arbitrary namespace is rejected",
			namespaceName:     "some-random-namespace",
			hostedClusterName: hostedClusterNm,
			wantErr:           true,
		},
		{
			name:              "missing ocm prefix is rejected",
			namespaceName:     "foo-production-" + clusterID,
			hostedClusterName: hostedClusterNm,
			wantErr:           true,
		},
		{
			// A prefix match would wrongly accept this; reconstruction does not.
			name:              "control plane namespace of a different hosted cluster is rejected",
			namespaceName:     cpNamespace,
			hostedClusterName: "hcp2",
			wantErr:           true,
		},
		{
			name:              "cluster ID of the wrong length is rejected",
			namespaceName:     "ocm-production-tooshort",
			hostedClusterName: hostedClusterNm,
			wantErr:           true,
		},
		{
			name:              "cluster ID containing a dash is rejected",
			namespaceName:     "ocm-production-aaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaa",
			hostedClusterName: hostedClusterNm,
			wantErr:           true,
		},
		{
			name:              "empty hostedClusterName is rejected",
			namespaceName:     hcNamespace,
			hostedClusterName: "",
			wantErr:           true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClassifyNamespace(tc.namespaceName, tc.hostedClusterName, tc.envIdentifier)

			if tc.wantErr {
				require.Error(t, err, "expected %q to be rejected rather than guessed at", tc.namespaceName)
				return
			}
			require.NoError(t, err, "expected %q to classify cleanly", tc.namespaceName)
			require.Equal(t, tc.wantRole, got.Role, "wrong namespace role")
			require.Equal(t, tc.wantHCNamespace, got.HostedClusterNamespace, "wrong HostedCluster namespace")
			require.Equal(t, tc.wantCPNamespace, got.ControlPlaneNamespace, "wrong control plane namespace")
		})
	}
}

// TestClassifyNamespaceRulesAreMutuallyExclusive asserts the invariant the two
// classification rules rest on: a cluster ID is 32 lowercase alphanumerics with
// no dashes, while a HostedCluster name is a short DNS label, so a control
// plane namespace can never also satisfy the HostedCluster-namespace rule.
func TestClassifyNamespaceRulesAreMutuallyExclusive(t *testing.T) {
	hc, err := ClassifyNamespace(hcNamespace, hostedClusterNm, "")
	require.NoError(t, err)
	cp, err := ClassifyNamespace(cpNamespace, hostedClusterNm, "")
	require.NoError(t, err)

	require.NotEqual(t, hc.Role, cp.Role, "the two namespaces must classify into different roles")
	require.Equal(t, hc.HostedClusterNamespace, cp.HostedClusterNamespace,
		"both roles must agree on which namespace holds the HostedCluster")
	require.Equal(t, hc.ControlPlaneNamespace, cp.ControlPlaneNamespace,
		"both roles must agree on which namespace holds the control plane")
}
