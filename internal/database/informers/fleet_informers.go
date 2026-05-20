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

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	StampRelistDuration             = 2 * time.Minute
	ManagementClusterRelistDuration = 2 * time.Minute
)

// NewStampInformer creates an unstarted SharedIndexInformer for stamps
// with the default relist duration.
func NewStampInformer(lister database.GlobalLister[fleet.Stamp]) cache.SharedIndexInformer {
	return NewStampInformerWithRelistDuration(lister, StampRelistDuration)
}

// NewStampInformerWithRelistDuration creates an unstarted SharedIndexInformer for stamps
// with a configurable relist duration.
func NewStampInformerWithRelistDuration(lister database.GlobalLister[fleet.Stamp], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing stamps")
			defer logger.Info("finished listing stamps")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &fleet.StampList{}
			list.ResourceVersion = "0"
			for _, s := range iter.Items(ctx) {
				list.Items = append(list.Items, *s)
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
		&fleet.Stamp{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
		},
	)
}

// NewManagementClusterInformer creates an unstarted SharedIndexInformer for management clusters
// with the default relist duration.
func NewManagementClusterInformer(lister database.GlobalLister[fleet.ManagementCluster]) cache.SharedIndexInformer {
	return NewManagementClusterInformerWithRelistDuration(lister, ManagementClusterRelistDuration)
}

// NewManagementClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for management clusters
// with a configurable relist duration.
func NewManagementClusterInformerWithRelistDuration(lister database.GlobalLister[fleet.ManagementCluster], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing management clusters")
			defer logger.Info("finished listing management clusters")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &fleet.ManagementClusterList{}
			list.ResourceVersion = "0"
			for _, mc := range iter.Items(ctx) {
				list.Items = append(list.Items, *mc)
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
		&fleet.ManagementCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				listers.ByCSProvisionShard: managementClusterProvisionShardIDIndexFunc,
			},
		},
	)
}
