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

package nodepool

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestCurrentPoolStates(t *testing.T) {
	tests := []struct {
		name        string
		pools       []armcontainerservice.AgentPool
		skuMetadata map[string]*skucache.SKUMetadata
		expected    []PoolState
	}{
		{
			name:     "empty input returns nil",
			pools:    nil,
			expected: nil,
		},
		{
			name: "pool without role label is filtered out",
			pools: []armcontainerservice.AgentPool{
				makeAgentPool("nodepool1", "Standard_D4s_v3", []string{"1"}, 100, 3, false, 0, 0, nil),
			},
			expected: nil,
		},
		{
			name: "system pool with role label is included",
			pools: []armcontainerservice.AgentPool{
				makeAgentPool("s1abc", "Standard_D4s_v3", []string{"1"}, 32, 2, true, 1, 3, map[string]*string{
					compute.RoleLabel: ptr.To("system"),
				}),
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleSystem, Name: "s1abc", Spec: compute.VMSpec{Size: "Standard_D4s_v3"},
						AgentPoolMode:     armcontainerservice.AgentPoolModeUser,
						AvailabilityZones: []string{"1"}, MaxCount: 3, OSDiskSizeGB: 32,
						Labels: map[string]string{compute.RoleLabel: "system"},
					},
					AutoScalingEnabled: true,
					Count:              2,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-s1abc",
					MinCount:           1,
				},
			},
		},
		{
			name: "worker pool with autoscaling projected correctly",
			pools: []armcontainerservice.AgentPool{
				makeWorkerAgentPool("w1abc", "Standard_E32ds_v6", []string{"1"}, 512, 2, true, 1, 14),
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleWorker, Name: "w1abc", Spec: compute.VMSpec{Size: "Standard_E32ds_v6"},
						AgentPoolMode:     armcontainerservice.AgentPoolModeUser,
						AvailabilityZones: []string{"1"}, MaxCount: 14, OSDiskSizeGB: 512,
						Labels: map[string]string{compute.RoleLabel: "worker"},
					},
					AutoScalingEnabled: true,
					Count:              2,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-w1abc",
					MinCount:           1,
				},
			},
		},
		{
			name: "worker pool without autoscaling uses count as maxCount",
			pools: []armcontainerservice.AgentPool{
				makeWorkerAgentPool("w1abc", "Standard_E32ds_v6", []string{"1"}, 512, 5, false, 0, 0),
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleWorker, Name: "w1abc", Spec: compute.VMSpec{Size: "Standard_E32ds_v6"},
						AgentPoolMode:     armcontainerservice.AgentPoolModeUser,
						AvailabilityZones: []string{"1"}, MaxCount: 5, OSDiskSizeGB: 512,
						Labels: map[string]string{compute.RoleLabel: "worker"},
					},
					AutoScalingEnabled: false,
					Count:              5,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-w1abc",
				},
			},
		},
		{
			name: "pool with nil properties is skipped",
			pools: []armcontainerservice.AgentPool{
				{Name: ptr.To("broken"), Properties: nil},
			},
			expected: nil,
		},
		{
			name: "configured Swift NIC count preserves the SKU maximum",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E16ds_v6": {Name: "Standard_E16ds_v6", Family: "StandardEdsv6Family", VCPUs: 16, MemoryBytes: memoryBytes("128Gi"), SecondaryNICs: 7},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Name: ptr.To("wrk161"),
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:            ptr.To("Standard_E16ds_v6"),
						Mode:              ptr.To(armcontainerservice.AgentPoolModeUser),
						AvailabilityZones: []*string{ptr.To("1")},
						OSDiskSizeGB:      ptr.To(int32(256)),
						Count:             ptr.To(int32(3)),
						EnableAutoScaling: ptr.To(true),
						MinCount:          ptr.To(int32(1)),
						MaxCount:          ptr.To(int32(10)),
						MaxPods:           ptr.To(int32(225)),
						ProvisioningState: ptr.To("Succeeded"),
						ETag:              ptr.To("etag-wrk161"),
						NodeLabels:        map[string]*string{compute.RoleLabel: ptr.To("worker"), "workload": ptr.To("general")},
						NodeTaints:        []*string{ptr.To("dedicated=worker:NoSchedule")},
						Tags: map[string]*string{
							agentpoolspec.SwiftMultiTenancyTag:      ptr.To("true"),
							agentpoolspec.SwiftSecondaryNICCountTag: ptr.To("3"),
						},
					},
				},
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleWorker, Name: "wrk161",
						AgentPoolMode:     armcontainerservice.AgentPoolModeUser,
						SecondaryNICs:     3,
						Spec:              compute.VMSpec{Size: "Standard_E16ds_v6", Family: "StandardEdsv6Family", VCPUs: 16, MemoryBytes: memoryBytes("128Gi"), SecondaryNICs: 7},
						AvailabilityZones: []string{"1"}, MaxCount: 10, OSDiskSizeGB: 256, MaxPods: 225,
						Labels:      map[string]string{compute.RoleLabel: "worker", "workload": "general"},
						Taints:      []string{"dedicated=worker:NoSchedule"},
						EnableSwift: true,
					},
					AutoScalingEnabled: true,
					Count:              3,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-wrk161",
					MinCount:           1,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := currentPoolStates(test.pools, test.skuMetadata)
			require.NoError(t, err)
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestUnresolvedSKUSizes(t *testing.T) {
	tests := []struct {
		name    string
		current []PoolState
		want    []string
	}{
		{
			name:    "empty input returns nil",
			current: nil,
			want:    nil,
		},
		{
			name: "all resolved returns nil",
			current: []PoolState{
				{Pool: compute.Pool{Spec: compute.VMSpec{Size: "Standard_E16ds_v6", VCPUs: 16}}},
			},
			want: nil,
		},
		{
			name: "unresolved SKU (zero vCPUs) is surfaced",
			current: []PoolState{
				{Pool: compute.Pool{Spec: compute.VMSpec{Size: "Standard_E16ds_v6", VCPUs: 16}}},
				{Pool: compute.Pool{Spec: compute.VMSpec{Size: "Standard_Unknown_v9"}}},
			},
			want: []string{"Standard_Unknown_v9"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, unresolvedSKUSizes(test.current))
		})
	}
}

