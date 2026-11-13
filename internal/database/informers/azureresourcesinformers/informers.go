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
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
)

// DeploymentStackDesireRelistDuration indicates the maximum time it will take
// to notice a new DeploymentStackDesire that was missed by the change feed.
const DeploymentStackDesireRelistDuration = 30 * time.Minute

// NewDeploymentStackDesireInformer creates an unstarted informer with a cluster index.
func NewDeploymentStackDesireInformer(lister cosmosstorageutils.GlobalLister[azureresourcesapi.DeploymentStackDesire], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewDeploymentStackDesireInformerWithRelistDuration(lister, cosmosClient, DeploymentStackDesireRelistDuration)
}

func NewDeploymentStackDesireInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[azureresourcesapi.DeploymentStackDesire], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire, cosmosstorageutils.GenericDocument[azureresourcesapi.DeploymentStackDesire]](
		[]azcorearm.ResourceType{azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType},
		utilsclock.RealClock{}, lister, cosmosClient, relistDuration, "azureResources",
	)
	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch(), InformerName: "DeploymentStackDesires"},
		&azureresourcesapi.DeploymentStackDesire{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      time.Hour,
			Indexers:          cache.Indexers{azureresourceslisters.ByCluster: clusterResourceIDIndexFunc},
			ObjectDescription: "DeploymentStackDesire",
		},
	)
}
