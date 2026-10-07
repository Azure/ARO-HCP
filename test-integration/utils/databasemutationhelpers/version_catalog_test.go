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
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestOpenShiftVersionCatalogArtifacts(t *testing.T) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	info, err := integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
	require.NoError(t, err)
	artifactDir := t.TempDir()
	info.(*integrationutils.MockCosmosIntegrationTestInfo).ArtifactsDir = artifactDir
	input := NewCosmosStepInput(info)
	fixture := os.DirFS("../../frontend/artifacts/FrontendCRUD/OpenShiftVersions/catalog/03-loadCosmos-catalog")
	load, err := NewLoadCosmosStep(NewStepID(0, "loadCosmos", "catalog"), fixture)
	require.NoError(t, err)
	load.RunTest(ctx, t, *input)
	compare, err := NewCosmosCompareStep(NewStepID(1, "cosmosCompare", "catalog"), fixture)
	require.NoError(t, err)
	compare.RunTest(ctx, t, *input)

	rid, err := coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	crud := NewCosmosCRUD[coreapi.OpenShiftVersionCatalog](t, info.ResourcesDBClient(), rid.Parent, coreapi.OpenShiftVersionCatalogResourceType)
	catalog, err := crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	require.Equal(t, rid.String(), catalog.GetResourceID().String())
	require.Equal(t, "microsoft.redhatopenshift", catalog.PartitionKey)
	require.Len(t, catalog.Entries, 6)
	changed := catalog.DeepCopy()
	changed.Entries[0].Available = true
	_, equal := ResourceInstanceEquals(t, catalog, changed)
	require.False(t, equal, "catalog availability must participate in comparisons")

	// Cleanup exports the provider-root document, and that artifact must be reloadable.
	info.Cleanup(ctx)
	var saved []string
	require.NoError(t, filepath.WalkDir(artifactDir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			saved = append(saved, path)
		}
		return err
	}))
	require.Len(t, saved, 1)
	require.Contains(t, saved[0], "openshiftversioncatalogs")
	require.NotContains(t, saved[0], "unknown")
	content, err := os.ReadFile(saved[0])
	require.NoError(t, err)
	require.NoError(t, crud.Delete(ctx, coreapi.OpenShiftVersionCatalogName))
	_, err = crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
	require.NoError(t, info.LoadContent(ctx, content))
	compare.RunTest(ctx, t, *input)
	restored, err := crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	diff, equal := ResourceInstanceEquals(t, catalog, restored)
	require.True(t, equal, "catalog changed during artifact round trip: %s", diff)
}
