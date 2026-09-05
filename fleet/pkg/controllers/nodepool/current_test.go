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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

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
			name: "configured Swift NIC count overrides the SKU maximum",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E16ds_v6": {Name: "Standard_E16ds_v6", Family: "standardEDSv6Family", VCPUs: 16, MemoryGB: 128, SecondaryNICs: 7},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Name: ptr.To("wrk161"),
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:            ptr.To("Standard_E16ds_v6"),
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
						Spec:              compute.VMSpec{Size: "Standard_E16ds_v6", Family: "standardEDSv6Family", VCPUs: 16, MemoryGiB: 128, SecondaryNICs: 3},
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

func TestHasSwiftTags(t *testing.T) {
	tests := []struct {
		name string
		pool armcontainerservice.AgentPool
		want bool
	}{
		{
			name: "multi-tenancy tag set to true",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Tags: map[string]*string{agentpoolspec.SwiftMultiTenancyTag: ptr.To("true")},
				},
			},
			want: true,
		},
		{
			name: "multi-tenancy tag set to false",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Tags: map[string]*string{agentpoolspec.SwiftMultiTenancyTag: ptr.To("false")},
				},
			},
			want: false,
		},
		{
			name: "tag absent",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Tags: map[string]*string{"other-tag": ptr.To("true")},
				},
			},
			want: false,
		},
		{
			name: "nil tags map",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{},
			},
			want: false,
		},
		{
			name: "nil properties",
			pool: armcontainerservice.AgentPool{},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, hasSwiftTags(test.pool))
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
		wantNICs int64
		wantErr  bool
	}{
		{name: "configured count", tag: ptr.To("3"), wantNICs: 3},
		{name: "missing", wantErr: true},
		{name: "empty", tag: ptr.To(""), wantErr: true},
		{name: "malformed", tag: ptr.To("unknown"), wantErr: true},
		{name: "zero", tag: ptr.To("0"), wantErr: true},
		{name: "negative", tag: ptr.To("-1"), wantErr: true},
		{name: "overflow", tag: ptr.To("9223372036854775808"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pools := []armcontainerservice.AgentPool{{Name: ptr.To("worker"), Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
				VMSize: ptr.To("sku"), OSDiskSizeGB: ptr.To[int32](32), Count: ptr.To[int32](1), MaxCount: ptr.To[int32](2), EnableAutoScaling: ptr.To(true),
				NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("worker")}, Tags: map[string]*string{agentpoolspec.SwiftMultiTenancyTag: ptr.To("true"), agentpoolspec.SwiftSecondaryNICCountTag: test.tag},
			}}}
			metadata := map[string]*skucache.SKUMetadata{"sku": {Name: "sku", VCPUs: 4, MemoryGB: 16, SecondaryNICs: 7}}
			current, err := currentPoolStates(pools, metadata)
			if test.wantErr {
				require.ErrorContains(t, err, "invalid Swift NIC count")
				require.Nil(t, current, "invalid configured NICs must not return SKU-maximum capacity")
			} else {
				require.NoError(t, err)
				require.Len(t, current, 1)
				require.Equal(t, test.wantNICs, current[0].Spec.SecondaryNICs)
			}
		})
	}
}
