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
)

func TestIsWorkerPool(t *testing.T) {
	tests := []struct {
		name string
		pool armcontainerservice.AgentPool
		want bool
	}{
		{
			name: "nil Properties returns false",
			pool: armcontainerservice.AgentPool{},
			want: false,
		},
		{
			name: "nil NodeLabels returns false",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{},
			},
			want: false,
		},
		{
			name: "missing label key returns false",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						"some-other-label": ptr.To("value"),
					},
				},
			},
			want: false,
		},
		{
			name: "wrong label value returns false",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						WorkerPoolLabel: ptr.To("system"),
					},
				},
			},
			want: false,
		},
		{
			name: "nil label value returns false",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						WorkerPoolLabel: nil,
					},
				},
			},
			want: false,
		},
		{
			name: "correct label returns true",
			pool: armcontainerservice.AgentPool{
				Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{
						WorkerPoolLabel: ptr.To(WorkerPoolLabelValue),
					},
				},
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, IsWorkerPool(test.pool))
		})
	}
}
