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

// Package informerutils holds the shared list/watch primitives used by the
// split informer factory packages (core/fleet/kubeapplier): the Cosmos
// change-feed backed ListerWatcher, an expiring watch.Interface, and the
// ListWatch semantics opt-out.
package informerutils

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosmetrics"
)

// ListWatchWithoutWatchListSemantics opts out of WatchListClient semantics.
// Mirrors the unexported wrapper from client-go/tools/cache/listwatch.go.
// Cosmos-backed informers use ChangeFeedWatcher which does not support
// the bookmark protocol that WatchListClient requires.
type ListWatchWithoutWatchListSemantics struct {
	*cache.ListWatch
	// InformerName attributes lists, query pages, and asynchronous change-feed
	// reads to the informer, independently of its consuming controllers.
	InformerName string
}

func (ListWatchWithoutWatchListSemantics) IsWatchListSemanticsUnSupported() bool { return true }

func (lw ListWatchWithoutWatchListSemantics) ListWithContext(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
	return lw.ListWatch.ListWithContext(cosmosmetrics.ContextWithInformerName(ctx, lw.InformerName), options)
}

func (lw ListWatchWithoutWatchListSemantics) WatchWithContext(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
	return lw.ListWatch.WatchWithContext(cosmosmetrics.ContextWithInformerName(ctx, lw.InformerName), options)
}
