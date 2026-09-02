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

package agentpools

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestPoolRole(t *testing.T) {
	tests := []struct {
		name string
		pool armcontainerservice.AgentPool
		want string
	}{
		{
			name: "nil Properties returns empty",
			pool: armcontainerservice.AgentPool{},
			want: "",
		},
		{
			name: "nil NodeLabels returns empty",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{},
			},
			want: "",
		},
		{
			name: "missing label key returns empty",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						"some-other-label": ptr.To("value"),
					},
				},
			},
			want: "",
		},
		{
			name: "nil label value returns empty",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: nil,
					},
				},
			},
			want: "",
		},
		{
			name: "worker role",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: ptr.To("worker"),
					},
				},
			},
			want: "worker",
		},
		{
			name: "system role",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: ptr.To("system"),
					},
				},
			},
			want: "system",
		},
		{
			name: "infra role",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: ptr.To("infra"),
					},
				},
			},
			want: "infra",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, PoolRole(test.pool))
		})
	}
}

func TestIsManagedPool(t *testing.T) {
	tests := []struct {
		name string
		pool armcontainerservice.AgentPool
		want bool
	}{
		{
			name: "no role label returns false",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{},
				},
			},
			want: false,
		},
		{
			name: "worker pool is managed",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: ptr.To("worker"),
					},
				},
			},
			want: true,
		},
		{
			name: "system pool is managed",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: ptr.To("system"),
					},
				},
			},
			want: true,
		},
		{
			name: "infra pool is managed",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						compute.RoleLabel: ptr.To("infra"),
					},
				},
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, IsManagedPool(test.pool))
		})
	}
}

func TestPoolMaxCount(t *testing.T) {
	tests := []struct {
		name string
		pool armcontainerservice.AgentPool
		want int64
	}{
		{
			name: "nil properties returns zero",
			pool: armcontainerservice.AgentPool{},
			want: 0,
		},
		{
			name: "nil count returns zero",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{},
			},
			want: 0,
		},
		{
			name: "autoscaling disabled uses count",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Count:             ptr.To[int32](5),
					EnableAutoScaling: ptr.To(false),
					MaxCount:          ptr.To[int32](10),
				},
			},
			want: 5,
		},
		{
			name: "autoscaling enabled uses maxCount",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Count:             ptr.To[int32](5),
					EnableAutoScaling: ptr.To(true),
					MaxCount:          ptr.To[int32](10),
				},
			},
			want: 10,
		},
		{
			name: "autoscaling enabled but maxCount nil falls back to count",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Count:             ptr.To[int32](5),
					EnableAutoScaling: ptr.To(true),
					MaxCount:          nil,
				},
			},
			want: 5,
		},
		{
			name: "nil EnableAutoScaling falls back to count",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					Count:             ptr.To[int32](5),
					EnableAutoScaling: nil,
					MaxCount:          ptr.To[int32](10),
				},
			},
			want: 5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, PoolMaxCount(test.pool))
		})
	}
}
