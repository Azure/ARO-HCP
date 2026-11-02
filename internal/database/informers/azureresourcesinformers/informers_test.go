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

package azureresourcesinformers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/azureresourcescosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
)

func TestDeploymentStackDesireInformer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	db := azureresourcescosmosstoragetesting.NewMockAzureResourcesDBClient()
	informer := NewDeploymentStackDesireInformer(db.GlobalListers().DeploymentStackDesires(), db)
	lister := azureresourceslisters.NewDeploymentStackDesireLister(informer.GetIndexer())
	var workers sync.WaitGroup
	workers.Go(func() { informer.RunWithContext(ctx) })
	defer workers.Wait()
	defer cancel()
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced))
	for _, cluster := range []string{"cluster", "other"} {
		crud, err := db.DeploymentStackDesiresForCluster("sub", "rg", cluster)
		require.NoError(t, err)
		id := mustParseResourceID(t, "/subscriptions/Sub/resourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+cluster+"/azureDeploymentStackDesires/Desire")
		_, err = crud.Create(ctx, &azureresourcesapi.DeploymentStackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub"},
			Spec:           azureresourcesapi.DeploymentStackDesireSpec{Action: azureresourcesapi.DeploymentStackDesireActionApply, Stack: &armdeploymentstacks.DeploymentStack{}},
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
	item, err := lister.GetByResourceID(ctx, azureresourcesapihelpers.ToDeploymentStackDesireResourceIDString("SUB", "rg", "CLUSTER", "DESIRE"))
	require.NoError(t, err)
	require.Same(t, items[0], item)
	updated := item.DeepCopy()
	updated.Status.ObservedSpecHash = "updated"
	crud, err := db.DeploymentStackDesiresForCluster("sub", "rg", "cluster")
	require.NoError(t, err)
	_, err = crud.Replace(ctx, updated, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		item, err := lister.GetByResourceID(ctx, azureresourcesapihelpers.ToDeploymentStackDesireResourceIDString("sub", "rg", "cluster", "desire"))
		return err == nil && item.Status.ObservedSpecHash == "updated"
	}, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, crud.Delete(ctx, "desire"))
	require.Eventually(t, func() bool {
		items, err := lister.ListForCluster(ctx, "sub", "rg", "cluster")
		return err == nil && len(items) == 0
	}, 10*time.Second, 20*time.Millisecond, "deletion must stop management through the change feed")
}

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return rid
}
