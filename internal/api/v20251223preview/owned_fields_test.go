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

package v20251223preview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// TestV2025ZeroOwnedFields_ZeroesAllCustomerProperties verifies that
// v2025 ZeroOwnedFields zeroes ALL customer properties. v2025 is the
// newest version and owns every customer-visible field.
func TestV2025ZeroOwnedFields_ZeroesAllCustomerProperties(t *testing.T) {
	internal := newFullyPopulatedCluster()

	ext := &HcpOpenShiftCluster{}
	ext.ZeroOwnedFields(internal)

	// All customer properties must be zeroed.
	require.Equal(t, api.VersionProfile{}, internal.CustomerProperties.Version,
		"Version should be zeroed")
	require.Equal(t, api.CustomerDNSProfile{}, internal.CustomerProperties.DNS,
		"DNS should be zeroed")
	require.Equal(t, api.NetworkProfile{}, internal.CustomerProperties.Network,
		"Network should be zeroed")
	require.Equal(t, api.CustomerAPIProfile{}, internal.CustomerProperties.API,
		"API should be zeroed")
	require.Equal(t, api.CustomerPlatformProfile{}, internal.CustomerProperties.Platform,
		"Platform should be zeroed")
	require.Equal(t, api.ClusterAutoscalingProfile{}, internal.CustomerProperties.Autoscaling,
		"Autoscaling should be zeroed")
	require.Equal(t, int32(0), internal.CustomerProperties.NodeDrainTimeoutMinutes,
		"NodeDrainTimeoutMinutes should be zeroed")
	require.Equal(t, api.EtcdProfile{}, internal.CustomerProperties.Etcd,
		"Etcd should be zeroed")
	require.Equal(t, api.ClusterImageRegistryProfile{}, internal.CustomerProperties.ClusterImageRegistry,
		"ClusterImageRegistry should be zeroed")
	require.Nil(t, internal.CustomerProperties.ImageDigestMirrors,
		"ImageDigestMirrors should be zeroed (nil)")
}

// TestV2025ZeroOwnedFields_PreservesServiceProviderProperties verifies that
// ZeroOwnedFields does NOT touch ServiceProviderProperties. Those fields are
// managed separately and must never be zeroed by any API version.
func TestV2025ZeroOwnedFields_PreservesServiceProviderProperties(t *testing.T) {
	internal := newFullyPopulatedCluster()

	// Capture the original ServiceProviderProperties.
	origSPP := internal.ServiceProviderProperties

	ext := &HcpOpenShiftCluster{}
	ext.ZeroOwnedFields(internal)

	require.Equal(t, origSPP.ProvisioningState, internal.ServiceProviderProperties.ProvisioningState,
		"ProvisioningState must survive ZeroOwnedFields")
	require.Equal(t, origSPP.ClusterServiceID.String(), internal.ServiceProviderProperties.ClusterServiceID.String(),
		"ClusterServiceID must survive ZeroOwnedFields")
	require.Equal(t, origSPP.ActiveOperationID, internal.ServiceProviderProperties.ActiveOperationID,
		"ActiveOperationID must survive ZeroOwnedFields")
	require.Equal(t, origSPP.ManagedIdentitiesDataPlaneIdentityURL, internal.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		"ManagedIdentitiesDataPlaneIdentityURL must survive ZeroOwnedFields")
	require.Equal(t, origSPP.DNS.BaseDomain, internal.ServiceProviderProperties.DNS.BaseDomain,
		"ServiceProviderProperties.DNS must survive ZeroOwnedFields")
}

// TestV2025ZeroOwnedFields_ZeroesARMMetadata verifies that ARM resource
// metadata fields (ID, Name, Type, Location, Tags, Identity, SystemData)
// are zeroed. These are owned by all API versions.
func TestV2025ZeroOwnedFields_ZeroesARMMetadata(t *testing.T) {
	internal := newFullyPopulatedCluster()

	ext := &HcpOpenShiftCluster{}
	ext.ZeroOwnedFields(internal)

	require.Nil(t, internal.ID, "ID should be zeroed")
	require.Empty(t, internal.Name, "Name should be zeroed")
	require.Empty(t, internal.Type, "Type should be zeroed")
	require.Empty(t, internal.Location, "Location should be zeroed")
	require.Nil(t, internal.Tags, "Tags should be zeroed")
	require.Nil(t, internal.Identity, "Identity should be zeroed")
	require.Nil(t, internal.SystemData, "SystemData should be zeroed")
}

func newFullyPopulatedCluster() *api.HCPOpenShiftCluster {
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
