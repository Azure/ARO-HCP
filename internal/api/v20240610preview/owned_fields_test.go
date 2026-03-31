// Copyright 2025 Microsoft Corporation
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

package v20240610preview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// TestV2024ZeroOwnedFields_PreservesV2025ExclusiveFields verifies that
// v2024 ZeroOwnedFields preserves v2025-exclusive fields:
// ImageDigestMirrors, VnetIntegrationSubnetID, and Kms.Visibility.
// These fields were introduced in v20251223preview and must NOT be
// zeroed by a v2024 PUT.
func TestV2024ZeroOwnedFields_PreservesV2025ExclusiveFields(t *testing.T) {
	internal := newClusterWithAllFields()

	ext := &HcpOpenShiftCluster{}
	ext.ZeroOwnedFields(internal)

	t.Run("ImageDigestMirrors preserved", func(t *testing.T) {
		require.Len(t, internal.CustomerProperties.ImageDigestMirrors, 1,
			"ImageDigestMirrors must survive v2024 ZeroOwnedFields")
		require.Equal(t, "quay.io/example", internal.CustomerProperties.ImageDigestMirrors[0].Source)
	})

	t.Run("VnetIntegrationSubnetID preserved", func(t *testing.T) {
		require.NotNil(t, internal.CustomerProperties.Platform.VnetIntegrationSubnetID,
			"VnetIntegrationSubnetID must survive v2024 ZeroOwnedFields")
	})

	t.Run("Kms.Visibility preserved", func(t *testing.T) {
		// After ZeroOwnedFields, CustomerManaged is not zeroed wholesale;
		// only the leaf fields v2024 owns are zeroed. The CustomerManaged
		// pointer itself should survive because v2024 zeros at the leaf level.
		//
		// However: after ZeroOwnedFields, the Kms struct's owned fields
		// (ActiveKey.Name, ActiveKey.Version, ActiveKey.VaultName) are zeroed,
		// but Visibility is NOT zeroed.
		require.NotNil(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged,
			"CustomerManaged must survive v2024 ZeroOwnedFields")
		require.NotNil(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms,
			"Kms must survive v2024 ZeroOwnedFields")
		require.Equal(t, api.KeyVaultVisibilityPrivate,
			internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility,
			"Kms.Visibility must survive v2024 ZeroOwnedFields")
	})
}

// TestV2024ZeroOwnedFields_ZeroesV2024OwnedFields verifies that v2024
// ZeroOwnedFields does zero the fields it owns.
func TestV2024ZeroOwnedFields_ZeroesV2024OwnedFields(t *testing.T) {
	internal := newClusterWithAllFields()

	ext := &HcpOpenShiftCluster{}
	ext.ZeroOwnedFields(internal)

	// ARM metadata must be zeroed.
	require.Nil(t, internal.ID, "ID should be zeroed")
	require.Empty(t, internal.Name, "Name should be zeroed")
	require.Empty(t, internal.Type, "Type should be zeroed")
	require.Empty(t, internal.Location, "Location should be zeroed")
	require.Nil(t, internal.Tags, "Tags should be zeroed")
	require.Nil(t, internal.Identity, "Identity should be zeroed")

	// Customer properties owned by v2024.
	require.Equal(t, api.VersionProfile{}, internal.CustomerProperties.Version,
		"Version should be zeroed")
	require.Equal(t, api.CustomerDNSProfile{}, internal.CustomerProperties.DNS,
		"DNS should be zeroed")
	require.Equal(t, api.NetworkProfile{}, internal.CustomerProperties.Network,
		"Network should be zeroed")
	require.Equal(t, api.CustomerAPIProfile{}, internal.CustomerProperties.API,
		"API should be zeroed")
	require.Equal(t, api.ClusterAutoscalingProfile{}, internal.CustomerProperties.Autoscaling,
		"Autoscaling should be zeroed")
	require.Equal(t, int32(0), internal.CustomerProperties.NodeDrainTimeoutMinutes,
		"NodeDrainTimeoutMinutes should be zeroed")
	require.Equal(t, api.ClusterImageRegistryProfile{}, internal.CustomerProperties.ClusterImageRegistry,
		"ClusterImageRegistry should be zeroed")

	// Platform: v2024-owned fields must be zeroed.
	require.Empty(t, internal.CustomerProperties.Platform.ManagedResourceGroup,
		"ManagedResourceGroup should be zeroed")
	require.Nil(t, internal.CustomerProperties.Platform.SubnetID,
		"SubnetID should be zeroed")
	require.Empty(t, string(internal.CustomerProperties.Platform.OutboundType),
		"OutboundType should be zeroed")
	require.Nil(t, internal.CustomerProperties.Platform.NetworkSecurityGroupID,
		"NetworkSecurityGroupID should be zeroed")
	require.Equal(t, api.OperatorsAuthenticationProfile{}, internal.CustomerProperties.Platform.OperatorsAuthentication,
		"OperatorsAuthentication should be zeroed")

	// Etcd: v2024-owned leaf fields must be zeroed.
	require.Empty(t, string(internal.CustomerProperties.Etcd.DataEncryption.KeyManagementMode),
		"KeyManagementMode should be zeroed")
	require.Empty(t, string(internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.EncryptionType),
		"EncryptionType should be zeroed")
	require.Empty(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Name,
		"ActiveKey.Name should be zeroed")
	require.Empty(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName,
		"ActiveKey.VaultName should be zeroed")
	require.Empty(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version,
		"ActiveKey.Version should be zeroed")
}

