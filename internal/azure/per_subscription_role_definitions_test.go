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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	testPlaceholderSubscriptionID      = "11111111-1111-1111-1111-111111111111"
	otherTestPlaceholderSubscriptionID = "00000000-0000-0000-0000-000000000000"

	diskStorageOperatorBuiltInRoleID   = "/providers/Microsoft.Authorization/roleDefinitions/5b7237c5-45e1-49d6-bc18-a1f62f400748"
	fileStorageOperatorBuiltInRoleID   = "/providers/Microsoft.Authorization/roleDefinitions/0d7aedc0-15fd-4a67-a412-efad370c947e"
	imageRegistryOperatorBuiltInRoleID = "/providers/Microsoft.Authorization/roleDefinitions/8b32b316-c2f5-4ddf-b05b-83dacd2d08b5"
)

func resolvedRoleDefinitionIDs(t *testing.T, identity *BaseClusterScopedIdentity, subscriptionID string) []string {
	t.Helper()
	ids, err := identity.RoleDefinitionsResourceIDs(subscriptionID)
	require.NoError(t, err)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// TestDevDataPlaneOperatorsUsePerSubscriptionRoles verifies that, in the dev config set, the disk CSI,
// file CSI and image registry data-plane operators resolve to the per-subscription custom roles created by
// dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep, while their
// control-plane counterparts keep the built-in roles.
func TestDevDataPlaneOperatorsUsePerSubscriptionRoles(t *testing.T) {
	t.Parallel()

	cfg := NewClusterScopedIdentitiesConfig(RoleDefinitionConfigSetNameDev)

	tests := []struct {
		operator                ClusterOperatorIdentifier
		expectedDataPlaneRoleID string
		expectedControlPlaneID  string
	}{
		{
			operator:                ClusterOperatorIdentifierDiskCSIDriver,
			expectedDataPlaneRoleID: "/providers/Microsoft.Authorization/roleDefinitions/5907dcbb-f5f4-547f-84e2-a854a5d7e3a9",
			expectedControlPlaneID:  diskStorageOperatorBuiltInRoleID,
		},
		{
			operator:                ClusterOperatorIdentifierFileCSIDriver,
			expectedDataPlaneRoleID: "/providers/Microsoft.Authorization/roleDefinitions/54f2f0e6-36cd-5318-9501-adc566aed026",
			expectedControlPlaneID:  fileStorageOperatorBuiltInRoleID,
		},
		{
			operator:                ClusterOperatorIdentifierImageRegistry,
			expectedDataPlaneRoleID: "/providers/Microsoft.Authorization/roleDefinitions/a18cae55-6b07-5669-bc62-3d4560308db9",
			expectedControlPlaneID:  imageRegistryOperatorBuiltInRoleID,
		},
	}
	for _, tt := range tests {
		t.Run(string(tt.operator), func(t *testing.T) {
			t.Parallel()

			dataPlane := cfg.DataPlaneOperatorsIdentities[tt.operator]
			require.NotNil(t, dataPlane)
			assert.Equal(t, []string{tt.expectedDataPlaneRoleID},
				resolvedRoleDefinitionIDs(t, &dataPlane.BaseClusterScopedIdentity, testPlaceholderSubscriptionID))

			// Bicep's subscription().id is lowercase, so the subscription ID casing must not matter.
			upperCaseSubscriptionID := "11111111-AAAA-BBBB-CCCC-DDDDDDDDDDDD"
			lowerCaseSubscriptionID := "11111111-aaaa-bbbb-cccc-dddddddddddd"
			assert.Equal(t,
				resolvedRoleDefinitionIDs(t, &dataPlane.BaseClusterScopedIdentity, lowerCaseSubscriptionID),
				resolvedRoleDefinitionIDs(t, &dataPlane.BaseClusterScopedIdentity, upperCaseSubscriptionID),
				"subscription ID casing must not change the calculated role definition ID")

			// A different subscription has a different copy of the role.
			assert.NotEqual(t,
				resolvedRoleDefinitionIDs(t, &dataPlane.BaseClusterScopedIdentity, testPlaceholderSubscriptionID),
				resolvedRoleDefinitionIDs(t, &dataPlane.BaseClusterScopedIdentity, otherTestPlaceholderSubscriptionID))

			controlPlane := cfg.ControlPlaneOperatorsIdentities[tt.operator]
			require.NotNil(t, controlPlane)
			assert.Equal(t, []string{tt.expectedControlPlaneID},
				resolvedRoleDefinitionIDs(t, &controlPlane.BaseClusterScopedIdentity, testPlaceholderSubscriptionID))
		})
	}
}

// TestPublicConfigSetIsSubscriptionIndependent verifies that the public config set never uses
// per-subscription role definitions, so its role definition IDs are identical for every subscription.
func TestPublicConfigSetIsSubscriptionIndependent(t *testing.T) {
	t.Parallel()

	cfg := NewClusterScopedIdentitiesConfig(RoleDefinitionConfigSetNamePublic)

	identities := map[string]*BaseClusterScopedIdentity{
		"service managed identity": &cfg.ServiceManagedIdentity.BaseClusterScopedIdentity,
	}
	for operator, identity := range cfg.ControlPlaneOperatorsIdentities {
		identities["control plane "+string(operator)] = &identity.BaseClusterScopedIdentity
	}
	for operator, identity := range cfg.DataPlaneOperatorsIdentities {
		identities["data plane "+string(operator)] = &identity.BaseClusterScopedIdentity
	}

	for name, identity := range identities {
		for _, rd := range identity.RoleDefinitions {
			require.NotNil(t, rd, name)
			assert.Empty(t, rd.CustomRoleName, "%s must not use a custom role", name)
			assert.NotNil(t, rd.ResourceID, "%s must have a fixed role definition ID", name)
		}
		assert.Equal(t,
			resolvedRoleDefinitionIDs(t, identity, testPlaceholderSubscriptionID),
			resolvedRoleDefinitionIDs(t, identity, otherTestPlaceholderSubscriptionID),
			name)
	}

	// The public data-plane operators keep the built-in roles.
	assert.Equal(t, []string{diskStorageOperatorBuiltInRoleID},
		resolvedRoleDefinitionIDs(t, &cfg.DataPlaneOperatorsIdentities[ClusterOperatorIdentifierDiskCSIDriver].BaseClusterScopedIdentity, testPlaceholderSubscriptionID))
	assert.Equal(t, []string{fileStorageOperatorBuiltInRoleID},
		resolvedRoleDefinitionIDs(t, &cfg.DataPlaneOperatorsIdentities[ClusterOperatorIdentifierFileCSIDriver].BaseClusterScopedIdentity, testPlaceholderSubscriptionID))
	assert.Equal(t, []string{imageRegistryOperatorBuiltInRoleID},
		resolvedRoleDefinitionIDs(t, &cfg.DataPlaneOperatorsIdentities[ClusterOperatorIdentifierImageRegistry].BaseClusterScopedIdentity, testPlaceholderSubscriptionID))
}

func TestResourceIDForSubscription(t *testing.T) {
	t.Parallel()

	fixedID, err := azcorearm.ParseResourceID(diskStorageOperatorBuiltInRoleID)
	require.NoError(t, err)

	tests := []struct {
		name           string
		roleDefinition *ClusterScopedIdentityRoleDefinition
		subscriptionID string
		expected       string
		expectErr      bool
	}{
		{
			name:           "fixed resource ID ignores the subscription",
			roleDefinition: &ClusterScopedIdentityRoleDefinition{DescriptiveName: "fixed", ResourceID: fixedID},
			subscriptionID: testPlaceholderSubscriptionID,
			expected:       diskStorageOperatorBuiltInRoleID,
		},
		{
			name:           "fixed resource ID does not need a subscription",
			roleDefinition: &ClusterScopedIdentityRoleDefinition{DescriptiveName: "fixed", ResourceID: fixedID},
			expected:       diskStorageOperatorBuiltInRoleID,
		},
		{
			name:           "custom role requires a subscription",
			roleDefinition: &ClusterScopedIdentityRoleDefinition{DescriptiveName: "custom", CustomRoleName: dataPlaneDiskCSIDriverDevRoleName},
			expectErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.roleDefinition.ResourceIDForSubscription(tt.subscriptionID)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got.String())
		})
	}
}

