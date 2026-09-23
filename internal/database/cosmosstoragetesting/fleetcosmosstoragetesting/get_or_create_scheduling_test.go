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

package fleetcosmosstoragetesting

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
)

func TestGetOrCreateManagementClusterScheduling(t *testing.T) {
	tests := []struct {
		name            string
		stampIdentifier string
		preCreate       bool
	}{
		{
			name:            "creates new scheduling document when none exists",
			stampIdentifier: "eastus",
			preCreate:       false,
		},
		{
			name:            "returns existing scheduling document",
			stampIdentifier: "eastus",
			preCreate:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fleetDB := NewMockFleetDBClient()

			if test.preCreate {
				scheduling := &fleetapi.ManagementClusterScheduling{}
				err := fleetDB.addManagementClusterScheduling(ctx, scheduling)
				require.Error(t, err, "empty partition key should fail")

				first, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, fleetDB, test.stampIdentifier)
				require.NoError(t, err)
				require.NotNil(t, first)
			}

			result, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, fleetDB, test.stampIdentifier)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, fleetapi.SchedulingResourceName, result.ResourceID.Name)

			second, err := fleetcosmosstorage.GetOrCreateManagementClusterScheduling(ctx, fleetDB, test.stampIdentifier)
			require.NoError(t, err)
			assert.Equal(t, result.InstanceVersion, second.InstanceVersion, "second call returns same document")
		})
	}
}
