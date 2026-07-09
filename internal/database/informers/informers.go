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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
)

// Default relist durations. With changefeed, the relist is only a safety net;
// near-real-time updates arrive via the change feed poll loop.
const (
	ApplyDesireRelistDuration = 3 * time.Minute
	ReadDesireRelistDuration  = 3 * time.Minute
)

// desireIndexers is the standard set registered on every *Desire informer.
func desireIndexers() cache.Indexers {
	return cache.Indexers{
		listers.ByManagementCluster: managementClusterIndexFunc,
		listers.ByCluster:           clusterResourceIDIndexFunc,
		listers.ByNodePool:          nodePoolResourceIDIndexFunc,
	}
}

// NewApplyDesireInformer creates an unstarted SharedIndexInformer for ApplyDesires
// using the default relist duration.
func NewApplyDesireInformer(lister database.GlobalLister[kubeapplier.ApplyDesire], changeFeedClient database.ChangeFeedClient) cache.SharedIndexInformer {
	return NewApplyDesireInformerWithRelistDuration(lister, changeFeedClient, ApplyDesireRelistDuration)
}

// NewApplyDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for ApplyDesires with a configurable relist duration.
func NewApplyDesireInformerWithRelistDuration(
	lister database.GlobalLister[kubeapplier.ApplyDesire], changeFeedClient database.ChangeFeedClient, relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire, database.GenericDocument[kubeapplier.ApplyDesire]](
		[]azcorearm.ResourceType{
			kubeapplier.ClusterScopedApplyDesireResourceType,
			kubeapplier.NodePoolScopedApplyDesireResourceType,
		},
		utilsclock.RealClock{},
		lister,
		changeFeedClient,
		relistDuration,
	)
	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&kubeapplier.ApplyDesire{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			Indexers:          desireIndexers(),
			ObjectDescription: "ApplyDesire",
		},
	)
}

// NewReadDesireInformer creates an unstarted SharedIndexInformer for ReadDesires
// using the default relist duration.
func NewReadDesireInformer(lister database.GlobalLister[kubeapplier.ReadDesire], changeFeedClient database.ChangeFeedClient) cache.SharedIndexInformer {
	return NewReadDesireInformerWithRelistDuration(lister, changeFeedClient, ReadDesireRelistDuration)
}

// NewReadDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for ReadDesires with a configurable relist duration.
func NewReadDesireInformerWithRelistDuration(
	lister database.GlobalLister[kubeapplier.ReadDesire], changeFeedClient database.ChangeFeedClient, relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[kubeapplier.ReadDesire, *kubeapplier.ReadDesire, database.GenericDocument[kubeapplier.ReadDesire]](
		[]azcorearm.ResourceType{
			kubeapplier.ClusterScopedReadDesireResourceType,
			kubeapplier.NodePoolScopedReadDesireResourceType,
		},
		utilsclock.RealClock{},
		lister,
		changeFeedClient,
		relistDuration,
	)
	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&kubeapplier.ReadDesire{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			Indexers:          desireIndexers(),
			ObjectDescription: "ReadDesire",
		},
	)
}

// store-key check: kubeapplier.*Desire types' GetObjectMeta returns a metadata
// object whose Name is the lower-cased ResourceID string, which is exactly
// what we use as the indexer's primary key. SharedIndexInformer derives the
// store key from the object's metadata via cache.MetaNamespaceKeyFunc by
// default (formats as `<namespace>/<name>`), but our objects have empty
// namespaces so the resulting key reduces to the lower-cased ResourceID.
//
// The lister Get* helpers build the same lower-cased ResourceID, so they look
// items up by exactly the key the informer used.
var _ = cache.MetaNamespaceKeyFunc
