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

package frontend

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"
)

type controlledInformer struct {
	cache.SharedIndexInformer
	synced  atomic.Bool
	started chan struct{}
	stopped chan struct{}
	release chan struct{}
}

func (i *controlledInformer) HasSynced() bool { return i.synced.Load() }

func (i *controlledInformer) RunWithContext(ctx context.Context) {
	close(i.started)
	<-ctx.Done()
	close(i.stopped)
	<-i.release
}

type observedListener struct {
	net.Listener
	once      sync.Once
	accepting chan struct{}
}

func (l *observedListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })
	return l.Listener.Accept()
}

func TestFrontendAdmissionCacheLifecycle(t *testing.T) {
	for _, mode := range []string{"cluster syncs first", "node pool syncs first", "cancel warmup"} {
		t.Run(mode, func(t *testing.T) {
			f := NewTestFrontend(t)
			clusters := &controlledInformer{SharedIndexInformer: f.clusterInformer, started: make(chan struct{}), stopped: make(chan struct{}), release: make(chan struct{})}
			pools := &controlledInformer{SharedIndexInformer: f.nodePoolInformer, started: make(chan struct{}), stopped: make(chan struct{}), release: make(chan struct{})}
			f.clusterInformer, f.nodePoolInformer = clusters, pools
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			api := &observedListener{Listener: listener, accepting: make(chan struct{})}
			f.listener = api
			f.metricsListener, err = net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(clusters.release); close(pools.release) }) }
			t.Cleanup(func() {
				cancel()
				release()
				_ = listener.Close()
				_ = f.metricsListener.Close()
			})
			go func() { done <- f.Run(ctx) }()
			for _, started := range []chan struct{}{clusters.started, pools.started} {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("informer did not start")
				}
			}
			client := &http.Client{Timeout: time.Second}
			response, err := client.Get("http://" + f.metricsListener.Addr().String() + "/metrics")
			require.NoError(t, err, "metrics must remain available during warmup")
			require.Equal(t, http.StatusOK, response.StatusCode)
			response.Body.Close()
			if mode == "node pool syncs first" {
				pools.synced.Store(true)
			} else {
				clusters.synced.Store(true)
			}
			select {
			case <-api.accepting:
				t.Fatal("API served before both caches synced")
			case <-time.After(150 * time.Millisecond):
			}
			if mode != "cancel warmup" {
				clusters.synced.Store(true)
				pools.synced.Store(true)
				select {
				case <-api.accepting:
				case <-time.After(5 * time.Second):
					t.Fatal("API did not serve after both caches synced")
				}
				response, err := client.Get("http://" + api.Addr().String() + "/healthz")
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, response.StatusCode, "health probe semantics must be preserved")
				response.Body.Close()
			}
			cancel()
			for _, stopped := range []chan struct{}{clusters.stopped, pools.stopped} {
				select {
				case <-stopped:
				case <-time.After(5 * time.Second):
					t.Fatal("informer was not cancelled")
				}
			}
			select {
			case err := <-done:
				t.Fatalf("Run returned without joining informers: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			release()
			select {
			case err := <-done:
				if mode == "cancel warmup" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not finish")
			}
			_, err = listener.Accept()
			require.ErrorIs(t, err, net.ErrClosed, "Run must close the listener even if warmup aborts")
			if mode == "cancel warmup" {
				select {
				case <-api.accepting:
					t.Fatal("API served during cancelled warmup")
				default:
				}
			}
		})
	}
}
