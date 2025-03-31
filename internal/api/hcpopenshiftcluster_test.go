package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"testing"

	"dario.cat/mergo"
	validator "github.com/go-playground/validator/v10"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func newTestValidator() *validator.Validate {
	validate := NewValidator()

	validate.RegisterAlias("enum_diskstorageaccounttype", EnumValidateTag(
		DiskStorageAccountTypePremium_LRS,
		DiskStorageAccountTypeStandardSSD_LRS,
		DiskStorageAccountTypeStandard_LRS))
	validate.RegisterAlias("enum_networktype", EnumValidateTag(
		NetworkTypeOVNKubernetes,
		NetworkTypeOther))
	validate.RegisterAlias("enum_outboundtype", EnumValidateTag(
		OutboundTypeLoadBalancer))
	validate.RegisterAlias("enum_visibility", EnumValidateTag(
		VisibilityPublic,
		VisibilityPrivate))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(
		arm.ManagedServiceIdentityTypeNone,
		arm.ManagedServiceIdentityTypeSystemAssigned,
		arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		arm.ManagedServiceIdentityTypeUserAssigned))
	validate.RegisterAlias("enum_optionalclustercapability", EnumValidateTag(
		OptionalClusterCapabilityImageRegistry))

	return validate
}

func compareErrors(x, y []arm.CloudErrorBody) string {
	return cmp.Diff(x, y,
		cmpopts.SortSlices(func(x, y arm.CloudErrorBody) bool { return x.Target < y.Target }),
		cmpopts.IgnoreFields(arm.CloudErrorBody{}, "Code"))
}

func minimumValidCluster() *HCPOpenShiftCluster {
	// Values are meaningless but need to pass validation.
	return &HCPOpenShiftCluster{
		Properties: HCPOpenShiftClusterProperties{
			Version: VersionProfile{
				ChannelGroup: "stable",
			},
			Platform: PlatformProfile{
				SubnetID:               "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/virtualNetworks/MyVNet/subnets",
				NetworkSecurityGroupID: "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/networkSecurityGroups/MyNSG",
				OperatorsAuthentication: OperatorsAuthenticationProfile{
					UserAssignedIdentities: UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]string{
							"operatorX": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity",
						},
					},
				},
			},
		},
		Identity: arm.ManagedServiceIdentity{
			UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
				"/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity": &arm.UserAssignedIdentity{},
			},
		},
	}
}

func minimumValidClusterwithBrokenIdentityAndOperatorsAuthentication() *HCPOpenShiftCluster {
	// Values are meaningless but need to pass validation.
	return &HCPOpenShiftCluster{
		Properties: HCPOpenShiftClusterProperties{
			Version: VersionProfile{
				ChannelGroup: "stable",
			},
			Platform: PlatformProfile{
				SubnetID:               "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/virtualNetworks/MyVNet/subnets",
				NetworkSecurityGroupID: "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.Network/networkSecurityGroups/MyNSG",
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
	}
}

func TestClusterRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		resource     *HCPOpenShiftCluster
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
			name:     "Minimum valid cluster with Broken Identity",
			resource: minimumValidClusterwithBrokenIdentityAndOperatorsAuthentication(),
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
		{
			name:     "Minimum valid cluster",
			resource: minimumValidCluster(),
		},
	}

	validate := newTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualErrors := ValidateRequest(validate, http.MethodPut, tt.resource)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}

func TestClusterValidateTags(t *testing.T) {
	// Note "required_for_put" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *HCPOpenShiftCluster
		expectErrors []arm.CloudErrorBody
	}{
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
	}

	validate := newTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := minimumValidCluster()
			err := mergo.Merge(resource, tt.tweaks, mergo.WithOverride)
			if err != nil {
				t.Fatal(err)
			}

			actualErrors := ValidateRequest(validate, http.MethodPut, resource)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
