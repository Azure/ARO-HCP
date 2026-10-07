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

package corecosmosstoragetesting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func TestOpenShiftVersionCatalogCRUD(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rid, err := coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	require.Equal(t, "/providers/microsoft.redhatopenshift/openshiftversioncatalogs/default", rid.String())
	catalog := &coreapi.OpenShiftVersionCatalog{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(coreapi.ProviderNamespace)},
		Entries:        []coreapi.OpenShiftVersionCatalogEntry{{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}, Available: true}},
	}
	require.Equal(t, catalog.PartitionKey, cosmosstorageutils.DerivePartitionKey(catalog))
	db, err := NewMockResourcesDBClientWithResources(ctx, []any{catalog})
	require.NoError(t, err)
	crud := db.OpenShiftVersionCatalogs()
	stored, err := crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(catalog.Entries, stored.Entries), "create must persist catalog entries")
	require.Equal(t, int64(1), stored.InstanceVersion)
	require.NotEmpty(t, stored.CosmosETag)
	byID, err := crud.GetByID(ctx, stored.GetCosmosUID())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(stored, byID, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "GetByID must return the stored catalog")

	copy := stored.DeepCopy()
	copy.Entries[0].Available = false
	copy.ResourceID.Name = "changed"
	require.True(t, stored.Entries[0].Available, "deepcopy must not alias entries")
	require.Equal(t, coreapi.OpenShiftVersionCatalogName, stored.ResourceID.Name, "deepcopy must not alias metadata")

	replacement := stored.DeepCopy()
	replacement.Entries = []coreapi.OpenShiftVersionCatalogEntry{}
	updated, err := crud.Replace(ctx, replacement, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.InstanceVersion)
	require.Equal(t, stored.GetCosmosUID(), updated.GetCosmosUID())
	require.Equal(t, stored.PartitionKey, updated.PartitionKey)
	require.NotEqual(t, stored.CosmosETag, updated.CosmosETag)
	_, err = crud.Replace(ctx, stored.DeepCopy(), nil)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "stale updates must be rejected")

	raw, ok := db.GetDocument(updated.GetCosmosUID())
	require.True(t, ok)
	var document cosmosstorageutils.GenericDocument[coreapi.OpenShiftVersionCatalog]
	require.NoError(t, json.Unmarshal(raw, &document))
	require.Equal(t, strings.ToLower(coreapi.ProviderNamespace), document.PartitionKey)
	require.Equal(t, strings.ToLower(coreapi.OpenShiftVersionCatalogResourceType.String()), strings.ToLower(document.ResourceType))
	require.NotNil(t, document.Content.Entries, "an empty catalog must serialize as []")
	require.Zero(t, document.TimeToLive)

	list, err := crud.List(ctx, nil)
	require.NoError(t, err)
	count := 0
	for _, item := range list.Items(ctx) {
		count++
		require.Empty(t, cmp.Diff(updated, item, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "list must return the updated catalog")
	}
	require.NoError(t, list.GetError())
	require.Equal(t, 1, count)
	require.NoError(t, crud.Delete(ctx, coreapi.OpenShiftVersionCatalogName))
	_, err = crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
}
