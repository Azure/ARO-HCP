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

package validation

import (
	"context"
	"path"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var (
	managedIdentity1 = api.NewTestUserAssignedIdentity("myManagedIdentity1")
	managedIdentity2 = api.NewTestUserAssignedIdentity("myManagedIdentity2")
	managedIdentity3 = api.NewTestUserAssignedIdentity("myManagedIdentity3")
)

func TestClusterRequired(t *testing.T) {
	tests := []struct {
		name         string
		resource     *api.HCPOpenShiftCluster
		tweaks       *api.HCPOpenShiftCluster
		expectErrors []expectedError
	}{
		{
			name:     "Empty cluster",
			resource: &api.HCPOpenShiftCluster{},
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "trackedResource.resource.id",
				},
				{
					message:   "Required value",
					fieldPath: "trackedResource.location",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.version.channelGroup",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.version.id",
				},
				{
					message:   "Unsupported value",
					fieldPath: "customerProperties.network.networkType",
				},
				{
					message:   "must be greater than or equal to 23",
					fieldPath: "customerProperties.network.hostPrefix",
				},
				{
					message:   "Unsupported value",
					fieldPath: "customerProperties.api.visiblity",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.platform.subnetId",
				},
				{
					message:   "Unsupported value",
					fieldPath: "customerProperties.platform.outboundType",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.platform.networkSecurityGroupId",
				},
				{
					message:   "Unsupported value",
					fieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode",
				},
				{
					message:   "Unsupported value",
					fieldPath: "customerProperties.clusterImageRegistry.state",
				},
				{
					message:   "Invalid value: 0: must be greater than or equal to 1",
					fieldPath: "customerProperties.autoscaling.maxPodGracePeriodSeconds",
				},
				{
					message:   "Invalid value: 0: must be greater than or equal to 1",
					fieldPath: "customerProperties.autoscaling.maxNodeProvisionTimeSeconds",
				},
			},
		},
		{
			name: "Default cluster",
			resource: api.NewDefaultHCPOpenShiftCluster(
				api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster")),
				api.TestLocation,
			),
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "customerProperties.platform.subnetId",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.platform.networkSecurityGroupId",
				},
			},
		},
		{
			name:     "Minimum valid cluster",
			resource: api.MinimumValidClusterTestCase(),
		},
		{
			name: "Cluster with identity",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": api.NewTestUserAssignedIdentity("MyManagedIdentity"),
								},
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						api.NewTestUserAssignedIdentity("MyManagedIdentity"): {},
					},
				},
			},
		},
		{
			name: "Cluster with broken identity",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": "wrong/Pattern/Of/ResourceIDString",
								},
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"wrong/Pattern/Of/ResourceIDString": {},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceIDString' must start with '/'",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceIDString' must start with '/'",
					fieldPath: "identity.userAssignedIdentities",
				},
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceIDString' must start with '/'",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceIDString' must start with '/'",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := tt.resource
			if resource == nil {
				resource = api.ClusterTestCase(t, tt.tweaks)
			}

			actualErrors := ValidateClusterCreate(context.TODO(), resource, nil)
			verifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}

