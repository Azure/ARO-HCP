// Copyright 2026 Microsoft Corporation
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

package agentpoolspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name          string
		pool          compute.Pool
		networkConfig compute.NetworkConfig
		wantMode      armcontainerservice.AgentPoolMode
		wantTags      map[string]string
	}{
		{
			name: "system role label selects system mode",
			pool: compute.Pool{
				Labels: map[string]string{compute.RoleLabel: string(compute.PoolRoleSystem)},
			},
			wantMode: armcontainerservice.AgentPoolModeSystem,
		},
		{
			name: "worker role label selects user mode",
			pool: compute.Pool{
				Labels: map[string]string{compute.RoleLabel: string(compute.PoolRoleWorker)},
			},
			wantMode: armcontainerservice.AgentPoolModeUser,
		},
		{
			name:     "no labels defaults to user mode",
			pool:     compute.Pool{},
			wantMode: armcontainerservice.AgentPoolModeUser,
		},
		{
			name: "swift enabled with secondary NICs sets multi-tenancy tags",
			pool: compute.Pool{
				EnableSwift: true,
				Spec:        compute.VMSpec{Size: "Standard_D8ds_v6", Family: "standardDDSv6Family", VCPUs: 8, MemoryGiB: 32, SecondaryNICs: 4},
			},
			wantMode: armcontainerservice.AgentPoolModeUser,
			wantTags: map[string]string{
				"aks-nic-enable-multi-tenancy": "true",
				"aks-nic-secondary-count":      "4",
			},
		},
		{
			name: "swift enabled but no secondary NICs sets no tags",
			pool: compute.Pool{
				EnableSwift: true,
				Spec:        compute.VMSpec{Size: "Standard_D8ds_v6", Family: "standardDDSv6Family", VCPUs: 8, MemoryGiB: 32, SecondaryNICs: 0},
			},
			wantMode: armcontainerservice.AgentPoolModeUser,
		},
		{
			name: "swift disabled with secondary NICs sets no tags",
			pool: compute.Pool{
				Spec: compute.VMSpec{Size: "Standard_D8ds_v6", Family: "standardDDSv6Family", VCPUs: 8, MemoryGiB: 32, SecondaryNICs: 4},
			},
			wantMode: armcontainerservice.AgentPoolModeUser,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			properties := Build(tt.pool, tt.networkConfig)
			require.NotNil(t, properties)

			require.NotNil(t, properties.Mode)
			assert.Equal(t, tt.wantMode, *properties.Mode)

			if tt.wantTags == nil {
				assert.Nil(t, properties.Tags)
				return
			}
			gotTags := make(map[string]string, len(properties.Tags))
			for k, v := range properties.Tags {
				gotTags[k] = *v
			}
			assert.Equal(t, tt.wantTags, gotTags)
		})
	}
}

// TestBuild_FullWorkerPoolPayload pins the complete ARM payload for a
// representative worker pool (taints, extra labels, Swift, non-default
// initialMinCount, a zone, and both subnets), so a regression in any field the
// narrower assertions above do not cover — OSDiskType, FIPS, EncryptionAtHost,
// MaxSurge, SecurityProfile, MinCount, MaxCount, taints — is caught. Run with
// UPDATE_GOLDEN=1 to refresh.
func TestBuild_FullWorkerPoolPayload(t *testing.T) {
	pool := compute.Pool{
		Role:              compute.PoolRoleWorker,
		Name:              "wrk16",
		Spec:              compute.VMSpec{Size: "Standard_E16ds_v6", Family: "standardEDSv6Family", VCPUs: 16, MemoryGiB: 128, SecondaryNICs: 7},
		AvailabilityZones: []string{"1"},
		MaxCount:          10,
		InitialMinCount:   2,
		OSDiskSizeGB:      256,
		MaxPods:           225,
		Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleWorker), "workload": "general"},
		Taints:            []string{"dedicated=worker:NoSchedule"},
		EnableSwift:       true,
	}
	networkConfig := compute.NetworkConfig{
		VnetSubnetID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/node",
		PodSubnetID:  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pod",
	}

	properties := Build(pool, networkConfig)

	got, err := json.MarshalIndent(properties, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	golden := filepath.Join("testdata", t.Name()+".json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, got, 0o644))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file not found: %s (run with UPDATE_GOLDEN=1 to create)", golden)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("golden mismatch (-want +got):\n%s", diff)
	}
}

func TestBuild_Zones(t *testing.T) {
	tests := []struct {
		name  string
		zones []string
	}{
		{name: "no zones", zones: nil},
		{name: "single zone", zones: []string{"1"}},
		{name: "multiple zones", zones: []string{"1", "2", "3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := compute.Pool{AvailabilityZones: tt.zones}
			properties := Build(pool, compute.NetworkConfig{})

			require.Len(t, properties.AvailabilityZones, len(tt.zones))
			for i, zone := range tt.zones {
				require.NotNil(t, properties.AvailabilityZones[i])
				assert.Equal(t, zone, *properties.AvailabilityZones[i])
			}
		})
	}
}

func TestBuild_NetworkConfig(t *testing.T) {
	tests := []struct {
		name           string
		networkConfig  compute.NetworkConfig
		wantVnetSubnet *string
		wantPodSubnet  *string
	}{
		{
			name:          "empty network config sets no subnet fields",
			networkConfig: compute.NetworkConfig{},
		},
		{
			name: "vnet subnet only",
			networkConfig: compute.NetworkConfig{
				VnetSubnetID: "/subscriptions/sub/vnetSubnet",
			},
		},
		{
			name: "vnet and pod subnet",
			networkConfig: compute.NetworkConfig{
				VnetSubnetID: "/subscriptions/sub/vnetSubnet",
				PodSubnetID:  "/subscriptions/sub/podSubnet",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			properties := Build(compute.Pool{}, tt.networkConfig)

			if len(tt.networkConfig.VnetSubnetID) == 0 {
				assert.Nil(t, properties.VnetSubnetID)
			} else {
				require.NotNil(t, properties.VnetSubnetID)
				assert.Equal(t, tt.networkConfig.VnetSubnetID, *properties.VnetSubnetID)
			}

			if len(tt.networkConfig.PodSubnetID) == 0 {
				assert.Nil(t, properties.PodSubnetID)
			} else {
				require.NotNil(t, properties.PodSubnetID)
				assert.Equal(t, tt.networkConfig.PodSubnetID, *properties.PodSubnetID)
			}
		})
	}
}
