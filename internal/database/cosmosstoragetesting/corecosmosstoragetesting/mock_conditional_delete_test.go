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

package corecosmosstoragetesting_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
)

func TestMockConditionalDeleteAllStores(t *testing.T) {
	resources := corecosmosstoragetesting.NewMockResourcesDBClient()
	fleet := fleetcosmosstoragetesting.NewMockFleetDBClient()
	kube := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	desires, err := kube.ApplyDesiresForCluster("sub", "rg", "cluster")
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		store interface {
			corecosmosstoragetesting.MockDocumentStore
			cosmosstorageutils.ChangeFeedClient
		}
		crud cosmosstorageutils.ConditionalDeleter
		id   string
	}{
		{"resources", resources, resources.Subscriptions(), "/subscriptions/sub"},
		{"fleet", fleet, fleet.HCPResourceRequirements(), "/providers/Microsoft.RedHatOpenShift/hcpResourceRequirements/test"},
		{"kube-applier", kube, desires, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/applyDesires/test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rid, err := azcorearm.ParseResourceID(tc.id)
			require.NoError(t, err)
			id, err := coreapi.ResourceIDToCosmosID(rid)
			require.NoError(t, err)
			original := json.RawMessage(fmt.Sprintf(`{"id":%q,"resourceID":%q,"_etag":"evaluated","properties":{"cosmosMetadata":{"instanceVersion":3},"status":{"count":7}}}`, id, tc.id))
			tc.store.StoreDocument(id, original)
			for _, etag := range []azcore.ETag{"stale", ""} {
				err := tc.crud.DeleteWithETag(t.Context(), rid.Name, etag)
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "mismatched ETag must return 412: %v", err)
				stored, ok := tc.store.GetDocument(id)
				require.True(t, ok)
				require.Equal(t, original, stored)
			}

			// Concurrent deletes must create only one tombstone and feed event.
			start := make(chan struct{})
			results := make(chan error, 16)
			for range cap(results) {
				go func() {
					<-start
					results <- tc.crud.DeleteWithETag(t.Context(), rid.Name, "evaluated")
				}()
			}
			close(start)
			for range cap(results) {
				require.NoError(t, <-results)
			}
			tombstone, ok := tc.store.GetDocument(id)
			require.True(t, ok)
			var doc cosmosstorageutils.GenericDocument[map[string]interface{}]
			require.NoError(t, json.Unmarshal(tombstone, &doc))
			require.NotNil(t, doc.DeletionTimestamp)
			require.EqualValues(t, cosmosstorageutils.SoftDeleteTTLSeconds, doc.TimeToLive)
			require.Equal(t, float64(4), doc.Content["cosmosMetadata"].(map[string]interface{})["instanceVersion"])
			require.Equal(t, float64(7), doc.Content["status"].(map[string]interface{})["count"])
			require.NotEmpty(t, doc.CosmosETag)
			require.NotEqual(t, azcore.ETag("evaluated"), doc.CosmosETag)
			for _, etag := range []azcore.ETag{"stale", ""} {
				require.NoError(t, tc.crud.DeleteWithETag(t.Context(), rid.Name, etag))
			}
			stored, ok := tc.store.GetDocument(id)
			require.True(t, ok)
			require.Equal(t, tombstone, stored, "idempotence must preserve TTL, timestamp, version and ETag")
			tc.store.DeleteDocument(id)
			require.NoError(t, tc.crud.DeleteWithETag(t.Context(), rid.Name, "stale"))
			require.Empty(t, tc.store.GetAllDocuments())
			feed, err := tc.store.ReadChangeFeed(t.Context(), nil)
			require.NoError(t, err)
			require.Equal(t, [][]byte{original, tombstone}, feed.Items, "only successful mutations enter the feed")

			t.Run("atomic mutation and feed ordering", func(t *testing.T) {
				tc.store.StoreDocument(id, json.RawMessage(`{"count":0}`))
				var wg sync.WaitGroup
				results := make(chan error, 32)
				for range cap(results) {
					wg.Go(func() {
						results <- tc.store.MutateDocument(id, func(data json.RawMessage) (json.RawMessage, error) {
							var doc map[string]int
							if err := json.Unmarshal(data, &doc); err != nil {
								return nil, err
							}
							doc["count"]++
							return json.Marshal(doc)
						})
					})
				}
				wg.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				feed, err := tc.store.ReadChangeFeed(t.Context(), nil)
				require.NoError(t, err)
				require.Len(t, feed.Items, 35)
				for i, item := range feed.Items[2:] {
					require.JSONEq(t, fmt.Sprintf(`{"count":%d}`, i), string(item), "feed order must match mutation order")
				}
				stored, _ := tc.store.GetDocument(id)
				require.Equal(t, json.RawMessage(feed.Items[len(feed.Items)-1]), stored)
				failure := errors.New("mutation failed")
				err = tc.store.MutateDocument(id, func(data json.RawMessage) (json.RawMessage, error) {
					data[0] = 'X'
					return data, failure
				})
				require.ErrorIs(t, err, failure)
				after, _ := tc.store.GetDocument(id)
				require.Equal(t, stored, after, "failed callbacks cannot mutate stored bytes")
				afterFeed, err := tc.store.ReadChangeFeed(t.Context(), nil)
				require.NoError(t, err)
				require.Equal(t, feed.Items, afterFeed.Items)
			})
		})
	}
}
