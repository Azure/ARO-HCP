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

package kubeappliercosmosstorage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func TestParseDesireScope(t *testing.T) {
	t.Parallel()

	mustParseResourceID := func(s string) *azcorearm.ResourceID {
		id, err := azcorearm.ParseResourceID(s)
		require.NoError(t, err)
		return id
	}

	tests := []struct {
		name            string
		id              *azcorearm.ResourceID
		wantAncestry    Ancestry
		wantErr         string
		wantBuilderType cosmosstorageutils.ResourceIDBuilder
	}{
		{
			name:            "cluster parent",
			id:              mustParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"),
			wantAncestry:    ClusterAncestry,
			wantBuilderType: cosmosstorageutils.ClusterNestedResourceIDBuilder{},
		},
		{
			name:            "node pool parent",
			id:              mustParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/np"),
			wantAncestry:    ClusterAncestry,
			wantBuilderType: cosmosstorageutils.ClusterNestedResourceIDBuilder{},
		},
		{
			name:            "credential request parent",
			id:              mustParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRequests/req"),
			wantAncestry:    ClusterAncestry,
			wantBuilderType: cosmosstorageutils.ClusterNestedResourceIDBuilder{},
		},
		{
			name:            "credential revocation parent",
			id:              mustParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRevocations/rev"),
			wantAncestry:    ClusterAncestry,
			wantBuilderType: cosmosstorageutils.ClusterNestedResourceIDBuilder{},
		},
		{
			name:            "management cluster parent",
			id:              mustParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/eastus/managementClusters/default"),
			wantAncestry:    StampAncestry,
			wantBuilderType: cosmosstorageutils.FleetResourceIDBuilder{},
		},
		{
			name:    "nil resource ID",
			id:      nil,
			wantErr: "desire scope resource ID is nil",
		},
		{
			name:    "unsupported resource type",
			id:      mustParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm"),
			wantErr: "is not a valid desire scope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scope, err := ParseDesireScope(tt.id)
			if len(tt.wantErr) != 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantAncestry, scope.Ancestry())
			assert.Equal(t, tt.id, scope.ResourceID())
			assert.IsType(t, tt.wantBuilderType, scope.ResourceIDBuilder())
		})
	}
}

func TestDesireScopeConstructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		constructor  func() (DesireScope, error)
		wantType     azcorearm.ResourceType
		wantAncestry Ancestry
	}{
		{
			name:         "ClusterScope",
			constructor:  func() (DesireScope, error) { return ClusterScope("sub", "rg", "cluster") },
			wantType:     coreapi.ClusterResourceType,
			wantAncestry: ClusterAncestry,
		},
		{
			name:         "NodePoolScope",
			constructor:  func() (DesireScope, error) { return NodePoolScope("sub", "rg", "cluster", "np") },
			wantType:     coreapi.NodePoolResourceType,
			wantAncestry: ClusterAncestry,
		},
		{
			name: "CredentialRequestScope",
			constructor: func() (DesireScope, error) {
				return CredentialRequestScope("sub", "rg", "cluster", "req")
			},
			wantType:     coreapi.SystemAdminCredentialRequestResourceType,
			wantAncestry: ClusterAncestry,
		},
		{
			name: "CredentialRevocationScope",
			constructor: func() (DesireScope, error) {
				return CredentialRevocationScope("sub", "rg", "cluster", "rev")
			},
			wantType:     coreapi.SystemAdminCredentialRevocationResourceType,
			wantAncestry: ClusterAncestry,
		},
		{
			name:         "ManagementClusterScope",
			constructor:  func() (DesireScope, error) { return ManagementClusterScope("eastus") },
			wantType:     fleetapi.ManagementClusterResourceType,
			wantAncestry: StampAncestry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scope, err := tt.constructor()
			require.NoError(t, err)
			assert.Equal(t, tt.wantAncestry, scope.Ancestry())
			assert.True(t, metadataapi.ResourceTypeEqual(tt.wantType, scope.ResourceID().ResourceType),
				"resource type mismatch: want %s, got %s", tt.wantType, scope.ResourceID().ResourceType)
		})
	}
}
