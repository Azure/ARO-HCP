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
	"fmt"
	"testing"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Comprehensive tests for ValidateClusterCreate
// using a subscription without AllowNonStableChannel flags
func TestValidateClusterCreate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		cluster      *api.HCPOpenShiftCluster
		expectErrors []expectedError
	}{
		{
			name:         "valid cluster - create",
			cluster:      createValidCluster(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster with identity - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// The helper already sets up a valid identity, so just return it
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "invalid DNS prefix - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.DNS.BaseDomainPrefix = "Invalid-Name"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "customerProperties.dns.baseDomainPrefix"},
			},
		},
		{
			name: "invalid network type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.NetworkType = "InvalidType"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "customerProperties.network.networkType"},
			},
		},
		{
			name: "invalid Pod CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.PodCIDR = "invalid-cidr"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.network.podCidr"},
			},
		},
		{
			name: "invalid Service CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.ServiceCIDR = "300.0.0.0/16"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.network.serviceCidr"},
			},
		},
		{
			name: "invalid Machine CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.MachineCIDR = "2001:db8::/32"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "not IPv4", fieldPath: "customerProperties.network.machineCidr"},
			},
		},
		{
			name: "host prefix too small - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.HostPrefix = 22
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 23", fieldPath: "customerProperties.network.hostPrefix"},
			},
		},
		{
			name: "host prefix too large - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.HostPrefix = 27
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 26", fieldPath: "customerProperties.network.hostPrefix"},
			},
		},
		{
			name: "invalid API visibility - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.Visibility = "InvalidVisibility"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "customerProperties.api.visiblity"},
			},
		},
		{
			name: "invalid authorized CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"invalid-cidr"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "empty authorized CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{""}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "empty list authorized CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must have at least 1 items", fieldPath: "customerProperties.api.authorizedCidrs"},
			},
		},
		{
			name: "authorized CIDR with leading whitespace - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{" 10.0.0.0/16"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must not contain extra whitespace", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "authorized CIDR with trailing whitespace - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16 "}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must not contain extra whitespace", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "authorized CIDR with internal whitespace - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0. 0.0/16"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "valid IPv4 address in authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"192.168.1.1"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid CIDR ranges in authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "IPv6 address in authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"2001:db8::1"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not IPv4", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "IPv6 CIDR in authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"2001:db8::/32"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not IPv4", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "invalid CIDR prefix in authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/33"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "multiple validation errors in authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"", "invalid-cidr", " 10.0.0.0/16", "2001:db8::1"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[1]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[1]"},
				{message: "must not contain extra whitespace", fieldPath: "customerProperties.api.authorizedCidrs[2]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[2]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[2]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[3]"},
				{message: "not IPv4", fieldPath: "customerProperties.api.authorizedCidrs[3]"},
			},
		},
		{
			name: "501 unique authorized CIDR blocks - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = makeUniqueCIDRs(501)
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must have at most 500 items", fieldPath: "customerProperties.api.authorizedCidrs"},
			},
		},
		{
			name: "missing subnet ID - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.SubnetID = ""
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "customerProperties.platform.subnetId"},
			},
		},
		{
			name: "invalid outbound type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.OutboundType = "InvalidType"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "customerProperties.platform.outboundType"},
			},
		},
		{
			name: "missing network security group ID - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.NetworkSecurityGroupID = ""
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "customerProperties.platform.networkSecurityGroupId"},
			},
		},
		{
			name: "wrong NSG resource type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.NetworkSecurityGroupID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "customerProperties.platform.networkSecurityGroupId"},
			},
		},
		{
			name: "node drain timeout too large - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.NodeDrainTimeoutMinutes = 10081
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 10080", fieldPath: "customerProperties.nodeDrainTimeoutMinutes"},
			},
		},
		{
			name: "invalid etcd encryption key management mode - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = "InvalidMode"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode"},
			},
		},
		{
			name: "customer managed without customer managed profile - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged = nil
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be specified when", fieldPath: "customerProperties.etcd.dataEncryption.customerManaged"},
			},
		},
		{
			name: "invalid cluster image registry state - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.ClusterImageRegistry.State = "InvalidState"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "customerProperties.clusterImageRegistry.state"},
			},
		},
		{
			name: "missing user assigned identity name - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"": "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators"},
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[]"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities"},
			},
		},
		{
			name: "invalid user assigned identity resource type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"test-operator": "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities"},
			},
		},
		{
			name: "missing identity type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "identity.type"},
				{message: "Unsupported value", fieldPath: "identity.state"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities"},
			},
		},
		{
			name: "invalid identity type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: "InvalidType",
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "identity.state"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities"},
			},
		},
		{
			name: "invalid user assigned identity resource type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet": {},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "identity.userAssignedIdentities"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
			},
		},
		{
			name: "multiple validation errors - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.DNS.BaseDomainPrefix = "Invalid-Name"
				c.CustomerProperties.Network.NetworkType = "InvalidType"
				c.CustomerProperties.API.Visibility = "InvalidVisibility"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "customerProperties.dns.baseDomainPrefix"},
				{message: "Unsupported value", fieldPath: "customerProperties.network.networkType"},
				{message: "Unsupported value", fieldPath: "customerProperties.api.visiblity"},
			},
		},
		// Tests for validateOperatorAuthenticationAgainstIdentities
		{
			name: "identity assigned but not used - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				unusedIdentityID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/unused-identity"
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						unusedIdentityID: {},
					},
				}
				// Don't reference the identity in operators
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{}
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = ""
				return c
			}(),
			expectErrors: []expectedError{
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities[/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/unused-identity]"},
			},
		},
		{
			name: "identity used but not assigned - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type:                   arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{},
				}
				// Reference an identity that's not assigned
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"test-operator": "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/unassigned-identity",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
			},
		},
		{
			name: "identity used multiple times - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				identityID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/shared-identity"
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						identityID: {},
					},
				}
				// Use the same identity in multiple places
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"operator1": identityID,
					"operator2": identityID,
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "identity is used multiple times", fieldPath: "identity.userAssignedIdentities[/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/shared-identity]"},
			},
		},
		{
			name: "data plane operator uses assigned identity - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				identityID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dataplane-identity"
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						identityID: {},
					},
				}
				// Data plane operators cannot use assigned identities
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]string{
					"dataplane-operator": identityID,
				}
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "cannot use identity assigned to this resource by .identities.userAssignedIdentities", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[dataplane-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities[/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dataplane-identity]"},
			},
		},
		{
			name: "service managed identity used correctly - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				identityID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service-identity"
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						identityID: {},
					},
				}
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{}
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = identityID
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "case insensitive identity matching - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				lowerCaseID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourcegroups/some-resource-group/providers/microsoft.managedidentity/userassignedidentities/test-identity"
				upperCaseID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						lowerCaseID: {},
					},
				}
				// Reference with different casing should work
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"test-operator": upperCaseID,
				}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		// Tests for validateResourceIDsAgainstClusterID
		{
			name: "managed resource group same as cluster resource group - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// Managed resource group cannot be the same as the cluster's resource group
				c.CustomerProperties.Platform.ManagedResourceGroup = "some-resource-group"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must not be the same resource group name", fieldPath: "customerProperties.platform.subnetId"},
				{message: "must not be the same resource group name", fieldPath: "customerProperties.platform.managedResourceGroup"},
				{message: "must not be the same resource group name", fieldPath: "customerProperties.platform.subnetId"},
				{message: "must not be the same resource group name", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
			},
		},
		{
			name: "subnet in different subscription - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// Subnet in different subscription should fail
				c.CustomerProperties.Platform.SubnetID = "/subscriptions/different-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.subnetId"},
			},
		},
		{
			name: "control plane operator identity in wrong location - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// Identity in different subscription
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"test-operator": "/subscriptions/different-sub/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities"},
			},
		},
		{
			name: "data plane operator identity validation - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// Data plane operator identity validation
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]string{
					"dataplane-operator": "/subscriptions/different-sub/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dataplane-identity",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[dataplane-operator]"},
			},
		},
		{
			name: "service managed identity validation - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// Service managed identity validation
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = "/subscriptions/different-sub/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service-identity"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity"},
			},
		},
		// Tests for network CIDR overlap validation
		{
			name: "machine CIDR overlaps with service CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.MachineCIDR = "10.0.0.0/16"
				c.CustomerProperties.Network.ServiceCIDR = "10.0.1.0/24" // Overlaps with machine CIDR
				c.CustomerProperties.Network.PodCIDR = "10.128.0.0/14"   // No overlap
				return c
			}(),
			expectErrors: []expectedError{
				{message: "machine CIDR '10.0.0.0/16' and service CIDR '10.0.1.0/24' overlap", fieldPath: "customerProperties.network"},
			},
		},
		{
			name: "machine CIDR overlaps with pod CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.MachineCIDR = "10.0.0.0/16"
				c.CustomerProperties.Network.PodCIDR = "10.0.1.0/24"       // Overlaps with machine CIDR
				c.CustomerProperties.Network.ServiceCIDR = "172.30.0.0/16" // No overlap
				return c
			}(),
			expectErrors: []expectedError{
				{message: "machine CIDR '10.0.0.0/16' and pod CIDR '10.0.1.0/24' overlap", fieldPath: "customerProperties.network"},
			},
		},
		{
			name: "service CIDR overlaps with pod CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.MachineCIDR = "192.168.0.0/16" // No overlap
				c.CustomerProperties.Network.ServiceCIDR = "10.0.0.0/16"
				c.CustomerProperties.Network.PodCIDR = "10.0.1.0/24" // Overlaps with service CIDR
				return c
			}(),
			expectErrors: []expectedError{
				{message: "service CIDR '10.0.0.0/16' and pod CIDR '10.0.1.0/24' overlap", fieldPath: "customerProperties.network"},
			},
		},
		{
			name: "multiple CIDR overlaps - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// All CIDRs overlap with each other
				c.CustomerProperties.Network.MachineCIDR = "10.0.0.0/14"
				c.CustomerProperties.Network.ServiceCIDR = "10.0.0.0/16"
				c.CustomerProperties.Network.PodCIDR = "10.1.0.0/16"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "machine CIDR '10.0.0.0/14' and service CIDR '10.0.0.0/16' overlap", fieldPath: "customerProperties.network"},
				{message: "machine CIDR '10.0.0.0/14' and pod CIDR '10.1.0.0/16' overlap", fieldPath: "customerProperties.network"},
			},
		},
		{
			name: "non-overlapping CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// No overlaps between any CIDRs
				c.CustomerProperties.Network.MachineCIDR = "192.168.0.0/16"
				c.CustomerProperties.Network.ServiceCIDR = "172.30.0.0/16"
				c.CustomerProperties.Network.PodCIDR = "10.128.0.0/14"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "invalid machine CIDR format - no overlap check - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				// Invalid CIDR format - overlap check should not crash
				c.CustomerProperties.Network.MachineCIDR = "invalid-cidr"
				c.CustomerProperties.Network.ServiceCIDR = "172.30.0.0/16"
				c.CustomerProperties.Network.PodCIDR = "10.128.0.0/14"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.network.machineCidr"},
			},
		},
		// Resource naming validation tests (covering middleware_validatestatic_test.go patterns)
		{
			name: "invalid cluster resource name - special character",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "$"
				c.Name = "$"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid cluster resource name - starts with hyphen",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "-garbage"
				c.Name = "-garbage"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid cluster resource name - starts with number",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "1cluster"
				c.Name = "1cluster"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid cluster resource name - too long",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				longName := "3a725v234c0Qd5bPfSYgk5okd2ps7UApyv8wtv810Y02ZvfAse0pgZemQ6dqE791QVKq6n6DAzU8bQTUOVCHwUOeq9fx92dpFebTgKEsx1Xl8Xrvs8NLehe3bj3h813B3j"
				c.ID.Name = longName
				c.Name = longName
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Too long", fieldPath: "id"},
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "invalid cluster resource name - too short",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "a"
				c.Name = "a"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "id"},
			},
		},
		{
			name: "valid cluster resource name - minimum length",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "abc"
				c.Name = "abc"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster resource name - with hyphens",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "my-cluster-1"
				c.Name = "my-cluster-1"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster resource name - mixed case",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ID.Name = "MyCluster"
				c.Name = "MyCluster"
				return c
			}(),
			expectErrors: []expectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateClusterCreate(ctx, tt.cluster, nil)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

