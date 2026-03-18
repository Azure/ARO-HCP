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

package v20251223preview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// TestNodePoolZeroValueRoundTripThroughJSON verifies that explicit zero values
// survive a GET-then-PUT round-trip through JSON serialization.
//
// These tests validate the PtrOrNil → Ptr fix: zero/false values must appear
// in GET responses so that GET-then-PUT preserves them.
//
// Round-trip: internal -> external -> JSON -> external -> defaults -> internal
//
// The existing roundTripInternalNodePool test (in conversion_fuzz_test.go)
// does NOT catch this because it skips JSON serialization.
//
// See docs/api-version-defaults-and-storage.md for the full design rationale.
func TestNodePoolZeroValueRoundTripThroughJSON(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *api.HCPOpenShiftClusterNodePool
		check func(t *testing.T, result *api.HCPOpenShiftClusterNodePool)
	}{
		{
			name: "AutoRepair false must survive round-trip",
			setup: func() *api.HCPOpenShiftClusterNodePool {
				np := newBaselineInternalNodePool()
				np.Properties.AutoRepair = false
				return np
			},
			check: func(t *testing.T, np *api.HCPOpenShiftClusterNodePool) {
				if np.Properties.AutoRepair != false {
					t.Errorf("AutoRepair: got %v, want false (default=true clobbered explicit value)", np.Properties.AutoRepair)
				}
			},
		},
		{
			name: "Replicas zero must survive round-trip",
			setup: func() *api.HCPOpenShiftClusterNodePool {
				np := newBaselineInternalNodePool()
				np.Properties.Replicas = 0
				return np
			},
			check: func(t *testing.T, np *api.HCPOpenShiftClusterNodePool) {
				if np.Properties.Replicas != 0 {
					t.Errorf("Replicas: got %d, want 0", np.Properties.Replicas)
				}
			},
		},
		{
			name: "AutoScaling.Min zero when Max is non-zero",
			setup: func() *api.HCPOpenShiftClusterNodePool {
				np := newBaselineInternalNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 0, Max: 5}
				return np
			},
			check: func(t *testing.T, np *api.HCPOpenShiftClusterNodePool) {
				require.NotNil(t, np.Properties.AutoScaling, "AutoScaling struct should not be nil")
				if np.Properties.AutoScaling.Min != 0 {
					t.Errorf("AutoScaling.Min: got %d, want 0", np.Properties.AutoScaling.Min)
				}
			},
		},
		{
			name: "AutoScaling.Max zero when Min is non-zero",
			setup: func() *api.HCPOpenShiftClusterNodePool {
				np := newBaselineInternalNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 3, Max: 0}
				return np
			},
			check: func(t *testing.T, np *api.HCPOpenShiftClusterNodePool) {
				require.NotNil(t, np.Properties.AutoScaling, "AutoScaling struct should not be nil")
				if np.Properties.AutoScaling.Max != 0 {
					t.Errorf("AutoScaling.Max: got %d, want 0", np.Properties.AutoScaling.Max)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.setup()
			result := jsonRoundTripNodePool(t, original)
			tt.check(t, result)
		})
	}
}

// TestClusterZeroValueRoundTripThroughJSON verifies the same GET-then-PUT
// round-trip safety for cluster-level fields. All tests pass after the
// PtrOrNil → Ptr fix.
func TestClusterZeroValueRoundTripThroughJSON(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *api.HCPOpenShiftCluster
		check func(t *testing.T, result *api.HCPOpenShiftCluster)
	}{
		{
			name: "HostPrefix zero must survive round-trip",
			setup: func() *api.HCPOpenShiftCluster {
				c := newBaselineInternalCluster()
				c.CustomerProperties.Network.HostPrefix = 0
				return c
			},
			check: func(t *testing.T, c *api.HCPOpenShiftCluster) {
				if c.CustomerProperties.Network.HostPrefix != 0 {
					t.Errorf("HostPrefix: got %d, want 0",
						c.CustomerProperties.Network.HostPrefix)
				}
			},
		},
		{
			name: "NodeDrainTimeoutMinutes zero must survive round-trip",
			setup: func() *api.HCPOpenShiftCluster {
				c := newBaselineInternalCluster()
				c.CustomerProperties.NodeDrainTimeoutMinutes = 0
				return c
			},
			check: func(t *testing.T, c *api.HCPOpenShiftCluster) {
				if c.CustomerProperties.NodeDrainTimeoutMinutes != 0 {
					t.Errorf("NodeDrainTimeoutMinutes: got %d, want 0",
						c.CustomerProperties.NodeDrainTimeoutMinutes)
				}
			},
		},
		{
			name: "MaxNodesTotal zero must survive round-trip",
			setup: func() *api.HCPOpenShiftCluster {
				c := newBaselineInternalCluster()
				c.CustomerProperties.Autoscaling.MaxNodesTotal = 0
				return c
			},
			check: func(t *testing.T, c *api.HCPOpenShiftCluster) {
				if c.CustomerProperties.Autoscaling.MaxNodesTotal != 0 {
					t.Errorf("MaxNodesTotal: got %d, want 0",
						c.CustomerProperties.Autoscaling.MaxNodesTotal)
				}
			},
		},
		{
			name: "MaxPodGracePeriodSeconds zero must survive round-trip",
			setup: func() *api.HCPOpenShiftCluster {
				c := newBaselineInternalCluster()
				c.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds = 0
				return c
			},
			check: func(t *testing.T, c *api.HCPOpenShiftCluster) {
				if c.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds != 0 {
					t.Errorf("MaxPodGracePeriodSeconds: got %d, want 0",
						c.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds)
				}
			},
		},
		{
			name: "MaxNodeProvisionTimeSeconds zero must survive round-trip",
			setup: func() *api.HCPOpenShiftCluster {
				c := newBaselineInternalCluster()
				c.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds = 0
				return c
			},
			check: func(t *testing.T, c *api.HCPOpenShiftCluster) {
				if c.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds != 0 {
					t.Errorf("MaxNodeProvisionTimeSeconds: got %d, want 0",
						c.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds)
				}
			},
		},
		{
			name: "PodPriorityThreshold zero must survive round-trip",
			setup: func() *api.HCPOpenShiftCluster {
				c := newBaselineInternalCluster()
				c.CustomerProperties.Autoscaling.PodPriorityThreshold = 0
				return c
			},
			check: func(t *testing.T, c *api.HCPOpenShiftCluster) {
				if c.CustomerProperties.Autoscaling.PodPriorityThreshold != 0 {
					t.Errorf("PodPriorityThreshold: got %d, want 0",
						c.CustomerProperties.Autoscaling.PodPriorityThreshold)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.setup()
			result := jsonRoundTripCluster(t, original)
			tt.check(t, result)
		})
	}
}

