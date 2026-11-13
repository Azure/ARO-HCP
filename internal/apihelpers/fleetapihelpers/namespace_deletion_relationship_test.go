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
	clusterID       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hcNamespace     = "ocm-production-" + clusterID
	cpNamespace     = hcNamespace + "-hcp1"
	hostedClusterNm = "hcp1"
)

func TestRelateHostedClusterNamespace(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		hostedClusterNamespace string
		hostedClusterName      string
		envIdentifier          string
		wantCPNamespace        string
		wantErr                bool
	}{
		{
			name:                   "hosted cluster namespace",
			hostedClusterNamespace: hcNamespace,
			hostedClusterName:      hostedClusterNm,
			wantCPNamespace:        cpNamespace,
		},
		{
			name:                   "normalize dot in hosted cluster name",
			hostedClusterNamespace: hcNamespace,
			hostedClusterName:      "my.cluster",
			wantCPNamespace:        hcNamespace + "-my-cluster",
		},
		{
			name:                   "control plane namespace is not a valid input",
			hostedClusterNamespace: cpNamespace,
			hostedClusterName:      hostedClusterNm,
			wantErr:                true,
		},
		{
			name:                   "control plane namespace with a 32-character hosted cluster name is rejected",
			hostedClusterNamespace: hcNamespace + "-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			hostedClusterName:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			wantErr:                true,
		},
		{
			name:                   "environment identifier may contain dashes",
			hostedClusterNamespace: "ocm-my-env-" + clusterID,
			hostedClusterName:      hostedClusterNm,
			wantCPNamespace:        "ocm-my-env-" + clusterID + "-hcp1",
		},
		{
			name:                   "environment identifier is enforced",
			hostedClusterNamespace: hcNamespace,
			hostedClusterName:      hostedClusterNm,
			envIdentifier:          "staging",
			wantErr:                true,
		},
		{
			name:                   "arbitrary namespace is rejected",
			hostedClusterNamespace: "some-random-namespace",
			hostedClusterName:      hostedClusterNm,
			wantErr:                true,
		},
		{
			name:                   "cluster ID of wrong length is rejected",
			hostedClusterNamespace: "ocm-production-tooshort",
			hostedClusterName:      hostedClusterNm,
			wantErr:                true,
		},
		{
			name:                   "cluster ID containing dash is rejected",
			hostedClusterNamespace: "ocm-production-aaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaa",
			hostedClusterName:      hostedClusterNm,
			wantErr:                true,
		},
		{
			name:                   "hosted cluster name is required",
			hostedClusterNamespace: hcNamespace,
			wantErr:                true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RelateHostedClusterNamespace(tc.hostedClusterNamespace, tc.hostedClusterName, tc.envIdentifier)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.hostedClusterNamespace, got.HostedClusterNamespace)
			require.Equal(t, tc.wantCPNamespace, got.ControlPlaneNamespace)
		})
	}
}
