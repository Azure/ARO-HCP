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

	validate.RegisterAlias("enum_outboundtype", EnumValidateTag("loadBalancer"))
	validate.RegisterAlias("enum_visibility", EnumValidateTag("private", "public"))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(
		arm.ManagedServiceIdentityTypeNone,
		arm.ManagedServiceIdentityTypeSystemAssigned,
		arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		arm.ManagedServiceIdentityTypeUserAssigned))

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
				ID:           "openshift-v4.16.0",
				ChannelGroup: "stable",
			},
			Network: NetworkProfile{
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
			},
			API: APIProfile{
				Visibility: "public",
			},
			Platform: PlatformProfile{
				SubnetID:                "/something/something/virtualNetworks/subnets",
				OperatorsAuthentication: OperatorsAuthenticationProfile{UserAssignedIdentities: UserAssignedIdentitiesProfile{ControlPlaneOperators: map[string]string{"operatorX": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity"}}},
			},
		},
		Identity: arm.ManagedServiceIdentity{UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{"/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity": &arm.UserAssignedIdentity{}}},
	}
}

func minimumValidClusterwithBrokenIdentityAndOperatorsAuthentication() *HCPOpenShiftCluster {
	// Values are meaningless but need to pass validation.
	return &HCPOpenShiftCluster{
		Properties: HCPOpenShiftClusterProperties{
			Version: VersionProfile{
				ID:           "openshift-v4.16.0",
				ChannelGroup: "stable",
			},
			Network: NetworkProfile{
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
			},
			API: APIProfile{
				Visibility: "public",
			},
			Platform: PlatformProfile{
				SubnetID:                "/something/something/virtualNetworks/subnets",
				OperatorsAuthentication: OperatorsAuthenticationProfile{UserAssignedIdentities: UserAssignedIdentitiesProfile{ControlPlaneOperators: map[string]string{"operatorX": "wrong/Pattern/Of/ResourceID"}}},
			},
		},
		Identity: arm.ManagedServiceIdentity{UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{"wrong/Pattern/Of/ResourceID": &arm.UserAssignedIdentity{}}},
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
					Message: "Missing required field 'id'",
					Target:  "properties.version.id",
				},
				{
					Message: "Missing required field 'channelGroup'",
					Target:  "properties.version.channelGroup",
				},
				{
					Message: "Missing required field 'podCidr'",
					Target:  "properties.network.podCidr",
				},
				{
					Message: "Missing required field 'serviceCidr'",
					Target:  "properties.network.serviceCidr",
				},
				{
					Message: "Missing required field 'machineCidr'",
					Target:  "properties.network.machineCidr",
				},
				{
					Message: "Missing required field 'visibility'",
					Target:  "properties.api.visibility",
				},
				{
					Message: "Missing required field 'subnetId'",
					Target:  "properties.platform.subnetId",
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
					Message: "Invalid value 'it's a secret to everybody' for field 'visibility' (must be one of: private public)",
					Target:  "properties.api.visibility",
				},
			},
		},
		{
			name: "Bad enum_managedserviceidentitytype",
			tweaks: &HCPOpenShiftCluster{
				Identity: arm.ManagedServiceIdentity{Type: "brokenServiceType"},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'brokenServiceType' for field 'type' (must be one of: None SystemAssigned SystemAssigned,UserAssigned UserAssigned)",
					Target:  "identity.type",
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