// TestV2024ZeroOwnedFields_PreservesServiceProviderProperties verifies that
// ServiceProviderProperties are never touched by ZeroOwnedFields.
func TestV2024ZeroOwnedFields_PreservesServiceProviderProperties(t *testing.T) {
	internal := newClusterWithAllFields()

	origSPP := internal.ServiceProviderProperties

	ext := &HcpOpenShiftCluster{}
	ext.ZeroOwnedFields(internal)

	require.Equal(t, origSPP.ProvisioningState, internal.ServiceProviderProperties.ProvisioningState,
		"ProvisioningState must survive ZeroOwnedFields")
	require.Equal(t, origSPP.ActiveOperationID, internal.ServiceProviderProperties.ActiveOperationID,
		"ActiveOperationID must survive ZeroOwnedFields")
	require.Equal(t, origSPP.ManagedIdentitiesDataPlaneIdentityURL, internal.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		"ManagedIdentitiesDataPlaneIdentityURL must survive ZeroOwnedFields")
}

// TestV2024ZeroOwnedFields_NilCustomerManaged verifies that ZeroOwnedFields
// handles a nil CustomerManaged pointer without panicking.
func TestV2024ZeroOwnedFields_NilCustomerManaged(t *testing.T) {
	internal := newClusterWithAllFields()
	internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged = nil

	ext := &HcpOpenShiftCluster{}

	// Must not panic.
	require.NotPanics(t, func() {
		ext.ZeroOwnedFields(internal)
	})
}

// TestV2024ApplyOwnedFields_PlatformManaged_ClearsCustomerManaged verifies that
// when a v2024 PUT request sets keyManagementMode=PlatformManaged WITHOUT a
// customerManaged block, the pre-existing CustomerManaged struct (which may
// contain v2025-exclusive fields like Kms.Visibility) is correctly nilled out.
// This is the CustomerManaged-to-PlatformManaged transition regression test.
func TestV2024ApplyOwnedFields_PlatformManaged_ClearsCustomerManaged(t *testing.T) {
	internal := newClusterWithAllFields()

	// Verify precondition: internal has a populated CustomerManaged with Kms.Visibility
	require.NotNil(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged)
	require.NotNil(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms)
	require.Equal(t, api.KeyVaultVisibilityPrivate,
		internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility)

	// Build a v2024 external type that sets PlatformManaged with no customerManaged block.
	platformManaged := generated.EtcdDataEncryptionKeyManagementModeTypePlatformManaged
	ext := &HcpOpenShiftCluster{
		HcpOpenShiftCluster: generated.HcpOpenShiftCluster{
			Properties: &generated.HcpOpenShiftClusterProperties{
				Etcd: &generated.EtcdProfile{
					DataEncryption: &generated.EtcdDataEncryptionProfile{
						KeyManagementMode: &platformManaged,
						// CustomerManaged intentionally nil — switching to platform-managed
					},
				},
			},
		},
	}

	ext.ZeroOwnedFields(internal)
	err := ext.ApplyOwnedFields(internal)
	require.NoError(t, err)

	require.Equal(t, api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
		internal.CustomerProperties.Etcd.DataEncryption.KeyManagementMode,
		"KeyManagementMode should be PlatformManaged")
	require.Nil(t, internal.CustomerProperties.Etcd.DataEncryption.CustomerManaged,
		"CustomerManaged must be nil after switching to PlatformManaged")
}

func newClusterWithAllFields() *api.HCPOpenShiftCluster {
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   api.Must(azcorearm.ParseResourceID(strings.ToLower("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/myCluster"))),
				Name: "myCluster",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
				SystemData: &arm.SystemData{
					CreatedBy: "test-user",
				},
			},
			Location: "eastus",
			Tags:     map[string]string{"env": "test"},
		},
		Identity: &arm.ManagedServiceIdentity{
			Type: arm.ManagedServiceIdentityTypeUserAssigned,
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           "4.15.1",
				ChannelGroup: "stable",
			},
			DNS: api.CustomerDNSProfile{
				BaseDomainPrefix: "myprefix",
			},
			Network: api.NetworkProfile{
				NetworkType: api.NetworkTypeOVNKubernetes,
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
				HostPrefix:  23,
			},
			API: api.CustomerAPIProfile{
				Visibility: api.VisibilityPublic,
			},
			Platform: api.CustomerPlatformProfile{
				ManagedResourceGroup:    "my-mrg",
				SubnetID:                api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet")),
				VnetIntegrationSubnetID: api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/swift-subnet")),
				OutboundType:            api.OutboundTypeLoadBalancer,
				NetworkSecurityGroupID:  api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.Network/networkSecurityGroups/nsg")),
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:               100,
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 900,
				PodPriorityThreshold:        -10,
			},
			NodeDrainTimeoutMinutes: 30,
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: "aes-cbc",
						Kms: &api.KmsEncryptionProfile{
							Visibility: api.KeyVaultVisibilityPrivate,
							ActiveKey: api.KmsKey{
								Name:      "my-key",
								VaultName: "my-vault",
								Version:   "1",
							},
						},
					},
				},
			},
			ClusterImageRegistry: api.ClusterImageRegistryProfile{
				State: api.ClusterImageRegistryStateEnabled,
			},
			ImageDigestMirrors: []api.ImageDigestMirror{
				{
					Source:  "quay.io/example",
					Mirrors: []string{"mirror.example.com/example"},
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState:                     arm.ProvisioningStateSucceeded,
			ClusterServiceID:                      api.Must(api.NewInternalID(ocm.GenerateClusterHREF("testCluster"))),
			ActiveOperationID:                     "op-123",
			ManagedIdentitiesDataPlaneIdentityURL: "https://dummyhost.identity.azure.net",
			DNS: api.ServiceProviderDNSProfile{
				BaseDomain: "example.aroapp.io",
			},
		},
	}
}
