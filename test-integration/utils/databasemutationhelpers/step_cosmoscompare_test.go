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

package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestFixtureContainerRoundTrip(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		var storage integrationutils.StorageIntegrationTestInfo
		var err error
		artifactDir := t.TempDir()
		if withMock {
			storage, err = integrationutils.NewMockCosmosFromTestingEnv(t.Context(), t)
			require.NoError(t, err)
			storage.(*integrationutils.MockCosmosIntegrationTestInfo).ArtifactsDir = artifactDir
		} else {
			storage, err = integrationutils.NewCosmosFromTestingEnv(t.Context(), t)
			require.NoError(t, err)
			storage.(*integrationutils.CosmosIntegrationTestInfo).ArtifactsDir = artifactDir
		}
		t.Cleanup(func() { storage.Cleanup(context.WithoutCancel(t.Context())) })
		fixtures := fstest.MapFS{
			"resources.json": &fstest.MapFile{Data: []byte(`{"id":"shared","partitionKey":"partition","resourceType":"Microsoft.RedHatOpenShift/hcpOpenShiftClusters","properties":{"status":{"value":"resource"}}}`)},
			"fleet.json":     &fstest.MapFile{Data: []byte(`{"id":"shared","partitionKey":"partition","resourceType":"Microsoft.RedHatOpenShift/stamps/managementClusters","properties":{"status":{"aksResourceID":"original"}}}`)},
		}
		require.NoError(t, integrationutils.LoadAllContent(t.Context(), storage, fixtures))
		t.Run("list", func(t *testing.T) {
			documents, err := storage.ListAllDocuments(t.Context())
			require.NoError(t, err)
			require.Len(t, documents, 2, "both physical containers must be enumerated even when IDs coincide")
		})
		t.Run("compare", func(t *testing.T) {
			step, err := NewCosmosCompareStep(NewStepID(1, "cosmosCompare", "both-containers"), fixtures)
			require.NoError(t, err)
			step.RunTest(t.Context(), t, *NewCosmosStepInput(storage))
			var expected, changed cosmosstorageutils.TypedDocument
			require.NoError(t, json.Unmarshal(fixtures["fleet.json"].Data, &expected))
			require.NoError(t, json.Unmarshal([]byte(`{"id":"shared","partitionKey":"partition","resourceType":"Microsoft.RedHatOpenShift/stamps/managementClusters","properties":{"status":{"aksResourceID":"changed"}}}`), &changed))
			_, equal := ResourceInstanceEquals(t, &expected, &changed)
			require.False(t, equal, "comparison normalization must retain Fleet status")
		})
		t.Run("save", func(t *testing.T) {
			storage.Cleanup(t.Context())
			for container, fixture := range map[string]string{"Resources": "resources.json", "Fleet": "fleet.json"} {
				content, err := os.ReadFile(filepath.Join(artifactDir, "cosmos-content", container, "unknown", "unknown-type-shared.json"))
				require.NoError(t, err, "artifacts must retain their physical container")
				var expected, actual cosmosstorageutils.TypedDocument
				require.NoError(t, json.Unmarshal(fixtures[fixture].Data, &expected))
				require.NoError(t, json.Unmarshal(content, &actual))
				diff, equal := ResourceInstanceEquals(t, &expected, &actual)
				require.True(t, equal, diff)
				require.NoError(t, storage.LoadContent(t.Context(), content), "saved artifacts must remain loadable")
			}
			step, err := NewCosmosCompareStep(NewStepID(2, "cosmosCompare", "saved-content"), fixtures)
			require.NoError(t, err)
			step.RunTest(t.Context(), t, *NewCosmosStepInput(storage))
		})
		if !withMock {
			t.Run("write-error", func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				for _, fixture := range fixtures {
					err := storage.LoadContent(ctx, fixture.Data)
					require.ErrorIs(t, err, context.Canceled)
					require.ErrorContains(t, err, "failed to create item")
				}
			})
			t.Run("query-error", func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				documents, err := storage.ListAllDocuments(ctx)
				require.Error(t, err)
				require.Nil(t, documents, "do not return a partial container listing on query failure")
			})
		}
	})
}
