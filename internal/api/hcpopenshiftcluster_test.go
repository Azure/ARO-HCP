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

package api

import (
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var (
	managedIdentity1 = NewTestUserAssignedIdentity("myManagedIdentity1")
	managedIdentity2 = NewTestUserAssignedIdentity("myManagedIdentity2")
	managedIdentity3 = NewTestUserAssignedIdentity("myManagedIdentity3")
)

func compareErrors(x, y []arm.CloudErrorBody) string {
	return cmp.Diff(x, y,
		cmpopts.SortSlices(func(x, y arm.CloudErrorBody) bool { return x.Target < y.Target }),
		cmpopts.IgnoreFields(arm.CloudErrorBody{}, "Code"))
}

func TestClusterRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		resource     *HCPOpenShiftCluster
		tweaks       *HCPOpenShiftCluster
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:     "Empty cluster",
			resource: &HCPOpenShiftCluster{},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'properties'",
					Target:  "properties",
				},
			},
		},
		{
			name:     "Default cluster",
			resource: NewDefaultHCPOpenShiftCluster(),
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'subnetId'",
					Target:  "properties.platform.subnetId",
				},
				{
					Message: "Missing required field 'networkSecurityGroupId'",
					Target:  "properties.platform.networkSecurityGroupId",
				},
			},
		},
		{
			name:     "Minimum valid cluster",
			resource: MinimumValidClusterTestCase(),
		},
		{
			name: "Cluster with identity",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": NewTestUserAssignedIdentity("MyManagedIdentity"),
								},
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						NewTestUserAssignedIdentity("MyManagedIdentity"): &arm.UserAssignedIdentity{},
					},
				},
			},
		},
		{
			name: "Cluster with broken identity",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": "wrong/Pattern/Of/ResourceID",
								},
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"wrong/Pattern/Of/ResourceID": &arm.UserAssignedIdentity{},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'wrong/Pattern/Of/ResourceID' for field 'controlPlaneOperators[operatorX]' (must be a valid 'Microsoft.ManagedIdentity/userAssignedIdentities' resource ID)",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
					Details: nil,
				},
				{
					Message: "Invalid value 'wrong/Pattern/Of/ResourceID' for field 'userAssignedIdentities[wrong/Pattern/Of/ResourceID]' (must be a valid 'Microsoft.ManagedIdentity/userAssignedIdentities' resource ID)",
					Target:  "identity.userAssignedIdentities[wrong/Pattern/Of/ResourceID]",
				},
			},
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPut, "localhost", nil)
			require.NoError(t, err)

			resource := tt.resource
			if resource == nil {
				resource = ClusterTestCase(t, tt.tweaks)
			}

			actualErrors := ValidateRequest(validate, request, resource)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}

