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
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Comprehensive tests for ValidateClusterCreate
func TestValidateClusterCreate(t *testing.T) {
	ctx := context.Background()

	type expectedError struct {
		message   string // Expected error message (partial match)
		fieldPath string // Expected field path for the error
	}

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
			name: "invalid version - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ID = "invalid-version"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "invalid DNS prefix - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.DNS.BaseDomainPrefix = "Invalid-Name"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "properties.dns.baseDomainPrefix"},
			},
		},
		{
			name: "invalid network type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.NetworkType = "InvalidType"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.network.networkType"},
			},
		},
		{
			name: "invalid Pod CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.PodCIDR = "invalid-cidr"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "properties.network.podCidr"},
			},
		},
		{
			name: "invalid Service CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.ServiceCIDR = "300.0.0.0/16"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "properties.network.serviceCidr"},
			},
		},
		{
			name: "invalid Machine CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.MachineCIDR = "2001:db8::/32"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "not IPv4", fieldPath: "properties.network.machineCidr"},
			},
		},
		{
			name: "host prefix too small - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.HostPrefix = 22
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be greater than or equal to 23", fieldPath: "properties.network.hostPrefix"},
			},
		},
		{
			name: "host prefix too large - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.HostPrefix = 27
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 26", fieldPath: "properties.network.hostPrefix"},
			},
		},
		{
			name: "invalid API visibility - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.Visibility = "InvalidVisibility"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.api.visiblity"},
			},
		},
		{
			name: "too many authorized CIDRs - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.AuthorizedCIDRs = make([]string, 501)
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Too long", fieldPath: "properties.api.authorizedCidrs"},
			},
		},
		{
			name: "invalid authorized CIDR - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.AuthorizedCIDRs = []string{"invalid-cidr"}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "properties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "missing subnet ID - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.SubnetID = ""
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.platform.subnetId"},
			},
		},
		{
			name: "invalid outbound type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.OutboundType = "InvalidType"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.platform.outboundType"},
			},
		},
		{
			name: "missing network security group ID - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.NetworkSecurityGroupID = ""
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.platform.networkSecurityGroupId"},
			},
		},
		{
			name: "wrong NSG resource type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.NetworkSecurityGroupID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "properties.platform.networkSecurityGroupId"},
			},
		},
		{
			name: "node drain timeout too large - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.NodeDrainTimeoutMinutes = 10081
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be less than or equal to 10080", fieldPath: "properties.nodeDrainTimeoutMinutes"},
			},
		},
		{
			name: "invalid etcd encryption key management mode - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Etcd.DataEncryption.KeyManagementMode = "InvalidMode"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.etcd.dataEncryption.keyManagementMode"},
			},
		},
		{
			name: "customer managed without customer managed profile - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
				c.Properties.Etcd.DataEncryption.CustomerManaged = nil
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be specified when", fieldPath: "properties.etcd.dataEncryption.customerManaged"},
			},
		},
		{
			name: "invalid cluster image registry state - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.ClusterImageRegistry.State = "InvalidState"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Unsupported value", fieldPath: "properties.clusterImageRegistry.state"},
			},
		},
		{
			name: "missing user assigned identity name - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"": "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Required value", fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators"},
			},
		},
		{
			name: "invalid user assigned identity resource type - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
					"test-operator": "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
				}
				return c
			}(),
			expectErrors: []expectedError{
				{message: "resource ID must reference an instance of type", fieldPath: "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[test-operator]"},
			},
		},
		{
			name: "multiple validation errors - create",
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ID = "invalid-version"
				c.Properties.DNS.BaseDomainPrefix = "Invalid-Name"
				c.Properties.Network.NetworkType = "InvalidType"
				c.Properties.API.Visibility = "InvalidVisibility"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
				{message: "must be a valid DNS RFC 1035 label", fieldPath: "properties.dns.baseDomainPrefix"},
				{message: "Unsupported value", fieldPath: "properties.network.networkType"},
				{message: "Unsupported value", fieldPath: "properties.api.visiblity"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateClusterCreate(ctx, tt.cluster)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}

			// Check that each expected error message and field path is found
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
					fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
					if messageMatch && fieldMatch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}

