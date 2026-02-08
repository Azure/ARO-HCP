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

package ocm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
)

func TestAddProperties(t *testing.T) {
	tests := []struct {
		name                  string
		provisionShardID      string
		provisionerNoOpProv   bool
		provisionerNoOpDeprov bool
		opts                  *ClusterOptions
		expectedProperties    map[string]string
	}{
		{
			name:               "nil opts - no properties set",
			opts:               nil,
			expectedProperties: map[string]string{},
		},
		{
			name: "MinimalResourceRequests false - no properties set",
			opts: &ClusterOptions{
				MinimalResourceRequests: false,
			},
			expectedProperties: map[string]string{},
		},
		{
			name: "MinimalResourceRequests true - property set",
			opts: &ClusterOptions{
				MinimalResourceRequests: true,
			},
			expectedProperties: map[string]string{
				"hosted_cluster_minimal_resource_requests": "true",
			},
		},
		{
			name:             "provision shard ID set",
			provisionShardID: "test-shard-123",
			opts:             nil,
			expectedProperties: map[string]string{
				"provision_shard_id": "test-shard-123",
			},
		},
		{
			name:                "provisioner noop provision enabled",
			provisionerNoOpProv: true,
			opts:                nil,
			expectedProperties: map[string]string{
				"provisioner_noop_provision": "true",
			},
		},
		{
			name:                  "provisioner noop deprovision enabled",
			provisionerNoOpDeprov: true,
			opts:                  nil,
			expectedProperties: map[string]string{
				"provisioner_noop_deprovision": "true",
			},
		},
		{
			name:                  "all properties combined",
			provisionShardID:      "test-shard-456",
			provisionerNoOpProv:   true,
			provisionerNoOpDeprov: true,
			opts: &ClusterOptions{
				MinimalResourceRequests: true,
			},
			expectedProperties: map[string]string{
				"provision_shard_id":                       "test-shard-456",
				"provisioner_noop_provision":               "true",
				"provisioner_noop_deprovision":             "true",
				"hosted_cluster_minimal_resource_requests": "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &clusterServiceClient{
				provisionShardID:           tt.provisionShardID,
				provisionerNoOpProvision:   tt.provisionerNoOpProv,
				provisionerNoOpDeprovision: tt.provisionerNoOpDeprov,
			}

			builder := arohcpv1alpha1.NewCluster()
			result := client.addProperties(builder, tt.opts)

			cluster, err := result.Build()
			require.NoError(t, err)

			props, _ := cluster.GetProperties()
			assert.Equal(t, tt.expectedProperties, props)
		})
	}
}

func TestAddProperties_PreservesExistingBuilderState(t *testing.T) {
	// Ensure addProperties doesn't interfere with other builder state
	client := &clusterServiceClient{
		provisionShardID: "test-shard",
	}

	opts := &ClusterOptions{
		MinimalResourceRequests: true,
	}

	// Set some initial builder state
	builder := arohcpv1alpha1.NewCluster().
		Name("test-cluster").
		Region(arohcpv1alpha1.NewCloudRegion().ID("eastus"))

	result := client.addProperties(builder, opts)

	cluster, err := result.Build()
	require.NoError(t, err)

	// Verify properties are set
	props, ok := cluster.GetProperties()
	require.True(t, ok)
	assert.Equal(t, "true", props["hosted_cluster_minimal_resource_requests"])
	assert.Equal(t, "test-shard", props["provision_shard_id"])

	// Verify other builder state is preserved
	name, ok := cluster.GetName()
	assert.True(t, ok)
	assert.Equal(t, "test-cluster", name)

	region, ok := cluster.GetRegion()
	assert.True(t, ok)
	regionID, ok := region.GetID()
	assert.True(t, ok)
	assert.Equal(t, "eastus", regionID)
}
