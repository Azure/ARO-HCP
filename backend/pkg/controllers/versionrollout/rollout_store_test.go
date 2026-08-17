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

package versionrollout

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
)

// TestFleetRolloutWriter_RoundTrip exercises the real fleet Cosmos CRUD wiring:
// seed a rollout, replace it through NewFleetRolloutWriter, and read it back.
func TestFleetRolloutWriter_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const channel = "stable-4.21"

	mockDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{
		newTestRollout(channel, v("4.21.4"), fleetapi.ControlPlaneVersionRolloutStatus{}),
	})
	require.NoError(t, err)

	existing, err := mockDB.ControlPlaneVersionRollouts().Get(ctx, channel)
	require.NoError(t, err)
	require.NotNil(t, existing.Spec.BestExactVersion)
	assert.True(t, existing.Spec.BestExactVersion.EQ(*v("4.21.4")))

	replacement := existing.DeepCopy()
	replacement.Spec.BestExactVersion = v("4.21.6")

	writer := NewFleetRolloutWriter(mockDB)
	_, err = writer.Replace(ctx, replacement, existing)
	require.NoError(t, err)

	after, err := mockDB.ControlPlaneVersionRollouts().Get(ctx, channel)
	require.NoError(t, err)
	require.NotNil(t, after.Spec.BestExactVersion)
	assert.True(t, after.Spec.BestExactVersion.EQ(*v("4.21.6")), "best version should be updated after Replace")
}
