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

package integrationutils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestFrontendCacheConvergence(t *testing.T) {
	defer VerifyNoNewGoLeaks(t)
	ctx, cancel := context.WithCancel(t.Context())
	ctx = utils.ContextWithLogger(ctx, DefaultLogger(t))
	defer cancel()
	info, err := NewIntegrationTestInfoFromEnv(ctx, t, true)
	require.NoError(t, err)
	defer info.Cleanup(utils.ContextWithLogger(context.Background(), DefaultLogger(t)))
	frontendDone, adminDone := make(chan error, 1), make(chan error, 1)
	go func() { frontendDone <- info.Frontend.Run(ctx) }()
	go func() { adminDone <- info.AdminAPI.Run(ctx) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			frontendErr, adminErr := <-frontendDone, <-adminDone
			require.NoError(t, frontendErr)
			require.NoError(t, adminErr)
		}
	}()
	require.NoError(t, WaitForHTTPReady(ctx, info.FrontendURL+"/healthz", info.AdminURL+"/healthz/ready"))
	require.True(t, info.ClusterInformer.HasSynced())
	require.True(t, info.NodePoolInformer.HasSynced())
	require.Empty(t, info.ClusterInformer.GetStore().List())
	require.Empty(t, info.NodePoolInformer.GetStore().List())

	const subscription = "0465bc32-c654-41b8-8d87-9815d7abe8f6"
	const clusterID = "/subscriptions/" + subscription + "/resourceGroups/test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/late"
	cases := []struct {
		id, resourceType string
		informer         cache.SharedIndexInformer
	}{
		{clusterID, coreapi.ClusterResourceType.String(), info.ClusterInformer},
		{clusterID + "/nodePools/late", coreapi.NodePoolResourceType.String(), info.NodePoolInformer},
	}
	load := func(id, resourceType, version string, instanceVersion int64) {
		t.Helper()
		uid, err := coreapi.ResourceIDStringToCosmosID(id)
		require.NoError(t, err)
		propertiesKey := "customerProperties"
		if resourceType == coreapi.NodePoolResourceType.String() {
			propertiesKey = "properties"
		}
		content, err := json.Marshal(map[string]any{
			"id": uid, "partitionKey": subscription, "resourceID": id, "resourceType": resourceType,
			"properties": map[string]any{
				"cosmosMetadata": map[string]any{"resourceID": id, "partitionKey": subscription, "instanceVersion": instanceVersion},
				propertiesKey:    map[string]any{"version": map[string]any{"id": version}},
			},
		})
		require.NoError(t, err)
		require.NoError(t, info.LoadContent(ctx, content))
	}

	// Use the real fixture loader after the frontend has synced an empty DB.
	for _, tc := range cases {
		load(tc.id, tc.resourceType, "4.20.8", 1)
	}
	require.NoError(t, info.WaitForFrontendCaches(ctx))
	for _, tc := range cases {
		require.Len(t, tc.informer.GetStore().List(), 1)
		load(tc.id, tc.resourceType, "4.21.1", 1)
	}
	require.NoError(t, info.WaitForFrontendCaches(ctx))
	for _, tc := range cases {
		content, err := json.Marshal(tc.informer.GetStore().List()[0])
		require.NoError(t, err)
		require.Contains(t, string(content), "4.21.1", "same-version fixture replacement must reach the cache")
	}

	t.Run("hard deletion", func(t *testing.T) {
		db := info.ResourcesDBClient().(*corecosmosstoragetesting.MockResourcesDBClient)
		defer func() {
			for _, tc := range cases {
				load(tc.id, tc.resourceType, "4.21.1", 1)
			}
		}()
		for _, tc := range cases {
			uid, err := coreapi.ResourceIDStringToCosmosID(tc.id)
			require.NoError(t, err)
			db.DeleteDocument(uid)
		}
		require.NoError(t, info.WaitForFrontendCaches(ctx))
		for _, tc := range cases {
			require.Empty(t, tc.informer.GetStore().List(), "hard deletes must converge via relist")
		}
	})
	require.NoError(t, info.WaitForFrontendCaches(ctx))

	// Freeze real, populated caches to deterministically test stale detection.
	cancel()
	frontendErr, adminErr := <-frontendDone, <-adminDone
	joined = true
	require.NoError(t, frontendErr)
	require.NoError(t, adminErr)
	ctx = t.Context()
	for _, tc := range cases {
		t.Run(tc.resourceType+" stale detection", func(t *testing.T) {
			for _, change := range []struct {
				version         string
				instanceVersion int64
			}{
				{"4.21.1", 2}, // Membership and content match, but the version is stale.
				{"4.22.1", 1}, // Membership and version match, but content is stale.
			} {
				load(tc.id, tc.resourceType, change.version, change.instanceVersion)
				waitCtx, stop := context.WithTimeout(ctx, 60*time.Millisecond)
				err := info.WaitForFrontendCaches(waitCtx)
				stop()
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.ErrorContains(t, err, "stale resource")
				load(tc.id, tc.resourceType, "4.21.1", 1)
			}
		})
	}
}

func TestWaitForHTTPReady(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/healthz", r.URL.Path)
				w.WriteHeader(status)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err := WaitForHTTPReady(ctx, server.URL+"/healthz")
			if status == http.StatusOK {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
	t.Run("blocked request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		require.ErrorIs(t, WaitForHTTPReady(ctx, server.URL+"/healthz"), context.DeadlineExceeded)
	})
}
