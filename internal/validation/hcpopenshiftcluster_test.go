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

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var (
	managedIdentity1 = api.NewTestUserAssignedIdentity("myManagedIdentity1")
	managedIdentity2 = api.NewTestUserAssignedIdentity("myManagedIdentity2")
	managedIdentity3 = api.NewTestUserAssignedIdentity("myManagedIdentity3")
)

// expectedError is defined in validate_cluster_test.go

func clusterContainsError(errs field.ErrorList, expectedErr expectedError) bool {
	for _, err := range errs {
		fieldMatches := strings.Contains(err.Field, expectedErr.fieldPath)
		messageMatches := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)

		if fieldMatches && messageMatches {
			return true
		}
	}
	return false
}

func TestClusterRequired(t *testing.T) {
	arm.SetAzureLocation(api.TestLocation)

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
					fieldPath: "trackedResource.location",
				},
				{
					message:   "Required value",
					fieldPath: "properties.version.id",
				},
				{
					message:   "Required value",
					fieldPath: "properties.version.channelGroup",
				},
				{
					message:   "Unsupported value",
					fieldPath: "properties.version.channelGroup",
				},
				{
					message:   "Unsupported value",
					fieldPath: "properties.network.networkType",
				},
				{
					message:   "must be greater than or equal to 23",
					fieldPath: "properties.network.hostPrefix",
				},
				{
					message:   "Unsupported value",
					fieldPath: "properties.api.visiblity",
				},
				{
					message:   "Required value",
					fieldPath: "properties.platform.subnetId",
				},
				{
					message:   "Unsupported value",
					fieldPath: "properties.platform.outboundType",
				},
				{
					message:   "Required value",
					fieldPath: "properties.platform.networkSecurityGroupId",
				},
				{
					message:   "Unsupported value",
					fieldPath: "properties.etcd.dataEncryption.keyManagementMode",
				},
				{
					message:   "Unsupported value",
					fieldPath: "properties.clusterImageRegistry.state",
				},
			},
		},
		{
			name:     "Default cluster",
			resource: api.NewDefaultHCPOpenShiftCluster(nil),
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "properties.platform.subnetId",
				},
				{
					message:   "Required value",
					fieldPath: "properties.platform.networkSecurityGroupId",
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
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": "wrong/Pattern/Of/ResourceID",
								},
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"wrong/Pattern/Of/ResourceID": {},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceID' must start with '/'",
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceID' must start with '/'",
					fieldPath: "identity.userAssignedIdentities",
				},
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceID' must start with '/'",
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					message:   "resource id 'wrong/Pattern/Of/ResourceID' must start with '/'",
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
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

			actualErrors := ValidateClusterCreate(context.TODO(), resource)

			if len(tt.expectErrors) == 0 && len(actualErrors) > 0 {
				t.Errorf("expected no errors but got: %v", actualErrors)
				return
			}

			for _, expectedErr := range tt.expectErrors {
				if !clusterContainsError(actualErrors, expectedErr) {
					t.Errorf("expected error %+v not found in %v", expectedErr, actualErrors)
				}
			}

			if len(actualErrors) != len(tt.expectErrors) {
				t.Errorf("expected %d errors, got %d: %v", len(tt.expectErrors), len(actualErrors), actualErrors)
			}
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
				Properties: api.HCPOpenShiftClusterProperties{
					Network: api.NetworkProfile{
						PodCIDR: "Mmm... apple cider",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "invalid CIDR address",
					fieldPath: "properties.network.podCidr",
				},
			},
		},
		{
			name: "Bad dns_rfc1035_label",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					DNS: api.DNSProfile{
						BaseDomainPrefix: "0badlabel",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be a valid DNS RFC 1035 label",
					fieldPath: "properties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Bad openshift_version",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Version: api.VersionProfile{
						ID: "bad.version",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "Malformed version",
					fieldPath: "properties.version.id",
				},
			},
		},
		{
			name: "Version cannot be MAJOR.MINOR.PATCH",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Version: api.VersionProfile{
						ID: "4.18.1",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be specified as MAJOR.MINOR; the PATCH value is managed",
					fieldPath: "properties.version.id",
				},
			},
		},
		{
			name: "Bad enum_outboundtype",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						OutboundType: "loadJuggler",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"LoadBalancer\"",
					fieldPath: "properties.platform.outboundType",
				},
			},
		},
		{
			name: "Bad enum_visibility",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					API: api.APIProfile{
						Visibility: "it's a secret to everybody",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"Private\", \"Public\"",
					fieldPath: "properties.api.visiblity",
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
				Properties: api.HCPOpenShiftClusterProperties{
					ClusterImageRegistry: api.ClusterImageRegistryProfile{
						State: api.ClusterImageRegistryProfileState("not enabled"),
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"Disabled\", \"Enabled\"",
					fieldPath: "properties.clusterImageRegistry.state",
				},
			},
		},
		{
			name: "Base domain prefix is too long",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					DNS: api.DNSProfile{
						BaseDomainPrefix: "this-domain-is-too-long",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "may not be more than 15 bytes",
					fieldPath: "properties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Host prefix is too small",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Network: api.NetworkProfile{
						HostPrefix: 22,
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be greater than or equal to 23",
					fieldPath: "properties.network.hostPrefix",
				},
			},
		},
		{
			name: "Host prefix is too large",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Network: api.NetworkProfile{
						HostPrefix: 27,
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be less than or equal to 26",
					fieldPath: "properties.network.hostPrefix",
				},
			},
		},
		{
			name: "Bad required_unless",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Version: api.VersionProfile{
						ChannelGroup: "fast",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "properties.version.id",
				},
				{
					message:   "supported values: \"stable\"",
					fieldPath: "properties.version.channelGroup",
				},
			},
		},
		{
			name: "Control plane operator name cannot be empty",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators",
				},
				{
					message:   "identity is not assigned to this resource",
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[]",
				},
			},
		},

		{
			name: "Data plane operator name cannot be empty",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators",
				},
			},
		},
		{
			name: "Customer managed ETCD key management mode requires CustomerManaged fields",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
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
					fieldPath: "properties.etcd.dataEncryption.customerManaged",
				},
			},
		},
		{
			name: "Platform managed ETCD key management mode excludes CustomerManaged fields",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
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
					fieldPath: "properties.etcd.dataEncryption.customerManaged",
				},
				{
					message:   "supported values: \"KMS\"",
					fieldPath: "properties.etcd.dataEncryption.customerManaged.encryptionType",
				},
			},
		},
		{
			name: "Customer managed Key Management Service (KMS) requires Kms fields",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
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
					fieldPath: "properties.etcd.dataEncryption.customerManaged.kms",
				},
			},
		},
		{
			// FIXME Use a valid alternate EncryptionType once we have one.
			name: "Alternate customer managed ETCD encyption type excludes Kms fields",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
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
					fieldPath: "properties.etcd.dataEncryption.customerManaged.encryptionType",
				},
				{
					message:   "may only be specified when `encryptionType` is \"KMS\"",
					fieldPath: "properties.etcd.dataEncryption.customerManaged.kms",
				},
				{
					message:   "Required value",
					fieldPath: "properties.etcd.dataEncryption.customerManaged.kms.activeKey.name",
				},
				{
					message:   "Required value",
					fieldPath: "properties.etcd.dataEncryption.customerManaged.kms.activeKey.vaultName",
				},
				{
					message:   "Required value",
					fieldPath: "properties.etcd.dataEncryption.customerManaged.kms.activeKey.version",
				},
			},
		},

		//--------------------------------
		// Complex multi-field validation
		//--------------------------------

		{
			name: "Cluster with invalid channel group",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Version: api.VersionProfile{
						ID:           "4.99",
						ChannelGroup: "freshmeat",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "supported values: \"stable\"",
					fieldPath: "properties.version.channelGroup",
				},
			},
		},
		{
			name: "Cluster with overlapping machine and service CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Network: api.NetworkProfile{
						ServiceCIDR: "10.0.0.0/23",
						MachineCIDR: "10.0.0.0/16",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "machine CIDR '10.0.0.0/16' and service CIDR '10.0.0.0/23' overlap",
					fieldPath: "properties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping machine and pod CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Network: api.NetworkProfile{
						PodCIDR:     "10.1.0.0/18",
						MachineCIDR: "10.1.0.0/23",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "machine CIDR '10.1.0.0/23' and pod CIDR '10.1.0.0/18' overlap",
					fieldPath: "properties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping service and pod CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Network: api.NetworkProfile{
						PodCIDR:     "10.2.0.0/18",
						ServiceCIDR: "10.2.0.0/24",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "service CIDR '10.2.0.0/24' and pod CIDR '10.2.0.0/18' overlap",
					fieldPath: "properties.network",
				},
			},
		},
		{
			name: "Cluster with invalid managed resource group",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						ManagedResourceGroup: api.TestResourceGroupName,
						// Use a different resource group name to avoid a subnet ID error.
						SubnetID: path.Join("/subscriptions", api.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", api.TestVirtualNetworkName, "subnets", api.TestSubnetName),
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must not be the same resource group name",
					fieldPath: "properties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Cluster with invalid subnet ID",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						ManagedResourceGroup: "MRG",
						SubnetID:             "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must not be the same resource group name: \"MRG\"",
					fieldPath: "properties.platform.subnetId",
				},
				{
					message:   "must be in the same Azure subscription: \"11111111-1111-1111-1111-111111111111\"",
					fieldPath: "properties.platform.subnetId",
				},
				{
					message:   "must not be the same resource group name: \"MRG\"",
					fieldPath: "properties.platform.subnetId",
				},
			},
		},
		{
			name: "Cluster with differently-cased identities",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					message:   "identity is assigned to this resource but not used",
					fieldPath: "identity.userAssignedIdentities",
				},
				{
					message:   "identity is not assigned to this resource",
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name: "Cluster with multiple identities",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
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
					fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[operatorX]",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := api.ClusterTestCase(t, tt.tweaks)

			actualErrors := ValidateClusterCreate(context.TODO(), resource)

			if len(tt.expectErrors) == 0 && len(actualErrors) > 0 {
				t.Errorf("expected no errors but got: %v", actualErrors)
				return
			}

			for _, expectedErr := range tt.expectErrors {
				if !clusterContainsError(actualErrors, expectedErr) {
					t.Errorf("expected error %+v not found in %v", expectedErr, actualErrors)
				}
			}

			if len(actualErrors) != len(tt.expectErrors) {
				t.Errorf("expected %d errors, got %d: %v", len(tt.expectErrors), len(actualErrors), actualErrors)
			}
		})
	}
}
