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

package kubeapplierinformers

import (
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
)

// Default relist durations. With changefeed, the relist is only a safety net;
// near-real-time updates arrive via the change feed poll loop.
//
// Relist intervals use distinct prime-number durations (in seconds) between
// 30 and 60 minutes. Using different primes makes it very unlikely that two
// informers will align their relist cycles, preventing thundering-herd list
// storms on Cosmos DB. When adding a new informer, pick the next unused
// prime from this range.
const (
	ApplyDesireRelistDuration = 3391 * time.Second // ~56 minutes 31 seconds
	ReadDesireRelistDuration  = 3529 * time.Second // ~58 minutes 49 seconds
)

// desireIndexers is the standard set registered on every *Desire informer.
func desireIndexers() cache.Indexers {
	return cache.Indexers{
		kubeapplierlisters.ByManagementCluster: managementClusterIndexFunc,
		kubeapplierlisters.ByCluster:           clusterResourceIDIndexFunc,
		kubeapplierlisters.ByNodePool:          nodePoolResourceIDIndexFunc,
	}
}

// NewApplyDesireInformer creates an unstarted SharedIndexInformer for ApplyDesires
// using the default relist duration.
func NewApplyDesireInformer(lister cosmosstorageutils.GlobalLister[kubeapplierapi.ApplyDesire], changeFeedClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewApplyDesireInformerWithRelistDuration(lister, changeFeedClient, ApplyDesireRelistDuration)
}

// NewApplyDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for ApplyDesires with a configurable relist duration.
func NewApplyDesireInformerWithRelistDuration(
	lister cosmosstorageutils.GlobalLister[kubeapplierapi.ApplyDesire], changeFeedClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire, cosmosstorageutils.GenericDocument[kubeapplierapi.ApplyDesire]](
		[]azcorearm.ResourceType{
			kubeapplierapi.ClusterScopedApplyDesireResourceType,
			kubeapplierapi.NodePoolScopedApplyDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRequestScopedApplyDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRevocationScopedApplyDesireResourceType,
		},
		utilsclock.RealClock{},
		lister,
		changeFeedClient,
		relistDuration,
		"kubeApplier",
	)
	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&kubeapplierapi.ApplyDesire{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			Indexers:          desireIndexers(),
			ObjectDescription: "ApplyDesire",
		},
	)
}

// NewReadDesireInformer creates an unstarted SharedIndexInformer for ReadDesires
// using the default relist duration.
func NewReadDesireInformer(lister cosmosstorageutils.GlobalLister[kubeapplierapi.ReadDesire], changeFeedClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewReadDesireInformerWithRelistDuration(lister, changeFeedClient, ReadDesireRelistDuration)
}

// NewReadDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for ReadDesires with a configurable relist duration.
func NewReadDesireInformerWithRelistDuration(
	lister cosmosstorageutils.GlobalLister[kubeapplierapi.ReadDesire], changeFeedClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire, cosmosstorageutils.GenericDocument[kubeapplierapi.ReadDesire]](
		[]azcorearm.ResourceType{
			kubeapplierapi.ClusterScopedReadDesireResourceType,
			kubeapplierapi.NodePoolScopedReadDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRequestScopedReadDesireResourceType,
			kubeapplierapi.SystemAdminCredentialRevocationScopedReadDesireResourceType,
			kubeapplierapi.ManagementClusterScopedReadDesireResourceType,
		},
		utilsclock.RealClock{},
		lister,
		changeFeedClient,
		relistDuration,
		"kubeApplier",
	)
	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&kubeapplierapi.ReadDesire{},
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
