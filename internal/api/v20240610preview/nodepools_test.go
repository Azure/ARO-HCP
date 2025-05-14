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

package v20240610preview

import (
	"fmt"
	"path"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestNodePoolValidateStaticComplex(t *testing.T) {
	tests := []struct {
		name         string
		tweaks       *api.HCPOpenShiftClusterNodePool
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:   "Minimum valid node pool",
			tweaks: &api.HCPOpenShiftClusterNodePool{},
		},
		{
			name: "Node pool with inconsistent channel group",
			tweaks: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Version: api.NodePoolVersionProfile{
						ChannelGroup: "freshmeat",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Node pool channel group 'freshmeat' must be the same as control plane channel group 'stable'",
					Target:  "properties.version.channelGroup",
				},
			},
		},
		{
			name: "Node pool with invalid subnet ID",
			tweaks: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Platform: api.NodePoolPlatformProfile{
						SubnetID: path.Join(api.TestGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", "otherVirtualNetwork", "subnets"),
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: fmt.Sprintf("Subnet '%s' must belong to the same VNet as the parent cluster VNet '%s'",
						path.Join(api.TestGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", "otherVirtualNetwork", "subnets"),
						path.Join(api.TestGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", "testVirtualNetwork")),
					Target: "properties.platform.subnetId",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nodePool NodePool
			cluster := api.MinimumValidClusterTestCase()
			resource := api.NodePoolTestCase(t, tt.tweaks)
			actualErrors := nodePool.validateStaticComplex(resource, cluster)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
