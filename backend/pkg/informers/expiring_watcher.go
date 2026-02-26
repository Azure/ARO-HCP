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
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// expiringWatcher implements watch.Interface and sends an expired error after
// the configured duration to cause the reflector to relist. This is used to
// drive SharedInformers backed by non-Kubernetes data sources (like CosmosDB)
// that do not support a native watch protocol.
type expiringWatcher struct {
	result chan watch.Event
	done   chan struct{}
}

// NewExpiringWatcher creates a watcher that terminates after the given duration
// by sending an HTTP 410 Gone / StatusReasonExpired error, causing the
// reflector to relist.
func NewExpiringWatcher(ctx context.Context, expiry time.Duration) watch.Interface {
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

func (w *expiringWatcher) ResultChan() <-chan watch.Event {
	return w.result
}