func TestClusterValidate(t *testing.T) {
	// Note "required_for_put" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *HCPOpenShiftCluster
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:   "Minimum valid cluster",
			tweaks: &HCPOpenShiftCluster{},
		},
		{
			name: "Bad cidrv4",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Network: NetworkProfile{
						PodCIDR: "Mmm... apple cider",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'Mmm... apple cider' for field 'podCidr' (must be a v4 CIDR range)",
					Target:  "properties.network.podCidr",
				},
			},
		},
		{
			name: "Bad dns_rfc1035_label",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					DNS: DNSProfile{
						BaseDomainPrefix: "0badlabel",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '0badlabel' for field 'baseDomainPrefix' (must be a valid DNS RFC 1035 label)",
					Target:  "properties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Bad openshift_version",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Version: VersionProfile{
						ID: "bad.version",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid OpenShift version 'bad.version'",
					Target:  "properties.version.id",
				},
			},
		},
		{
			name: "Bad enum_outboundtype",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OutboundType: "loadJuggler",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'loadJuggler' for field 'outboundType' (must be loadBalancer)",
					Target:  "properties.platform.outboundType",
				},
			},
		},
		{
			name: "Bad enum_visibility",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					API: APIProfile{
						Visibility: "it's a secret to everybody",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'it's a secret to everybody' for field 'visibility' (must be one of: public private)",
					Target:  "properties.api.visibility",
				},
			},
		},
		{
			name: "Bad enum_managedserviceidentitytype",
			tweaks: &HCPOpenShiftCluster{
				Identity: arm.ManagedServiceIdentity{
					Type: "brokenServiceType",
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'brokenServiceType' for field 'type' (must be one of: None SystemAssigned SystemAssigned,UserAssigned UserAssigned)",
					Target:  "identity.type",
				},
			},
		},
		{
			name: "Base domain prefix is too long",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					DNS: DNSProfile{
						BaseDomainPrefix: "this-domain-is-too-long",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'this-domain-is-too-long' for field 'baseDomainPrefix' (maximum length is 15)",
					Target:  "properties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Host prefix is too small",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Network: NetworkProfile{
						HostPrefix: 22,
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '22' for field 'hostPrefix' (must be at least 23)",
					Target:  "properties.network.hostPrefix",
				},
			},
		},
		{
			name: "Host prefix is too large",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Network: NetworkProfile{
						HostPrefix: 27,
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '27' for field 'hostPrefix' (must be at most 26)",
					Target:  "properties.network.hostPrefix",
				},
			},
		},
		{
			name: "Bad required_unless",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Version: VersionProfile{
						ChannelGroup: "fast",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Field 'id' is required when 'channelGroup' is not 'stable'",
					Target:  "properties.version.id",
				},
			},
		},
		{
			name: "Control plane operator name cannot be empty",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"": managedIdentity1,
								},
							},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'controlPlaneOperators[]'",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[]",
				},
			},
		},

		{
			name: "Data plane operator name cannot be empty",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]string{
									"": managedIdentity1,
								},
							},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'dataPlaneOperators[]'",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[]",
				},
			},
		},
		//--------------------------------
		// Complex multi-field validation
		//--------------------------------

		{
			name: "Cluster with invalid channel group",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Version: VersionProfile{
						ID:           "openshift-v4.99.0",
						ChannelGroup: "freshmeat",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Channel group must be 'stable'",
					Target:  "properties.version.channelGroup",
				},
			},
		},
		{
			name: "Cluster with overlapping machine and service CIDRs",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Network: NetworkProfile{
						ServiceCIDR: "10.0.0.0/23",
						MachineCIDR: "10.0.0.0/16",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Machine CIDR '10.0.0.0/16' and service CIDR '10.0.0.0/23' overlap",
					Target:  "properties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping machine and pod CIDRs",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Network: NetworkProfile{
						PodCIDR:     "10.1.0.0/18",
						MachineCIDR: "10.1.0.0/23",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Machine CIDR '10.1.0.0/23' and pod CIDR '10.1.0.0/18' overlap",
					Target:  "properties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping service and pod CIDRs",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Network: NetworkProfile{
						PodCIDR:     "10.2.0.0/18",
						ServiceCIDR: "10.2.0.0/24",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Service CIDR '10.2.0.0/24' and pod CIDR '10.2.0.0/18' overlap",
					Target:  "properties.network",
				},
			},
		},
		{
			name: "Cluster with invalid managed resource group",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						ManagedResourceGroup: TestResourceGroupName,
						// Use a different resource group name to avoid a subnet ID error.
						SubnetID: path.Join("/subscriptions", TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", TestVirtualNetworkName, "subnets", TestSubnetName),
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Managed resource group name must not be the cluster's resource group name",
					Target:  "properties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Cluster with invalid subnet ID",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						ManagedResourceGroup: "MRG",
						SubnetID:             "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Subnet '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet' must be in the same Azure subscription as the cluster",
					Target:  "properties.platform.subnetId",
				},
				{
					Message: "Subnet '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet' cannot be in the managed resource group 'MRG'",
					Target:  "properties.platform.subnetId",
				},
			},
		},
		{
			name: "Cluster with differently-cased identities",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": strings.ToLower(managedIdentity1),
								},
								ServiceManagedIdentity: strings.ToLower(managedIdentity2),
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						strings.ToUpper(managedIdentity1): &arm.UserAssignedIdentity{},
						strings.ToUpper(managedIdentity2): &arm.UserAssignedIdentity{},
					},
				},
			},
		},
		{
			name: "Cluster with broken identities",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity2,
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity3: &arm.UserAssignedIdentity{},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Identity '" + managedIdentity1 + "' is not assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					Message: "Identity '" + managedIdentity3 + "' is assigned to this resource but not used",
					Target:  "identity.userAssignedIdentities",
				},
				{
					Message: "Identity '" + managedIdentity2 + "' is not assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name: "Cluster with multiple identities",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
									"operatorY": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity1,
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1: &arm.UserAssignedIdentity{},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Identity '" + managedIdentity1 + "' is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities",
				},
			},
		},
		{
			name: "Cluster with invalid data plane operator identities",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Platform: PlatformProfile{
						OperatorsAuthentication: OperatorsAuthenticationProfile{
							UserAssignedIdentities: UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
								},
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1: &arm.UserAssignedIdentity{},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Identity '" + managedIdentity1 + "' is assigned to this resource but not used",
					Target:  "identity.userAssignedIdentities",
				},
				{
					Message: "Data plane operator 'operatorX' cannot use identity assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[operatorX]",
				},
			},
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPut, TestClusterResourceID, nil)
			require.NoError(t, err)

			resource := ClusterTestCase(t, tt.tweaks)

			actualErrors := resource.Validate(validate, request)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}

			for _, e := range actualErrors {
				AssertJSONPath[HCPOpenShiftCluster](t, e.Target)
			}
		})
	}
}
