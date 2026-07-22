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

package ocm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestConvertCStoClusterAdminCredentialStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      cmv1.BreakGlassCredentialStatus
		want    api.ClusterAdminCredentialStatus
		wantErr bool
	}{
		{name: "empty", in: "", wantErr: true},
		{name: "created", in: cmv1.BreakGlassCredentialStatusCreated, want: api.ClusterAdminCredentialStatusCreated},
		{name: "issued", in: cmv1.BreakGlassCredentialStatusIssued, want: api.ClusterAdminCredentialStatusIssued},
		{name: "failed", in: cmv1.BreakGlassCredentialStatusFailed, want: api.ClusterAdminCredentialStatusFailed},
		{name: "expired", in: cmv1.BreakGlassCredentialStatusExpired, want: api.ClusterAdminCredentialStatusExpired},
		{name: "awaiting_revocation", in: cmv1.BreakGlassCredentialStatusAwaitingRevocation, want: api.ClusterAdminCredentialStatusAwaitingRevocation},
		{name: "revoked", in: cmv1.BreakGlassCredentialStatusRevoked, want: api.ClusterAdminCredentialStatusRevoked},
		{name: "unknown", in: cmv1.BreakGlassCredentialStatus("fantasy"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ConvertCStoClusterAdminCredentialStatus(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