// Comprehensive tests for ValidateClusterUpdate
func TestValidateClusterUpdate(t *testing.T) {
	ctx := context.Background()

	type expectedError struct {
		message   string // Expected error message (partial match)
		fieldPath string // Expected field path for the error
	}

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
			name: "valid cluster update - allow channel group change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ChannelGroup = "stable"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ChannelGroup = "stable"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow authorized CIDRs change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.AuthorizedCIDRs = []string{"10.0.0.0/16", "192.168.1.0/24"}
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.AuthorizedCIDRs = []string{"10.0.0.0/16"}
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow autoscaling changes",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Autoscaling.MaxNodesTotal = 200
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Autoscaling.MaxNodesTotal = 100
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster update - allow node drain timeout change",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.NodeDrainTimeoutMinutes = 60
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.NodeDrainTimeoutMinutes = 30
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "immutable provisioning state - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.ProvisioningState = arm.ProvisioningStateProvisioning
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.ProvisioningState = arm.ProvisioningStateSucceeded
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.provisioningState"},
			},
		},
		{
			name: "immutable version ID - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ID = "4.15.2"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ID = "4.15.1"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "immutable base domain - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.DNS.BaseDomain = "new.example.com"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.DNS.BaseDomain = "example.com"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.dns.baseDomain"},
			},
		},
		{
			name: "immutable base domain prefix - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.DNS.BaseDomainPrefix = "new-prefix"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.DNS.BaseDomainPrefix = "old-prefix"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.dns.baseDomainPrefix"},
			},
		},
		{
			name: "immutable network profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.PodCIDR = "10.200.0.0/14"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Network.PodCIDR = "10.128.0.0/14"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.network"},
			},
		},
		{
			name: "immutable console profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Console.URL = "https://new-console.example.com"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Console.URL = "https://console.example.com"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.console.url"},
			},
		},
		{
			name: "immutable API URL - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.URL = "https://new-api.example.com"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.URL = "https://api.example.com"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.api.url"},
			},
		},
		{
			name: "immutable API visibility - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.Visibility = api.VisibilityPrivate
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.Visibility = api.VisibilityPublic
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.api.visiblity"},
			},
		},
		{
			name: "immutable platform profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/new-subnet"
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.platform.subnetId"},
			},
		},
		{
			name: "immutable etcd profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Etcd.DataEncryption.KeyManagementMode = api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.etcd"},
			},
		},
		{
			name: "immutable cluster image registry profile - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.ClusterImageRegistry.State = api.ClusterImageRegistryProfileStateDisabled
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.ClusterImageRegistry.State = api.ClusterImageRegistryProfileStateEnabled
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.clusterImageRegistry"},
			},
		},
		{
			name: "invalid new field value on update - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.API.AuthorizedCIDRs = []string{"invalid-cidr"}
				return c
			}(),
			oldCluster: createValidCluster(),
			expectErrors: []expectedError{
				{message: "invalid CIDR address", fieldPath: "properties.api.authorizedCidrs[0]"},
			},
		},
		{
			name: "multiple immutable field changes - update",
			newCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ID = "4.15.2"
				c.Properties.DNS.BaseDomainPrefix = "new-prefix"
				c.Properties.API.Visibility = api.VisibilityPrivate
				return c
			}(),
			oldCluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.Properties.Version.ID = "4.15.1"
				c.Properties.DNS.BaseDomainPrefix = "old-prefix"
				c.Properties.API.Visibility = api.VisibilityPublic
				return c
			}(),
			expectErrors: []expectedError{
				{message: "field is immutable", fieldPath: "properties.version.id"},
				{message: "field is immutable", fieldPath: "properties.dns.baseDomainPrefix"},
				{message: "field is immutable", fieldPath: "properties.api.visiblity"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateClusterUpdate(ctx, tt.newCluster, tt.oldCluster)

			if len(tt.expectErrors) == 0 {
				if len(errs) != 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs)
				}
				return
			}

			// Check that each expected error message and field path is found
			for _, expectedErr := range tt.expectErrors {
				found := false
				for _, err := range errs {
					messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
					fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
					if messageMatch && fieldMatch {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
				}
			}
		})
	}
}

// Helper function to create a valid cluster for testing
func createValidCluster() *api.HCPOpenShiftCluster {
	cluster := api.NewDefaultHCPOpenShiftCluster()

	// Set required fields that are not in the default
	cluster.Properties.Version.ID = "4.15.1"
	cluster.Properties.DNS.BaseDomainPrefix = "test-cluster"
	cluster.Properties.Platform.SubnetID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"
	cluster.Properties.Platform.NetworkSecurityGroupID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg"

	// Set up user assigned identities for valid testing
	cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]string{
		"test-operator": "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity",
	}

	return cluster
}
