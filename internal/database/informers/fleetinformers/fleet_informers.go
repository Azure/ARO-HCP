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

package fleetinformers

import (
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

const (
	StampRelistDuration                      = 2 * time.Minute
	ManagementClusterRelistDuration          = 2 * time.Minute
	ControlPlaneVersionRolloutRelistDuration = 2 * time.Minute
)

// NewStampInformer creates an unstarted SharedIndexInformer for stamps
// with the default relist duration.
func NewStampInformer(lister cosmosstorageutils.GlobalLister[fleetapi.Stamp], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewStampInformerWithRelistDuration(lister, cosmosClient, StampRelistDuration)
}

// NewStampInformerWithRelistDuration creates an unstarted SharedIndexInformer for stamps
// with a configurable relist duration.
func NewStampInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[fleetapi.Stamp], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[fleetapi.Stamp, *fleetapi.Stamp, cosmosstorageutils.GenericDocument[fleetapi.Stamp]](
		[]azcorearm.ResourceType{fleetapi.StampResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
		"fleet",
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&fleetapi.Stamp{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			ObjectDescription: "Stamp",
		},
	)
}

// NewManagementClusterInformer creates an unstarted SharedIndexInformer for management clusters
// with the default relist duration.
func NewManagementClusterInformer(lister cosmosstorageutils.GlobalLister[fleetapi.ManagementCluster], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewManagementClusterInformerWithRelistDuration(lister, cosmosClient, ManagementClusterRelistDuration)
}

// NewManagementClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for management clusters
// with a configurable relist duration.
func NewManagementClusterInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[fleetapi.ManagementCluster], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[fleetapi.ManagementCluster, *fleetapi.ManagementCluster, cosmosstorageutils.GenericDocument[fleetapi.ManagementCluster]](
		[]azcorearm.ResourceType{fleetapi.ManagementClusterResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
		"fleet",
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&fleetapi.ManagementCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				fleetlisters.ByCSProvisionShard: managementClusterProvisionShardIDIndexFunc,
			},
			ObjectDescription: "ManagementCluster",
		},
	)
}

// NewControlPlaneVersionRolloutInformer creates an unstarted SharedIndexInformer
// for control-plane version rollouts with the default relist duration.
func NewControlPlaneVersionRolloutInformer(lister cosmosstorageutils.GlobalLister[fleetapi.ControlPlaneVersionRollout], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewControlPlaneVersionRolloutInformerWithRelistDuration(lister, cosmosClient, ControlPlaneVersionRolloutRelistDuration)
}

// NewControlPlaneVersionRolloutInformerWithRelistDuration creates an unstarted
// SharedIndexInformer for control-plane version rollouts with a configurable relist duration.
func NewControlPlaneVersionRolloutInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[fleetapi.ControlPlaneVersionRollout], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout, cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]](
		[]azcorearm.ResourceType{fleetapi.ControlPlaneVersionRolloutResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&fleetapi.ControlPlaneVersionRollout{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			ObjectDescription: "ControlPlaneVersionRollout",
		},
	)
}
