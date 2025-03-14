package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func compareErrors(x, y []arm.CloudErrorBody) string {
	return cmp.Diff(x, y,
		cmpopts.SortSlices(func(x, y arm.CloudErrorBody) bool { return x.Target < y.Target }),
		cmpopts.IgnoreFields(arm.CloudErrorBody{}, "Code"))
}

// This function returns a valid HCPOpenShiftCluster where MI in Identity field is used in OperatorsAuthentication field.
func minimumValidClusterIdentities() *api.HCPOpenShiftCluster {
	// Values are meaningless but need to pass validation.
	return &api.HCPOpenShiftCluster{
		Properties: api.HCPOpenShiftClusterProperties{
			Network: api.NetworkProfile{
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
			},
			API: api.APIProfile{
				Visibility: "public",
			},
			Platform: api.PlatformProfile{
				SubnetID: "/something/something/virtualNetworks/subnets",
				OperatorsAuthentication: api.OperatorsAuthenticationProfile{
					UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]string{
							"operatorX": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1",
						},
						ServiceManagedIdentity: "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity2",
					},
				},
			},
		},
		Identity: arm.ManagedServiceIdentity{
			UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
				"/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1": &arm.UserAssignedIdentity{},
				"/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity2": &arm.UserAssignedIdentity{},
			},
		},
	}
}

// This function returns a in-valid HCPOpenShiftCluster where MI is assigned in Identity field but its not used in OperatorsAuthentication field.
func minimumValidClusterwithBrokenIdentities() *api.HCPOpenShiftCluster {
	// Values are meaningless but need to pass validation.
	return &api.HCPOpenShiftCluster{
		Properties: api.HCPOpenShiftClusterProperties{
			Network: api.NetworkProfile{
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
			},
			API: api.APIProfile{
				Visibility: "public",
			},
			Platform: api.PlatformProfile{
				SubnetID: "/something/something/virtualNetworks/subnets",
				OperatorsAuthentication: api.OperatorsAuthenticationProfile{
					UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]string{
							"operatorX": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1",
						},
						ServiceManagedIdentity: "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity2",
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

// This function returns a in-valid HCPOpenShiftCluster where MI is assigned in Identity field but its used multiple times in OperatorsAuthentication field.
func minimumValidClusterwithMultipleIdentities() *api.HCPOpenShiftCluster {
	// Values are meaningless but need to pass validation.
	return &api.HCPOpenShiftCluster{
		Properties: api.HCPOpenShiftClusterProperties{
			Network: api.NetworkProfile{
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
			},
			API: api.APIProfile{
				Visibility: "public",
			},
			Platform: api.PlatformProfile{
				SubnetID: "/something/something/virtualNetworks/subnets",
				OperatorsAuthentication: api.OperatorsAuthenticationProfile{
					UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
						ControlPlaneOperators: map[string]string{
							"operatorX": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1",
							"operatorY": "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1",
						},
						ServiceManagedIdentity: "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1",
					},
				},
			},
		},
		Identity: arm.ManagedServiceIdentity{
			UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
				"/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1": &arm.UserAssignedIdentity{},
			},
		},
	}
}
func TestClusterRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		resource     *api.HCPOpenShiftCluster
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:     "Minimum valid cluster with Broken Identity",
			resource: minimumValidClusterwithBrokenIdentities(),
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "identity /subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1 is not assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					Message: "identity /subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity is assigned to this resource but not used",
					Target:  "identity.UserAssignedIdentities",
				},
				{
					Message: "identity /subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity2 is not assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name:     "Minimum valid cluster with Multiple Identity",
			resource: minimumValidClusterwithMultipleIdentities(),
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "identity /subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1 is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					Message: "identity /subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1 is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorY]",
				},
				{
					Message: "identity /subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/MyResourceGroup/providers/Microsoft.ManagedIdentity/userAssignedIdentities/MyManagedIdentity1 is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name:     "Minimum valid cluster",
			resource: minimumValidClusterIdentities(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualErrors := validateStaticComplex(tt.resource)
			fmt.Printf("tt: %v\n", actualErrors)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
