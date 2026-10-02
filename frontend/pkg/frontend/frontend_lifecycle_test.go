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

	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
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

type controlledFrontendInformers struct {
	coreinformers.FrontendInformers
	informers []*controlledInformer
	runs      atomic.Int32
}

func (b *controlledFrontendInformers) HasSynced() bool {
	for _, informer := range b.informers {
		if !informer.HasSynced() {
			return false
		}
	}
	return true
}

func (b *controlledFrontendInformers) RunWithContext(ctx context.Context) {
	b.runs.Add(1)
	var wg sync.WaitGroup
	for _, informer := range b.informers {
		wg.Go(func() { informer.RunWithContext(ctx) })
	}
	wg.Wait()
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
	for last, mode := range []string{"cluster syncs last", "node pool syncs last", "provider cluster syncs last", "provider node pool syncs last", "cancel warmup"} {
		t.Run(mode, func(t *testing.T) {
			f := NewTestFrontend(t)
			bundle := &controlledFrontendInformers{FrontendInformers: f.informers}
			for range 4 {
				bundle.informers = append(bundle.informers, &controlledInformer{started: make(chan struct{}), stopped: make(chan struct{}), release: make(chan struct{})})
			}
			f.informers = bundle
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			api := &observedListener{Listener: listener, accepting: make(chan struct{})}
			f.listener = api
			f.metricsListener, err = net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			var releaseOnce sync.Once
			release := func() {
				releaseOnce.Do(func() {
					for _, informer := range bundle.informers {
						close(informer.release)
					}
				})
			}
			t.Cleanup(func() {
				cancel()
				release()
				_ = listener.Close()
				_ = f.metricsListener.Close()
			})
			go func() { done <- f.Run(ctx) }()
			for _, informer := range bundle.informers {
				select {
				case <-informer.started:
				case <-time.After(5 * time.Second):
					t.Fatal("informer did not start")
				}
			}
			client := &http.Client{Timeout: time.Second}
			response, err := client.Get("http://" + f.metricsListener.Addr().String() + "/metrics")
			require.NoError(t, err, "metrics must remain available during warmup")
			require.Equal(t, http.StatusOK, response.StatusCode)
			response.Body.Close()
			for index, informer := range bundle.informers {
				if index != last%4 {
					informer.synced.Store(true)
				}
			}
			select {
			case <-api.accepting:
				t.Fatal("API served before all four caches synced")
			case <-time.After(150 * time.Millisecond):
			}
			if mode != "cancel warmup" {
				bundle.informers[last].synced.Store(true)
				select {
				case <-api.accepting:
				case <-time.After(5 * time.Second):
					t.Fatal("API did not serve after all four caches synced")
				}
				response, err := client.Get("http://" + api.Addr().String() + "/healthz")
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, response.StatusCode, "health probe semantics must be preserved")
				response.Body.Close()
			}
			cancel()
			for _, informer := range bundle.informers {
				select {
				case <-informer.stopped:
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
			require.EqualValues(t, 1, bundle.runs.Load(), "frontend must start exactly one bundle runner")
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

func TestFrontendRunReturnsServerErrors(t *testing.T) {
	for _, failedServer := range []string{"metrics", "api"} {
		t.Run(failedServer, func(t *testing.T) {
			f := NewTestFrontend(t)
			var err error
			f.listener, err = net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			f.metricsListener, err = net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = f.listener.Close()
				_ = f.metricsListener.Close()
			})
			if failedServer == "metrics" {
				require.NoError(t, f.metricsListener.Close())
			} else {
				require.NoError(t, f.listener.Close())
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			require.ErrorIs(t, f.Run(ctx), net.ErrClosed, "server failure must survive cleanup")
			require.NoError(t, ctx.Err(), "server failure must cancel Run without waiting for the caller")
			_, err = f.listener.Accept()
			require.ErrorIs(t, err, net.ErrClosed)
			_, err = f.metricsListener.Accept()
			require.ErrorIs(t, err, net.ErrClosed)
		})
	}
}