func makeWorkerAgentPool(name, vmSize string, zones []string, osDiskSizeGB, count int32, autoScale bool, minCount, maxCount int32) armcontainerservice.AgentPool {
	labels := map[string]*string{
		compute.RoleLabel: ptr.To(string(compute.PoolRoleWorker)),
	}
	return makeAgentPool(name, vmSize, zones, osDiskSizeGB, count, autoScale, minCount, maxCount, labels)
}

func makeAgentPool(name, vmSize string, zones []string, osDiskSizeGB, count int32, autoScale bool, minCount, maxCount int32, labels map[string]*string) armcontainerservice.AgentPool {
	zonePtrs := make([]*string, len(zones))
	for i, z := range zones {
		zonePtrs[i] = ptr.To(z)
	}

	pool := armcontainerservice.AgentPool{
		Name: ptr.To(name),
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Mode:              ptr.To(armcontainerservice.AgentPoolModeUser),
			VMSize:            ptr.To(vmSize),
			AvailabilityZones: zonePtrs,
			OSDiskSizeGB:      &osDiskSizeGB,
			Count:             &count,
			EnableAutoScaling: &autoScale,
			NodeLabels:        labels,
			ProvisioningState: ptr.To("Succeeded"),
			ETag:              ptr.To("etag-" + name),
		},
	}
	if autoScale {
		pool.Properties.MinCount = &minCount
		pool.Properties.MaxCount = &maxCount
	}
	return pool
}

func TestCurrentPoolStatesSwiftNICValidation(t *testing.T) {
	tests := []struct {
		name     string
		tag      *string
		present  bool
		swift    bool
		wantNICs int64
		wantErr  bool
	}{
		{name: "configured count", tag: ptr.To("3"), present: true, swift: true, wantNICs: 3},
		{name: "missing", swift: true},
		{name: "present nil", present: true, swift: true, wantErr: true},
		{name: "empty", tag: ptr.To(""), present: true, swift: true, wantErr: true},
		{name: "malformed", tag: ptr.To("unknown"), present: true, swift: true, wantErr: true},
		{name: "zero", tag: ptr.To("0"), present: true, swift: true, wantErr: true},
		{name: "negative", tag: ptr.To("-1"), present: true, swift: true, wantErr: true},
		{name: "overflow", tag: ptr.To("9223372036854775808"), present: true, swift: true, wantErr: true},
		{name: "NICs without Swift", tag: ptr.To("3"), present: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pools := []armcontainerservice.AgentPool{{Name: ptr.To("worker"), Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
				VMSize: ptr.To("sku"), OSDiskSizeGB: ptr.To[int32](32), Count: ptr.To[int32](1), MaxCount: ptr.To[int32](2), EnableAutoScaling: ptr.To(true),
				NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("custom")}, Tags: map[string]*string{},
			}}}
			if test.swift {
				pools[0].Properties.Tags[agentpoolspec.SwiftMultiTenancyTag] = ptr.To("true")
			}
			if test.present {
				pools[0].Properties.Tags[agentpoolspec.SwiftSecondaryNICCountTag] = test.tag
			}
			metadata := map[string]*skucache.SKUMetadata{"sku": {Name: "sku", VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), SecondaryNICs: 7}}
			current, err := currentPoolStates(pools, metadata)
			if test.wantErr {
				require.Error(t, err)
				require.Nil(t, current, "invalid configured NICs must not return SKU-maximum capacity")
			} else {
				require.NoError(t, err)
				require.Len(t, current, 1)
				require.Equal(t, test.wantNICs, current[0].SecondaryNICs)
				require.Equal(t, int64(7), current[0].Spec.SecondaryNICs)
			}
		})
	}
}

