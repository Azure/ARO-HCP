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

// Package informers provides Cosmos-backed SharedIndexInformers for the
// kube-applier *Desire resource types. The factory accepts a
// database.KubeApplierGlobalListers, which the caller obtains either from
// KubeApplierDBClient.GlobalListers() (cross-partition, used by the backend) or
// from KubeApplierDBClient.PartitionListers(mgmtCluster) (single-partition,
// used by the kube-applier binary).
package informers

import (
	"context"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// listWatchWithoutWatchListSemantics opts out of WatchListClient semantics.
// Mirrors the unexported wrapper from client-go/tools/cache/listwatch.go.
// Cosmos-backed informers use newExpiringWatcher which does not support
// the bookmark protocol that WatchListClient requires.
type listWatchWithoutWatchListSemantics struct {
	*cache.ListWatch
}

func (listWatchWithoutWatchListSemantics) IsWatchListSemanticsUnSupported() bool { return true }

// expiringWatcher implements watch.Interface and sends an expired error after
// the configured duration to cause the reflector to relist. This drives
// SharedInformers backed by non-Kubernetes data sources like Cosmos that have
// no native watch protocol. It is structurally identical to the backend's
// expiring watcher; we copy it here so this package has no dependency on
// backend/.
type expiringWatcher struct {
	result chan watch.Event
	done   chan struct{}
}

// newExpiringWatcher creates a watcher that terminates after the given
// duration by sending an HTTP 410 Gone / StatusReasonExpired error.
func newExpiringWatcher(ctx context.Context, expiry time.Duration) watch.Interface {
	w := &expiringWatcher{
		result: make(chan watch.Event),
		done:   make(chan struct{}),
	}
	go func() {
		select {
		case <-time.After(expiry):
			w.result <- watch.Event{
				Type: watch.Error,
				Object: &metav1.Status{
					Status:  metav1.StatusFailure,
					Code:    http.StatusGone,
					Reason:  metav1.StatusReasonExpired,
					Message: "watch expired",
				},
			}
		case <-w.done:
		case <-ctx.Done():
		}
		close(w.result)
	}()
	return w
}

func (w *expiringWatcher) Stop() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
}

func (w *expiringWatcher) ResultChan() <-chan watch.Event { return w.result }