// jsonRoundTripNodePool simulates a GET-then-PUT cycle through JSON.
// This is the path where PtrOrNil data loss manifests:
//
//	internal -> NewHCPOpenShiftClusterNodePool -> JSON marshal ->
//	JSON unmarshal -> SetDefaultValuesNodePool (simulating constructor) -> ConvertToInternal
func jsonRoundTripNodePool(t *testing.T, original *api.HCPOpenShiftClusterNodePool) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	v := version{}
	ext := v.NewHCPOpenShiftClusterNodePool(original)

	jsonBytes, err := json.Marshal(ext)
	require.NoError(t, err)

	newExt := &NodePool{}
	require.NoError(t, json.Unmarshal(jsonBytes, newExt))
	SetDefaultValuesNodePool(newExt)

	result, err := newExt.ConvertToInternal(nil)
	require.NoError(t, err)
	return result
}

// jsonRoundTripCluster simulates a GET-then-PUT cycle through JSON.
func jsonRoundTripCluster(t *testing.T, original *api.HCPOpenShiftCluster) *api.HCPOpenShiftCluster {
	t.Helper()
	v := version{}
	ext := v.NewHCPOpenShiftCluster(original)

	jsonBytes, err := json.Marshal(ext)
	require.NoError(t, err)

	newExt := &HcpOpenShiftCluster{}
	require.NoError(t, json.Unmarshal(jsonBytes, newExt))
	SetDefaultValuesCluster(newExt)

	result, err := newExt.ConvertToInternal(nil)
	require.NoError(t, err)
	return result
}

// newBaselineInternalNodePool creates a valid node pool with all potentially
// unsafe fields set to non-zero values. Test cases mutate specific fields
// to zero before round-tripping.
func newBaselineInternalNodePool() *api.HCPOpenShiftClusterNodePool {
	return &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   api.Must(azcorearm.ParseResourceID(strings.ToLower("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/myCluster/nodePools/myNodePool"))),
				Name: "myNodePool",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools",
			},
			Location: "eastus",
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           "4.15.1",
				ChannelGroup: "stable",
			},
			Platform: api.NodePoolPlatformProfile{
				SubnetID: api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet")),
				VMSize:   "Standard_D2s_v3",
				OSDisk: api.OSDiskProfile{
					SizeGiB:                ptr.To(int32(128)),
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
				},
			},
			Replicas:   3,
			AutoRepair: true,
			Labels:     map[string]string{},
			Taints:     []api.Taint{},
		},
	}
}

// newBaselineInternalCluster creates a valid cluster with all potentially
// unsafe fields set to non-zero values. Test cases mutate specific fields
// to zero before round-tripping.
func newBaselineInternalCluster() *api.HCPOpenShiftCluster {
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   api.Must(azcorearm.ParseResourceID(strings.ToLower("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/myCluster"))),
				Name: "myCluster",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
			},
			Location: "eastus",
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Version: api.VersionProfile{
				ID:           "4.15.1",
				ChannelGroup: "stable",
			},
			Network: api.NetworkProfile{
				NetworkType: api.NetworkTypeOVNKubernetes,
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
				HostPrefix:  23,
			},
			API: api.CustomerAPIProfile{
				Visibility: api.VisibilityPublic,
			},
			Platform: api.CustomerPlatformProfile{
				OutboundType: api.OutboundTypeLoadBalancer,
				SubnetID:     api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet")),
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:               100,
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 900,
				PodPriorityThreshold:        -10,
			},
			NodeDrainTimeoutMinutes: 30,
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
				},
			},
			ClusterImageRegistry: api.ClusterImageRegistryProfile{
				State: api.ClusterImageRegistryStateEnabled,
			},
		},
	}
}
