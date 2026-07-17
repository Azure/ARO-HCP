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

package informers

import (
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
)

const (
	StampRelistDuration             = 2 * time.Minute
	ManagementClusterRelistDuration = 2 * time.Minute
)

// NewStampInformer creates an unstarted SharedIndexInformer for stamps
// with the default relist duration.
func NewStampInformer(lister database.GlobalLister[fleet.Stamp], cosmosClient database.ChangeFeedClient) cache.SharedIndexInformer {
	return NewStampInformerWithRelistDuration(lister, cosmosClient, StampRelistDuration)
}

// NewStampInformerWithRelistDuration creates an unstarted SharedIndexInformer for stamps
// with a configurable relist duration.
func NewStampInformerWithRelistDuration(lister database.GlobalLister[fleet.Stamp], cosmosClient database.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[fleet.Stamp, *fleet.Stamp, database.GenericDocument[fleet.Stamp]](
		[]azcorearm.ResourceType{fleet.StampResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&fleet.Stamp{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			ObjectDescription: "Stamp",
		},
	)
}

// NewManagementClusterInformer creates an unstarted SharedIndexInformer for management clusters
// with the default relist duration.
func NewManagementClusterInformer(lister database.GlobalLister[fleet.ManagementCluster], cosmosClient database.ChangeFeedClient) cache.SharedIndexInformer {
	return NewManagementClusterInformerWithRelistDuration(lister, cosmosClient, ManagementClusterRelistDuration)
}

// NewManagementClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for management clusters
// with a configurable relist duration.
func NewManagementClusterInformerWithRelistDuration(lister database.GlobalLister[fleet.ManagementCluster], cosmosClient database.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[fleet.ManagementCluster, *fleet.ManagementCluster, database.GenericDocument[fleet.ManagementCluster]](
		[]azcorearm.ResourceType{fleet.ManagementClusterResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&fleet.ManagementCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				listers.ByCSProvisionShard: managementClusterProvisionShardIDIndexFunc,
			},
			ObjectDescription: "ManagementCluster",
		},
	)
}
