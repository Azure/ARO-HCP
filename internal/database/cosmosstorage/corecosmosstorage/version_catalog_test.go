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

package corecosmosstorage

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestOpenShiftVersionCatalogAccessor(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rid, err := coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	uid, err := coreapi.ResourceIDToCosmosID(rid)
	require.NoError(t, err)
	catalog := &coreapi.OpenShiftVersionCatalog{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(coreapi.ProviderNamespace)},
		Entries:        []coreapi.OpenShiftVersionCatalogEntry{},
	}
	credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
	require.NoError(t, err)
	var stored []byte
	requests := 0
	options := cosmosclient.Options{KeyCredential: &credential, ClientOptions: azcore.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: catalogTransport(func(req *http.Request) (*http.Response, error) {
			respond := func(code int, body string) (*http.Response, error) {
				return &http.Response{StatusCode: code, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			if req.URL.Path == "" || req.URL.Path == "/" {
				return respond(http.StatusOK, `{"id":"test"}`)
			}
			requests++
			require.Equal(t, `["microsoft.redhatopenshift"]`, req.Header.Get("x-ms-documentdb-partitionkey"))
			if req.Method == http.MethodPost {
				require.Equal(t, "/dbs/test/colls/Resources/docs", req.URL.Path)
			} else {
				require.Equal(t, "/dbs/test/colls/Resources/docs/"+uid, req.URL.Path)
			}
			if req.Method == http.MethodGet {
				if stored == nil {
					return respond(http.StatusNotFound, `{"code":"NotFound"}`)
				}
				return respond(http.StatusOK, string(stored))
			}
			require.Contains(t, []string{http.MethodPost, http.MethodPut}, req.Method)
			var document cosmosstorageutils.GenericDocument[coreapi.OpenShiftVersionCatalog]
			require.NoError(t, json.NewDecoder(req.Body).Decode(&document))
			require.Equal(t, uid, document.ID)
			require.Equal(t, strings.ToLower(coreapi.ProviderNamespace), document.PartitionKey)
			require.Equal(t, rid.String(), document.ResourceID.String())
			require.Equal(t, rid.String(), document.Content.ResourceID.String())
			require.NotNil(t, document.Content.Entries)
			if req.Method == http.MethodPut {
				require.Equal(t, "first-etag", req.Header.Get("If-Match"), "Replace must be conditional")
				require.Equal(t, int64(2), document.Content.InstanceVersion)
				document.CosmosETag = "second-etag"
			} else {
				require.Equal(t, int64(1), document.Content.InstanceVersion)
				document.CosmosETag = "first-etag"
			}
			stored, err = json.Marshal(document)
			require.NoError(t, err)
			return respond(http.StatusOK, string(stored))
		}),
	}}
	bucket, err := cosmosratelimit.NewTokenBucket("version-catalog-test", 1000, 1000)
	require.NoError(t, err)
	db, err := NewResourcesDBClient("https://cosmos.test", "test", options, bucket)
	require.NoError(t, err)
	crud := db.OpenShiftVersionCatalogs()
	_, err = crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
	created, err := crud.Create(ctx, catalog, nil)
	require.NoError(t, err)
	require.Equal(t, azcore.ETag("first-etag"), created.CosmosETag)
	read, err := crud.Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(created, read, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "read must return the created catalog")
	replacement := read.DeepCopy()
	replacement.Entries = []coreapi.OpenShiftVersionCatalogEntry{{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}}}
	updated, err := crud.Replace(ctx, replacement, nil)
	require.NoError(t, err)
	require.Equal(t, azcore.ETag("second-etag"), updated.CosmosETag)
	require.Empty(t, cmp.Diff(replacement.Entries, updated.Entries), "replace must persist catalog entries")
	require.Equal(t, 4, requests)
}
