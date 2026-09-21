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

package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClusterScopedIdentitiesConfigOperatorIdentifiersRejectPipe(t *testing.T) {
	t.Parallel()

	for _, setName := range []RoleDefinitionConfigSetName{
		RoleDefinitionConfigSetNameDev,
		RoleDefinitionConfigSetNamePublic,
	} {
		t.Run(string(setName), func(t *testing.T) {
			t.Parallel()

			cfg := NewClusterScopedIdentitiesConfig(setName)
			require.NoError(t, validateClusterOperatorIdentifiers(cfg))

			for operator, identity := range cfg.ControlPlaneOperatorsIdentities {
				assert.NotContains(t, string(operator), string(clusterOperatorIdentifierForbiddenRune), "control plane operator map key")
				require.NotNil(t, identity)
				assert.NotContains(t, string(identity.ClusterOperatorIdentifier), string(clusterOperatorIdentifierForbiddenRune), "control plane operator identifier field")
			}
			for operator, identity := range cfg.DataPlaneOperatorsIdentities {
				assert.NotContains(t, string(operator), string(clusterOperatorIdentifierForbiddenRune), "data plane operator map key")
				require.NotNil(t, identity)
				assert.NotContains(t, string(identity.ClusterOperatorIdentifier), string(clusterOperatorIdentifierForbiddenRune), "data plane operator identifier field")
			}
		})
	}
}

func TestValidateClusterOperatorIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		config        *ClusterScopedIdentitiesConfig
		wantErrSubstr string
	}{
		{
			name:   "empty maps",
			config: &ClusterScopedIdentitiesConfig{},
		},
		{
			name: "valid identifiers",
			config: &ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: ControlPlaneOperatorsIdentities{
					ClusterOperatorIdentifierControlPlane: {
						BaseClusterScopedOperatorIdentity: BaseClusterScopedOperatorIdentity{
							ClusterOperatorIdentifier: ClusterOperatorIdentifierControlPlane,
						},
					},
				},
				DataPlaneOperatorsIdentities: DataPlaneOperatorsIdentities{
					ClusterOperatorIdentifierDiskCSIDriver: {
						BaseClusterScopedOperatorIdentity: BaseClusterScopedOperatorIdentity{
							ClusterOperatorIdentifier: ClusterOperatorIdentifierDiskCSIDriver,
						},
					},
				},
			},
		},
		{
			name: "control plane map key contains pipe",
			config: &ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: ControlPlaneOperatorsIdentities{
					ClusterOperatorIdentifier("bad|operator"): {},
				},
			},
			wantErrSubstr: `control plane operator identifier "bad|operator" must not contain '|'`,
		},
		{
			name: "control plane identifier field contains pipe",
			config: &ClusterScopedIdentitiesConfig{
				ControlPlaneOperatorsIdentities: ControlPlaneOperatorsIdentities{
					ClusterOperatorIdentifierControlPlane: {
						BaseClusterScopedOperatorIdentity: BaseClusterScopedOperatorIdentity{
							ClusterOperatorIdentifier: ClusterOperatorIdentifier("bad|operator"),
						},
					},
				},
			},
			wantErrSubstr: `control plane operator identifier "bad|operator" must not contain '|'`,
		},
		{
			name: "data plane map key contains pipe",
			config: &ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: DataPlaneOperatorsIdentities{
					ClusterOperatorIdentifier("bad|operator"): {},
				},
			},
			wantErrSubstr: `data plane operator identifier "bad|operator" must not contain '|'`,
		},
		{
			name: "data plane identifier field contains pipe",
			config: &ClusterScopedIdentitiesConfig{
				DataPlaneOperatorsIdentities: DataPlaneOperatorsIdentities{
					ClusterOperatorIdentifierDiskCSIDriver: {
						BaseClusterScopedOperatorIdentity: BaseClusterScopedOperatorIdentity{
							ClusterOperatorIdentifier: ClusterOperatorIdentifier("bad|operator"),
						},
					},
				},
			},
			wantErrSubstr: `data plane operator identifier "bad|operator" must not contain '|'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateClusterOperatorIdentifiers(tt.config)
			if tt.wantErrSubstr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErrSubstr, err.Error())
		})
	}
}