func TestRoleDefinitionsForSetNameGuardRails(t *testing.T) {
	t.Parallel()

	fixedID, err := azcorearm.ParseResourceID(diskStorageOperatorBuiltInRoleID)
	require.NoError(t, err)

	fixed := &ClusterScopedIdentityRoleDefinition{DescriptiveName: "fixed", ResourceID: fixedID}
	customRole := &ClusterScopedIdentityRoleDefinition{DescriptiveName: "custom", CustomRoleName: "custom"}
	both := &ClusterScopedIdentityRoleDefinition{DescriptiveName: "both", ResourceID: fixedID, CustomRoleName: "custom"}
	neither := &ClusterScopedIdentityRoleDefinition{DescriptiveName: "neither"}

	tests := []struct {
		name      string
		setName   RoleDefinitionConfigSetName
		spec      *clusterScopedIdentityRoleDefinitionConfigSetsSpec
		expectErr bool
	}{
		{name: "dev allows a custom role", setName: RoleDefinitionConfigSetNameDev, spec: &clusterScopedIdentityRoleDefinitionConfigSetsSpec{Dev: customRole, Public: fixed}},
		{name: "public allows a fixed role", setName: RoleDefinitionConfigSetNamePublic, spec: &clusterScopedIdentityRoleDefinitionConfigSetsSpec{Dev: customRole, Public: fixed}},
		{name: "public rejects a custom role", setName: RoleDefinitionConfigSetNamePublic, spec: &clusterScopedIdentityRoleDefinitionConfigSetsSpec{Dev: fixed, Public: customRole}, expectErr: true},
		{name: "dev rejects both fields set", setName: RoleDefinitionConfigSetNameDev, spec: &clusterScopedIdentityRoleDefinitionConfigSetsSpec{Dev: both, Public: fixed}, expectErr: true},
		{name: "dev rejects neither field set", setName: RoleDefinitionConfigSetNameDev, spec: &clusterScopedIdentityRoleDefinitionConfigSetsSpec{Dev: neither, Public: fixed}, expectErr: true},
		{name: "public rejects neither field set", setName: RoleDefinitionConfigSetNamePublic, spec: &clusterScopedIdentityRoleDefinitionConfigSetsSpec{Dev: fixed, Public: neither}, expectErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := roleDefinitionsForSetName(tt.setName, []*clusterScopedIdentityRoleDefinitionConfigSetsSpec{tt.spec})
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
