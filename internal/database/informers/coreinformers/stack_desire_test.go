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

package coreinformers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

func TestStackDesireInformer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	informer := NewStackDesireInformer(db.ResourcesGlobalListers().StackDesires(), db)
	lister := corelisters.NewStackDesireLister(informer.GetIndexer())
	var workers sync.WaitGroup
	workers.Go(func() { informer.RunWithContext(ctx) })
	defer workers.Wait()
	defer cancel()
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced))
	for _, cluster := range []string{"cluster", "other"} {
		id := mustParseResourceID(t, "/subscriptions/Sub/resourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+cluster+"/stackDesires/Desire")
		_, err := db.StackDesires("sub", "rg", cluster).Create(ctx, &coreapi.StackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub"},
			Spec:           coreapi.StackDesireSpec{Action: coreapi.StackDesireActionApply, Stack: &armdeploymentstacks.DeploymentStack{}},
		}, nil)
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		items, err := lister.List(ctx)
		return err == nil && len(items) == 2
	}, 10*time.Second, 20*time.Millisecond, "new desires must arrive through the change feed without a relist")
	items, err := lister.ListForCluster(ctx, "SUB", "rg", "CLUSTER")
	require.NoError(t, err)
	require.Len(t, items, 1, "cluster index must isolate desires by owning cluster")
	item, err := lister.Get(ctx, "SUB", "rg", "CLUSTER", "DESIRE")
	require.NoError(t, err)
	require.Same(t, items[0], item)
	updated := item.DeepCopy()
	updated.Status.ObservedSpecHash = "updated"
	_, err = db.StackDesires("sub", "rg", "cluster").Replace(ctx, updated, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		item, err := lister.Get(ctx, "sub", "rg", "cluster", "desire")
		return err == nil && item.Status.ObservedSpecHash == "updated"
	}, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, db.StackDesires("sub", "rg", "cluster").Delete(ctx, "desire"))
	require.Eventually(t, func() bool {
		items, err := lister.ListForCluster(ctx, "sub", "rg", "cluster")
		return err == nil && len(items) == 0
	}, 10*time.Second, 20*time.Millisecond, "deletion must stop management through the change feed")
}
