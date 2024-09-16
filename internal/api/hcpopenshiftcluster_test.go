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
			Spec: ClusterSpec{
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
					SubnetID:               "/something/something/virtualNetworks/subnets",
					NetworkSecurityGroupID: "/something/something/networkSecurityGroups",
				},
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
					Message: "Missing required field 'id'",
					Target:  "properties.spec.version.id",
				},
				{
					Message: "Missing required field 'channelGroup'",
					Target:  "properties.spec.version.channelGroup",
				},
				{
					Message: "Missing required field 'podCidr'",
					Target:  "properties.spec.network.podCidr",
				},
				{
					Message: "Missing required field 'serviceCidr'",
					Target:  "properties.spec.network.serviceCidr",
				},
				{
					Message: "Missing required field 'machineCidr'",
					Target:  "properties.spec.network.machineCidr",
				},
				{
					Message: "Missing required field 'visibility'",
					Target:  "properties.spec.api.visibility",
				},
				{
					Message: "Missing required field 'subnetId'",
					Target:  "properties.spec.platform.subnetId",
				},
				{
					Message: "Missing required field 'networkSecurityGroupId'",
					Target:  "properties.spec.platform.networkSecurityGroupId",
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
					Spec: ClusterSpec{
						Network: NetworkProfile{
							PodCIDR: "Mmm... apple cider",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'Mmm... apple cider' for field 'podCidr' (must be a v4 CIDR range)",
					Target:  "properties.spec.network.podCidr",
				},
			},
		},
		{
			name: "Bad dns_rfc1035_label",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Spec: ClusterSpec{
						DNS: DNSProfile{
							BaseDomainPrefix: "0badlabel",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '0badlabel' for field 'baseDomainPrefix' (must be a valid DNS RFC 1035 label)",
					Target:  "properties.spec.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Bad enum_outboundtype",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Spec: ClusterSpec{
						Platform: PlatformProfile{
							OutboundType: "loadJuggler",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'loadJuggler' for field 'outboundType' (must be loadBalancer)",
					Target:  "properties.spec.platform.outboundType",
				},
			},
		},
		{
			name: "Bad enum_visibility",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Spec: ClusterSpec{
						API: APIProfile{
							Visibility: "it's a secret to everybody",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'it's a secret to everybody' for field 'visibility' (must be one of: private public)",
					Target:  "properties.spec.api.visibility",
				},
			},
		},
		{
			name: "Bad startswith=http:",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Spec: ClusterSpec{
						Proxy: ProxyProfile{
							HTTPProxy: "ftp://not_an_http_url",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'ftp://not_an_http_url' for field 'httpProxy' (must start with 'http:')",
					Target:  "properties.spec.proxy.httpProxy",
				},
			},
		},
		{
			name: "Bad url",
			tweaks: &HCPOpenShiftCluster{
				Properties: HCPOpenShiftClusterProperties{
					Spec: ClusterSpec{
						Proxy: ProxyProfile{
							HTTPProxy: "http_but_not_a_url",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'http_but_not_a_url' for field 'httpProxy' (must be a URL)",
					Target:  "properties.spec.proxy.httpProxy",
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
