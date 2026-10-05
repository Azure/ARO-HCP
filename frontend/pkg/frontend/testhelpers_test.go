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

package frontend

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestNewTestFrontendInformersShareDatabase(t *testing.T) {
	f := NewTestFrontend(t)
	require.False(t, f.informers.HasSynced(), "unit constructor must not start informers")
	ctx, cancel := context.WithTimeout(utils.ContextWithLogger(t.Context(), testr.New(t)), 10*time.Second)
	defer cancel()

	cluster := coreapitesting.MinimumValidClusterTestCase()
	id := cluster.ID
	cluster.SetResourceID(id)
	cluster.SetPartitionKey(id.SubscriptionID)
	stored, err := f.resourcesDBClient.HCPClusters(id.SubscriptionID, id.ResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		f.informers.RunWithContext(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("timed out waiting for informers to stop")
		}
	}()

	require.True(t, cache.WaitForCacheSync(ctx.Done(), f.informers.HasSynced), "frontend informer bundle must sync")
	cached, err := f.clusterLister.Get(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
	require.NoError(t, err, "cached lister must observe the fixture written through the frontend database client")
	require.Equal(t, stored, cached)
}
