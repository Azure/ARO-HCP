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

func TestAddClientProperties(t *testing.T) {
	tests := []struct {
		name                  string
		provisionShardID      string
		provisionerNoOpProv   bool
		provisionerNoOpDeprov bool
		existingProperties    map[string]string
		expectedProperties    map[string]string
	}{
		{
			name:               "no client config, no existing properties",
			expectedProperties: map[string]string{},
		},
		{
			name: "preserves existing properties from builder",
			existingProperties: map[string]string{
				CSPropertySizeOverride: "true",
			},
			expectedProperties: map[string]string{
				CSPropertySizeOverride: "true",
			},
		},
		{
			name:             "provision shard ID set",
			provisionShardID: "test-shard-123",
			expectedProperties: map[string]string{
				"provision_shard_id": "test-shard-123",
			},
		},
		{
			name:                "provisioner noop provision enabled",
			provisionerNoOpProv: true,
			expectedProperties: map[string]string{
				"provisioner_noop_provision": "true",
			},
		},
		{
			name:                  "provisioner noop deprovision enabled",
			provisionerNoOpDeprov: true,
			expectedProperties: map[string]string{
				"provisioner_noop_deprovision": "true",
			},
		},
		{
			name:                  "client properties merged with existing builder properties",
			provisionShardID:      "test-shard-456",
			provisionerNoOpProv:   true,
			provisionerNoOpDeprov: true,
			existingProperties: map[string]string{
				CSPropertySizeOverride: "true",
			},
			expectedProperties: map[string]string{
				"provision_shard_id":           "test-shard-456",
				"provisioner_noop_provision":   "true",
				"provisioner_noop_deprovision": "true",
				CSPropertySizeOverride:         "true",
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
			if tt.existingProperties != nil {
				builder.Properties(tt.existingProperties)
			}
			inputCluster, err := builder.Build()
			require.NoError(t, err)

			result, err := client.addClientProperties(inputCluster)
			require.NoError(t, err)

			props, _ := result.GetProperties()
			assert.Equal(t, tt.expectedProperties, props)
		})
	}
}
