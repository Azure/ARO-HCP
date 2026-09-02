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

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestCurrentPoolStates(t *testing.T) {
	tests := []struct {
		name     string
		pools    []armcontainerservice.AgentPool
		expected []PoolState
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
					agentpools.RoleLabel: ptr.To("system"),
				}),
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleSystem, Name: "s1abc", Spec: compute.VMSpec{Size: "Standard_D4s_v3"},
						AvailabilityZones: []string{"1"}, MaxCount: 3, OSDiskSizeGB: 32,
						Labels: map[string]string{agentpools.RoleLabel: "system"},
					},
					AutoScalingEnabled: true,
					Count:              2,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-s1abc",
				},
			},
		},
		{
			name: "worker pool with autoscaling projected correctly",
			pools: []armcontainerservice.AgentPool{
				makeWorkerAgentPool("s1abc", "Standard_E32ds_v6", []string{"1"}, 512, 2, true, 1, 14),
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleWorker, Name: "s1abc", Spec: compute.VMSpec{Size: "Standard_E32ds_v6"},
						AvailabilityZones: []string{"1"}, MaxCount: 14, OSDiskSizeGB: 512,
						Labels: map[string]string{agentpools.RoleLabel: "worker"},
					},
					AutoScalingEnabled: true,
					Count:              2,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-s1abc",
				},
			},
		},
		{
			name: "worker pool without autoscaling uses count as maxCount",
			pools: []armcontainerservice.AgentPool{
				makeWorkerAgentPool("s1abc", "Standard_E32ds_v6", []string{"1"}, 512, 5, false, 0, 0),
			},
			expected: []PoolState{
				{
					Pool: compute.Pool{
						Role: compute.PoolRoleWorker, Name: "s1abc", Spec: compute.VMSpec{Size: "Standard_E32ds_v6"},
						AvailabilityZones: []string{"1"}, MaxCount: 5, OSDiskSizeGB: 512,
						Labels: map[string]string{agentpools.RoleLabel: "worker"},
					},
					AutoScalingEnabled: false,
					Count:              5,
					ProvisioningState:  "Succeeded",
					ETag:               "etag-s1abc",
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := currentPoolStates(test.pools, nil)
			assert.Equal(t, test.expected, result)
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
		agentpools.RoleLabel: ptr.To(agentpools.WorkerPoolLabelValue),
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