// Comprehensive tests for ValidateClusterUpdate
func TestValidateClusterUpdate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		newCluster   *api.HCPOpenShiftCluster
		oldCluster   *api.HCPOpenShiftCluster
		expectErrors []expectedError
	}{
		{
			name:         "valid cluster update - no changes",
			newCluster:   createValidCluster(),
			oldCluster:   createValidCluster(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - systemData",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.SystemData = &arm.SystemData{
					LastModifiedAt: ptr.To(time.Now()),
				}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.SystemData = &arm.SystemData{
					LastModifiedAt: ptr.To(time.Now().Add(-1 * time.Hour)),
				}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow channel group change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ChannelGroup = "stable"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ChannelGroup = "stable"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow authorized CIDRs change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16", "192.168.1.0/24"}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow autoscaling changes",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Autoscaling.MaxNodesTotal = 200
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Autoscaling.MaxNodesTotal = 100
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow node drain timeout change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.NodeDrainTimeoutMinutes = 60
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "identity cannot change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity":  {},
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity2": {},
					},
				}
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"test-operator":   "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity",
					"test-operator-2": "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity2",
				}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.platform"},
				{message: "field is immutable", fieldPath: "customerProperties.platform.operatorsAuthentication"},
				{message: "field is immutable", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities"},
				{message: "field is immutable", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators"},
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator-2]"},
			},
		},
		{
			name: "immutable provisioning state - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateProvisioning
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateSucceeded
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "serviceProviderProperties.provisioningState"},
			},
		},
		{
			name: "immutable version ID - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ID = "4.15"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ID = "4.16"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.version.id"},
			},
		},
		{
			name: "immutable version ChannelGroup - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ChannelGroup = "fast"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ChannelGroup = "stable"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.version.channelGroup"},
			},
		},
		{
			name: "immutable base domain - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.DNS.BaseDomain = "new.example.com"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.DNS.BaseDomain = "example.com"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "serviceProviderProperties.dns.baseDomain"},
			},
		},
		{
			name: "immutable base domain prefix - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.DNS.BaseDomainPrefix = "new-prefix"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.DNS.BaseDomainPrefix = "old-prefix"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.dns.baseDomainPrefix"},
			},
		},
		{
			name: "immutable network profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.PodCIDR = "10.200.0.0/14"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Network.PodCIDR = "10.128.0.0/14"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.network"},
				{message: "field is immutable", fieldPath: "customerProperties.network.podCidr"},
			},
		},
		{
			name: "immutable console profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.Console.URL = "https://new-console.example.com"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.Console.URL = "https://console.example.com"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "serviceProviderProperties.console"},
				{message: "field is immutable", fieldPath: "serviceProviderProperties.console.url"},
			},
		},
		{
			name: "immutable API URL - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.API.URL = "https://new-api.example.com"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.ServiceProviderProperties.API.URL = "https://api.example.com"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "serviceProviderProperties.api.url"},
			},
		},
		{
			name: "immutable API visibility - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.Visibility = api.VisibilityPrivate
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.Visibility = api.VisibilityPublic
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.api.visiblity"},
			},
		},
		{
			name: "immutable platform profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/new-subnet"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.platform"},
				{message: "field is immutable", fieldPath: "customerProperties.platform.subnetId"},
				{message: "must be in the same Azure subscription", fieldPath: "customerProperties.platform.subnetId"},
			},
		},
		{
			name: "immutable etcd profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.etcd"},
				{message: "field is immutable", fieldPath: "customerProperties.etcd.dataEncryption"},
				{message: "field is immutable", fieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode"},
				{message: "must be specified when `keyManagementMode` is \"CustomerManaged\"", fieldPath: "customerProperties.etcd.dataEncryption.customerManaged"},
			},
		},
		{
			name: "immutable cluster image registry profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.ClusterImageRegistry.State = api.ClusterImageRegistryProfileStateDisabled
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.ClusterImageRegistry.State = api.ClusterImageRegistryProfileStateEnabled
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.clusterImageRegistry"},
				{message: "field is immutable", fieldPath: "customerProperties.clusterImageRegistry.state"},
			},
		},
		{
			name: "invalid new field value on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"invalid-cidr"}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "empty authorized CIDR on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{""}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "authorized CIDR with whitespace on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{" 10.0.0.0/16 "}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "must not contain extra whitespace", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "not an IP", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "IPv6 in authorized CIDRs on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"2001:db8::1"}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "not IPv4", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
				{message: "invalid CIDR address", fieldPath: "customerProperties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "empty list authorized CIDR - create",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "must have at least 1 items", fieldPath: "customerProperties.api.authorizedCidrs"},
			},
		},
		{
			name: "too many authorized CIDRs on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = make([]string, 501)
				for i := range c.CustomerProperties.API.AuthorizedCIDRs {
					c.CustomerProperties.API.AuthorizedCIDRs[i] = "10.0.0.1"
				}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "Too many", fieldPath: "customerProperties.api.authorizedCidrs"},
			},
		},
		{
			name: "501 unique authorized CIDR blocks on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = makeUniqueCIDRs(501)
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "must have at most 500 items", fieldPath: "customerProperties.api.authorizedCidrs"},
			},
		},
		{
			name: "add authorized CIDR on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16", "192.168.1.0/24", "172.16.0.0/12"}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "remove authorized CIDR on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16"}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16", "192.168.1.0/24"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "clear all authorized CIDRs on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = nil
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.API.AuthorizedCIDRs = []string{"10.0.0.0/16", "192.168.1.0/24"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "immutable location - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Location = "westus2"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Location = "eastus"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "trackedResource.location"},
			},
		},
		{
			name: "immutable identity principal ID - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type:        arm.ManagedServiceIdentityTypeUserAssigned,
					PrincipalID: "new-principal-id",
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type:        arm.ManagedServiceIdentityTypeUserAssigned,
					PrincipalID: "old-principal-id",
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "identity.principalId"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities[/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity]"},
			},
		},
		{
			name: "immutable identity tenant ID - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type:     arm.ManagedServiceIdentityTypeUserAssigned,
					TenantID: "new-tenant-id",
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type:     arm.ManagedServiceIdentityTypeUserAssigned,
					TenantID: "old-tenant-id",
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "identity.tenantId"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities[/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity]"},
			},
		},
		{
			name: "immutable user assigned identity client ID - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {
							ClientID: api.Ptr("new-client-id"),
						},
					},
				}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {
							ClientID: api.Ptr("old-client-id"),
						},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "identity.userAssignedIdentities[/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity].clientId"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities[/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity]"},
			},
		},
		{
			name: "immutable user assigned identity principal ID - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {
							PrincipalID: api.Ptr("new-principal-id"),
						},
					},
				}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Identity = &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity": {
							PrincipalID: api.Ptr("old-principal-id"),
						},
					},
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "identity.userAssignedIdentities[/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity].principalId"},
				{message: "identity is not assigned to this resource", fieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
				{message: "identity is assigned to this resource but not used", fieldPath: "identity.userAssignedIdentities[/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity]"},
			},
		},
		{
			name: "multiple immutable field changes - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ID = "4.16"
				c.CustomerProperties.Version.ChannelGroup = "fast"
				c.CustomerProperties.DNS.BaseDomainPrefix = "new-prefix"
				c.CustomerProperties.API.Visibility = api.VisibilityPrivate
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ID = "4.15"
				c.CustomerProperties.Version.ChannelGroup = "stable"
				c.CustomerProperties.DNS.BaseDomainPrefix = "old-prefix"
				c.CustomerProperties.API.Visibility = api.VisibilityPublic
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "customerProperties.version.id"},
				{message: "field is immutable", fieldPath: "customerProperties.version.channelGroup"},
				{message: "field is immutable", fieldPath: "customerProperties.dns.baseDomainPrefix"},
				{message: "field is immutable", fieldPath: "customerProperties.api.visiblity"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateClusterUpdate(ctx, tt.newCluster, tt.oldCluster, nil)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func makeUniqueCIDRs(n int) []string {
	cidrs := make([]string, n)
	for i := range cidrs {
		octet3 := i / 256
		octet4 := i % 256
		cidrs[i] = fmt.Sprintf("10.0.%d.%d", octet3, octet4)
	}
	return cidrs
}

// Helper function to create a valid cluster for testing
func createValidCluster() *api.HCPOpenShiftCluster {
	cluster := api.NewDefaultHCPOpenShiftCluster(
		api.Must(azcorearm.ParseResourceID("/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/noop-updat")),
		api.TestLocation,
	)

	// Set required fields that are not in the default
	cluster.Location = "eastus"                    // Required for TrackedResource validation
	cluster.CustomerProperties.Version.ID = "4.15" // Use MAJOR.MINOR format
	cluster.CustomerProperties.DNS.BaseDomainPrefix = "test-cluster"
	// Use different resource group for subnet to ensure same subscription validation
	cluster.CustomerProperties.Platform.SubnetID = "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
	cluster.CustomerProperties.Platform.NetworkSecurityGroupID = "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.Network/networkSecurityGroups/test-nsg"
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg" // Different from cluster resource group

	// Set up user assigned identities for valid testing with matching subscription and location
	identityID := "/subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
		"test-operator": identityID,
	}

	// Add the identity to the cluster's identity section so it's properly assigned
	cluster.Identity = &arm.ManagedServiceIdentity{
		Type: arm.ManagedServiceIdentityTypeUserAssigned,
		UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
			identityID: {},
		},
	}

	return cluster
}