func TestCurrentPoolStatesOSDiskTypeValidation(t *testing.T) {
	tests := []struct {
		name     string
		diskType *armcontainerservice.OSDiskType
		wantErr  bool
	}{
		{name: "ephemeral is projected", diskType: ptr.To(armcontainerservice.OSDiskTypeEphemeral)},
		{name: "unreported disk type is projected", diskType: nil},
		{name: "managed is rejected", diskType: ptr.To(armcontainerservice.OSDiskTypeManaged), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pools := []armcontainerservice.AgentPool{{Name: ptr.To("worker"), Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
				VMSize: ptr.To("sku"), OSDiskSizeGB: ptr.To[int32](32), OSDiskType: test.diskType,
				Count: ptr.To[int32](1), MaxCount: ptr.To[int32](2), EnableAutoScaling: ptr.To(true),
				NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")},
			}}}
			metadata := map[string]*skucache.SKUMetadata{"sku": {Name: "sku", VCPUs: 4, MemoryBytes: memoryBytes("16Gi")}}
			current, err := currentPoolStates(pools, metadata)
			if test.wantErr {
				require.ErrorContains(t, err, "expected \"Ephemeral\"")
				require.Nil(t, current, "a managed-disk pool must not be projected as a comparable pool")
				return
			}
			require.NoError(t, err)
			require.Len(t, current, 1)
		})
	}
}

func TestUnfreezeObservedPoolMinimum(t *testing.T) {
	for _, test := range []struct {
		name        string
		role        compute.PoolRole
		mode        armcontainerservice.AgentPoolMode
		minCount    *int32
		wantMinimum int32
	}{
		{name: "system mode with arbitrary role", role: "control", mode: armcontainerservice.AgentPoolModeSystem, wantMinimum: 1},
		{name: "user mode with system role preserves zero minimum", role: compute.PoolRoleSystem, mode: armcontainerservice.AgentPoolModeUser, minCount: ptr.To[int32](0), wantMinimum: 0},
		{name: "system mode preserves positive minimum", role: "custom", mode: armcontainerservice.AgentPoolModeSystem, minCount: ptr.To[int32](2), wantMinimum: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := makeAgentPool("existing", specD4v3.Size, []string{"1"}, 32, 3, false, 0, 0,
				map[string]*string{compute.RoleLabel: ptr.To(string(test.role))})
			observed.Properties.MinCount = test.minCount
			observed.Properties.Mode = ptr.To(test.mode)
			metadata := map[string]*skucache.SKUMetadata{
				specD4v3.Size: {Name: specD4v3.Size, Family: string(specD4v3.Family), VCPUs: specD4v3.VCPUs, MemoryBytes: specD4v3.MemoryBytes},
			}
			current, err := currentPoolStates([]armcontainerservice.AgentPool{observed}, metadata)
			require.NoError(t, err)
			desired := current[0].Pool
			desired.MinCount = 1
			tr := requireSimulation(t, []compute.Pool{desired}, current, generousBudgets([]compute.Pool{desired}, current), true, 10)
			require.Equal(t, "converged", tr.Outcome)
			require.Len(t, tr.Steps, 1)
			action, ok := tr.Steps[0].Action.(unfreezeAction)
			require.True(t, ok, "a non-autoscaled pool must be unfrozen")
			require.Equal(t, test.wantMinimum, action.MinCount)
			require.Equal(t, test.wantMinimum, tr.finalState()[0].MinCount)
		})
	}
}

func TestPoolZonesByRole(t *testing.T) {
	agentPool := func(role string, zones ...string) armcontainerservice.AgentPool {
		properties := &armcontainerservice.ManagedClusterAgentPoolProfileProperties{}
		if len(role) > 0 {
			properties.NodeLabels = map[string]*string{compute.RoleLabel: ptr.To(role)}
		}
		for _, zone := range zones {
			properties.AvailabilityZones = append(properties.AvailabilityZones, ptr.To(zone))
		}
		return armcontainerservice.AgentPool{Name: ptr.To("pool"), Properties: properties}
	}
	tests := []struct {
		name  string
		pools []armcontainerservice.AgentPool
		want  map[compute.PoolRole][]string
	}{
		{
			name:  "zones of worker pools, deduplicated and sorted",
			pools: []armcontainerservice.AgentPool{agentPool("worker", "3"), agentPool("worker", "1"), agentPool("worker", "3")},
			want:  map[compute.PoolRole][]string{compute.PoolRoleWorker: {"1", "3"}},
		},
		{
			name:  "all managed roles pin their own zones",
			pools: []armcontainerservice.AgentPool{agentPool("infra", "2"), agentPool("custom", "3", "1"), agentPool("", "3")},
			want:  map[compute.PoolRole][]string{compute.PoolRoleInfra: {"2"}, "custom": {"1", "3"}},
		},
		{
			name:  "non-zonal pools pin no actual zone",
			pools: []armcontainerservice.AgentPool{agentPool("custom", ""), {Name: ptr.To("no-properties")}},
			want:  map[compute.PoolRole][]string{"custom": {}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, poolZonesByRole(test.pools))
		})
	}
}
