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
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Default relist durations. Each *Desire informer relists at this cadence so
// the kube-applier sees newly-created desires within one cycle even when the
// expiring-watcher protocol drops events.
const (
	ApplyDesireRelistDuration  = 30 * time.Second
	DeleteDesireRelistDuration = 30 * time.Second
	ReadDesireRelistDuration   = 30 * time.Second
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
func NewApplyDesireInformer(lister database.GlobalLister[kubeapplier.ApplyDesire]) cache.SharedIndexInformer {
	return NewApplyDesireInformerWithRelistDuration(lister, ApplyDesireRelistDuration)
}

// NewApplyDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for ApplyDesires with a configurable relist duration.
func NewApplyDesireInformerWithRelistDuration(
	lister database.GlobalLister[kubeapplier.ApplyDesire], relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing ApplyDesires")
			defer logger.Info("finished listing ApplyDesires")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}
			list := &kubeapplier.ApplyDesireList{}
			list.ResourceVersion = "0"
			for _, d := range iter.Items(ctx) {
				list.Items = append(list.Items, *d)
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}
			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return newExpiringWatcher(ctx, relistDuration), nil
		},
	}
	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw},
		&kubeapplier.ApplyDesire{},
		cache.SharedIndexInformerOptions{
			// ResyncPeriod doubles as the informer's resync check period:
			// per-handler resyncs cannot fire faster than this. Tying it to
			// relistDuration keeps "how often the informer re-checks Cosmos"
			// aligned with "how often handlers can be resynced," which is
			// what kube-applier's cooldown-gated controllers want.
			ResyncPeriod: relistDuration,
			Indexers:     desireIndexers(),
		},
	)
}

// NewDeleteDesireInformer creates an unstarted SharedIndexInformer for DeleteDesires
// using the default relist duration.
func NewDeleteDesireInformer(lister database.GlobalLister[kubeapplier.DeleteDesire]) cache.SharedIndexInformer {
	return NewDeleteDesireInformerWithRelistDuration(lister, DeleteDesireRelistDuration)
}

// NewDeleteDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for DeleteDesires with a configurable relist duration.
func NewDeleteDesireInformerWithRelistDuration(
	lister database.GlobalLister[kubeapplier.DeleteDesire], relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing DeleteDesires")
			defer logger.Info("finished listing DeleteDesires")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}
			list := &kubeapplier.DeleteDesireList{}
			list.ResourceVersion = "0"
			for _, d := range iter.Items(ctx) {
				list.Items = append(list.Items, *d)
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}
			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return newExpiringWatcher(ctx, relistDuration), nil
		},
	}
	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw},
		&kubeapplier.DeleteDesire{},
		cache.SharedIndexInformerOptions{
			// ResyncPeriod doubles as the informer's resync check period:
			// per-handler resyncs cannot fire faster than this. Tying it to
			// relistDuration keeps "how often the informer re-checks Cosmos"
			// aligned with "how often handlers can be resynced," which is
			// what kube-applier's cooldown-gated controllers want.
			ResyncPeriod: relistDuration,
			Indexers:     desireIndexers(),
		},
	)
}

// NewReadDesireInformer creates an unstarted SharedIndexInformer for ReadDesires
// using the default relist duration.
func NewReadDesireInformer(lister database.GlobalLister[kubeapplier.ReadDesire]) cache.SharedIndexInformer {
	return NewReadDesireInformerWithRelistDuration(lister, ReadDesireRelistDuration)
}

// NewReadDesireInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for ReadDesires with a configurable relist duration.
func NewReadDesireInformerWithRelistDuration(
	lister database.GlobalLister[kubeapplier.ReadDesire], relistDuration time.Duration,
) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing ReadDesires")
			defer logger.Info("finished listing ReadDesires")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}
			list := &kubeapplier.ReadDesireList{}
			list.ResourceVersion = "0"
			for _, d := range iter.Items(ctx) {
				list.Items = append(list.Items, *d)
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}
			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return newExpiringWatcher(ctx, relistDuration), nil
		},
	}
	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw},
		&kubeapplier.ReadDesire{},
		cache.SharedIndexInformerOptions{
			// ResyncPeriod doubles as the informer's resync check period:
			// per-handler resyncs cannot fire faster than this. Tying it to
			// relistDuration keeps "how often the informer re-checks Cosmos"
			// aligned with "how often handlers can be resynced," which is
			// what kube-applier's cooldown-gated controllers want.
			ResyncPeriod: relistDuration,
			Indexers:     desireIndexers(),
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
