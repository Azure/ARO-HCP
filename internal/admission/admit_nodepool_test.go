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

package admission

import (
	"context"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func createValidNodePool() *api.HCPOpenShiftClusterNodePool {
	resourceID, _ := azcorearm.ParseResourceID(api.TestNodePoolResourceID)
	nodePool := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID, api.TestLocation)
	nodePool.Properties.Version.ID = "4.15.0"
	nodePool.Properties.Version.ChannelGroup = "stable"
	nodePool.Properties.Platform.VMSize = "Standard_D8s_v3"
	return nodePool
}

func createValidCluster() *api.HCPOpenShiftCluster {
	cluster := api.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Version.ChannelGroup = "stable"
	return cluster
}

func TestAdmitNodePool(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		nodePool     *api.HCPOpenShiftClusterNodePool
		cluster      *api.HCPOpenShiftCluster
		subscription *arm.Subscription
		expectErrors []expectedError
	}{
		{
			name:         "valid node pool with X.Y.Z version",
			nodePool:     createValidNodePool(),
			cluster:      createValidCluster(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{},
		},
		{
			name: "version malformed using X.Y format",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "4.19"
				return np
			}(),
			cluster:      createValidCluster(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "must be specified as MAJOR.MINOR.PATCH", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "channel group mismatch with cluster",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ChannelGroup = "fast"
				return np
			}(),
			cluster:      createValidCluster(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "must be the same as control plane channel group", fieldPath: "properties.version.channelGroup"},
			},
		},
		{
			name: "invalid version format - malformed",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "invalid-version"
				return np
			}(),
			cluster:      createValidCluster(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AdmitNodePool(ctx, tt.nodePool, tt.cluster, tt.subscription)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestAdmitNodePoolWithNonStableChannels(t *testing.T) {
	ctx := context.Background()
	subscription := api.CreateTestSubscription(api.FeatureAllowDevNonStableChannels)

	tests := []struct {
		name         string
		nodePool     *api.HCPOpenShiftClusterNodePool
		cluster      *api.HCPOpenShiftCluster
		expectErrors []expectedError
	}{
		{
			name: "non-stable channel allows prerelease versions",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "4.19.0-rc.1"
				np.Properties.Version.ChannelGroup = "candidate"
				return np
			}(),
			cluster: func() *api.HCPOpenShiftCluster {
				c := createValidCluster()
				c.CustomerProperties.Version.ChannelGroup = "candidate"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "stable channel still requires X.Y.Z format with feature flag",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := createValidNodePool()
				np.Properties.Version.ID = "4.19"
				np.Properties.Version.ChannelGroup = "stable"
				return np
			}(),
			cluster:      createValidCluster(),
			expectErrors: []expectedError{
				{message: "must be specified as MAJOR.MINOR.PATCH", fieldPath: "properties.version.id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AdmitNodePool(ctx, tt.nodePool, tt.cluster, subscription)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}