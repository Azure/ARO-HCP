package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gotest.tools/assert"
)

func TestDetectTLE(t *testing.T) {
	const TLE = "[ Template Language Expression ]"

	tests := []struct {
		name     string
		resource any
		hasTLE   bool
	}{
		{
			// Based on demo/cluster.tmpl.json
			name: "Complex resource with no TLE",
			resource: map[string]any{
				"properties": map[string]any{
					"version": map[string]any{
						"id":           "openshift-v4.18.1",
						"channelGroup": "stable",
					},
					"dns": map[string]any{},
					"network": map[string]any{
						"networkType": "OVNKubernetes",
						"podCidr":     "10.128.0.0/14",
						"serviceCidr": "172.30.0.0/16",
						"machineCidr": "10.0.0.0/16",
						"hostPrefix":  23,
					},
					"console": map[string]any{},
					"api": map[string]any{
						"visibility": "public",
					},
					"platform": map[string]any{
						"managedResourceGroup":   "$managed-resource-group",
						"subnetId":               "/subscriptions/$sub/resourceGroups/$customer-rg/providers/Microsoft.Network/virtualNetworks/customer-vnet/subnets/customer-subnet-1",
						"outboundType":           "loadBalancer",
						"networkSecurityGroupId": "/subscriptions/$sub/resourceGroups/$customer-rg/providers/Microsoft.Network/networkSecurityGroups/customer-nsg",
						"operatorsAuthentication": map[string]any{
							"userAssignedIdentities": map[string]any{
								"controlPlaneOperators": map[string]string{
									"example_operator": "example_resource_id",
								},
								"dataPlaneOperators": map[string]string{
									"example_operator": "example_resource_id",
								},
								"serviceManagedIdentity": "example_resource_id",
							},
						},
					},
				},
				"identity": map[string]any{
					"type": "UserAssigned",
					"userAssignedIdentities": map[string]any{
						"example_resource_id": map[string]any{},
					},
				},
			},
			hasTLE: false,
		},
		{
			name:     "String with TLE",
			resource: TLE,
			hasTLE:   true,
		},
		{
			name:     "Slice with TLE",
			resource: []any{TLE},
			hasTLE:   true,
		},
		{
			name: "Map with TLE in key",
			resource: map[string]any{
				TLE: "value",
			},
			hasTLE: true,
		},
		{
			name: "Map with TLE in value",
			resource: map[string]any{
				"key": TLE,
			},
			hasTLE: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.resource)
			require.NoError(t, err)

			hasTLE, err := DetectTLE(data)
			require.NoError(t, err)

			assert.Equal(t, tt.hasTLE, hasTLE)
		})
	}
}
