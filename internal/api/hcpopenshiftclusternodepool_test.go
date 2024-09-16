package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func minimumValidNodePool() *HCPOpenShiftClusterNodePool {
	// Values are meaningless but need to pass validation.
	return &HCPOpenShiftClusterNodePool{
		Properties: HCPOpenShiftClusterNodePoolProperties{
			Spec: NodePoolSpec{
				Version: VersionProfile{
					ID:           "openshift-v4.16.0",
					ChannelGroup: "stable",
				},
				Platform: NodePoolPlatformProfile{
					VMSize: "Standard_D8s_v3",
				},
			},
		},
	}
}

func TestNodePoolRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		resource     *HCPOpenShiftClusterNodePool
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:     "Empty node pool",
			resource: &HCPOpenShiftClusterNodePool{},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'properties'",
					Target:  "properties",
				},
			},
		},
		{
			name: "Default node pool",
			// NewDefaultHCPOpenShiftClusterNodePool does not currently have
			// any non-zero defaults. We need a non-zero value somewhere to
			// trigger required fields beyond just "properties".
			resource: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Spec: NodePoolSpec{
						AutoRepair: true,
					},
				},
			},
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
					Message: "Missing required field 'vmSize'",
					Target:  "properties.spec.platform.vmSize",
				},
			},
		},
		{
			name:     "Minimum valid node pool",
			resource: minimumValidNodePool(),
		},
	}

	// from hcpopenshiftcluster_test.go
	validate := newTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualErrors := ValidateRequest(validate, http.MethodPut, tt.resource)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