func TestClusterValidate(t *testing.T) {
	// Note "required" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *api.HCPOpenShiftCluster
		expectErrors []expectedError
	}{
		{
			name:   "Minimum valid cluster",
			tweaks: &api.HCPOpenShiftCluster{},
		},
		{
			name: "Bad cidrv4",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						PodCIDR: "Mmm... apple cider",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "invalid CIDR address",
					fieldPath: "customerProperties.network.podCidr",
				},
			},
		},
		{
			name: "Bad dns_rfc1035_label",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					DNS: api.CustomerDNSProfile{
						BaseDomainPrefix: "0badlabel",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be a valid DNS RFC 1035 label",
					fieldPath: "customerProperties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Bad enum_outboundtype",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OutboundType: "loadJuggler",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"LoadBalancer\"",
					fieldPath: "customerProperties.platform.outboundType",
				},
			},
		},
		{
			name: "Bad required_unless",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ChannelGroup: "fast",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "customerProperties.version.id",
				},
			},
		},
		{
			name: "Bad enum_visibility",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						Visibility: "it's a secret to everybody",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"Private\", \"Public\"",
					fieldPath: "customerProperties.api.visiblity",
				},
			},
		},
		{
			name: "Bad enum_managedserviceidentitytype",
			tweaks: &api.HCPOpenShiftCluster{
				Identity: &arm.ManagedServiceIdentity{
					Type: "brokenServiceType",
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"None\", \"SystemAssigned\", \"SystemAssigned,UserAssigned\", \"UserAssigned\"",
					fieldPath: "identity.state",
				},
			},
		},
		{
			name: "Bad enum_clusterimageregistryprofilestate",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					ClusterImageRegistry: api.ClusterImageRegistryProfile{
						State: api.ClusterImageRegistryProfileState("not enabled"),
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"Disabled\", \"Enabled\"",
					fieldPath: "customerProperties.clusterImageRegistry.state",
				},
			},
		},
		{
			name: "Base domain prefix is too long",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					DNS: api.CustomerDNSProfile{
						BaseDomainPrefix: "this-domain-is-too-long",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "may not be more than 15 bytes",
					fieldPath: "customerProperties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Host prefix is too small",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						HostPrefix: 22,
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be greater than or equal to 23",
					fieldPath: "customerProperties.network.hostPrefix",
				},
			},
		},
		{
			name: "Host prefix is too large",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						HostPrefix: 27,
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be less than or equal to 26",
					fieldPath: "customerProperties.network.hostPrefix",
				},
			},
		},
		{
			name: "Control plane operator name cannot be empty",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"": managedIdentity1,
								},
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators",
				},
				{
					message:   "identity is not assigned to this resource",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[]",
				},
			},
		},

		{
			name: "Data plane operator name cannot be empty",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]string{
									"": managedIdentity1,
								},
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators",
				},
			},
		},
		{
			name: "Customer managed ETCD key management mode requires CustomerManaged fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be specified when `keyManagementMode` is \"CustomerManaged\"",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged",
				},
			},
		},
		{
			name: "Platform managed ETCD key management mode excludes CustomerManaged fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
							CustomerManaged:   &api.CustomerManagedEncryptionProfile{},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "may only be specified when `keyManagementMode` is \"CustomerManaged\"",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged",
				},
				{
					message:   "supported values: \"KMS\"",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.encryptionType",
				},
			},
		},
		{
			name: "Customer managed Key Management Service (KMS) requires Kms fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: api.CustomerManagedEncryptionTypeKMS,
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be specified when `encryptionType` is \"KMS\"",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms",
				},
			},
		},
		{
			// FIXME Use a valid alternate EncryptionType once we have one.
			name: "Alternate customer managed ETCD encyption type excludes Kms fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: "Alternate",
								Kms:            &api.KmsEncryptionProfile{},
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"KMS\"",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.encryptionType",
				},
				{
					message:   "may only be specified when `encryptionType` is \"KMS\"",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.activeKey.name",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.activeKey.vaultName",
				},
				{
					message:   "Required value",
					fieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.activeKey.version",
				},
			},
		},

		//--------------------------------
		// Complex multi-field validation
		//--------------------------------

		{
			name: "Cluster with overlapping machine and service CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						ServiceCIDR: "10.0.0.0/23",
						MachineCIDR: "10.0.0.0/16",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "machine CIDR '10.0.0.0/16' and service CIDR '10.0.0.0/23' overlap",
					fieldPath: "customerProperties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping machine and pod CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						PodCIDR:     "10.1.0.0/18",
						MachineCIDR: "10.1.0.0/23",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "machine CIDR '10.1.0.0/23' and pod CIDR '10.1.0.0/18' overlap",
					fieldPath: "customerProperties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping service and pod CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						PodCIDR:     "10.2.0.0/18",
						ServiceCIDR: "10.2.0.0/24",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "service CIDR '10.2.0.0/24' and pod CIDR '10.2.0.0/18' overlap",
					fieldPath: "customerProperties.network",
				},
			},
		},
		{
			name: "Cluster with invalid managed resource group",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: api.TestResourceGroupName,
						// Use a different resource group name to avoid a subnet ID error.
						SubnetID: path.Join("/subscriptions", api.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", api.TestVirtualNetworkName, "subnets", api.TestSubnetName),
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must not be the same resource group name",
					fieldPath: "customerProperties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Cluster with invalid subnet ID",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: "MRG",
						SubnetID:             "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must not be the same resource group name: \"MRG\"",
					fieldPath: "customerProperties.platform.subnetId",
				},
				{
					message:   "must be in the same Azure subscription: \"11111111-1111-1111-1111-111111111111\"",
					fieldPath: "customerProperties.platform.subnetId",
				},
				{
					message:   "must not be the same resource group name: \"MRG\"",
					fieldPath: "customerProperties.platform.subnetId",
				},
			},
		},
		{
			name: "Cluster with differently-cased identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": strings.ToLower(managedIdentity1),
								},
								ServiceManagedIdentity: strings.ToLower(managedIdentity2),
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						strings.ToUpper(managedIdentity1): {},
						strings.ToUpper(managedIdentity2): {},
					},
				},
			},
		},
		{
			name: "Cluster with broken identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity2,
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity3: {},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "identity is not assigned to this resource",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					message:   "identity is assigned to this resource but not used",
					fieldPath: "identity.userAssignedIdentities",
				},
				{
					message:   "identity is not assigned to this resource",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name: "Cluster with multiple identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
									"operatorY": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity1,
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1: {},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "identity is used multiple times",
					fieldPath: "identity.userAssignedIdentities",
				},
			},
		},
		{
			name: "Cluster with invalid data plane operator identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
								},
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1: {},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "identity is assigned to this resource but not used",
					fieldPath: "identity.userAssignedIdentities",
				},
				{
					message:   "cannot use identity assigned to this resource by .identities.userAssignedIdentities",
					fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[operatorX]",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := api.ClusterTestCase(t, tt.tweaks)

			actualErrors := ValidateClusterCreate(context.TODO(), resource, nil)
			verifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}
