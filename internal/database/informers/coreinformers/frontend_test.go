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

package coreinformers

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

type frontendTestInformer struct {
	cache.SharedIndexInformer
	synced  bool
	started bool
	stopped bool
	release chan struct{}
}

func (f *frontendTestInformer) HasSynced() bool { return f.synced }

func (f *frontendTestInformer) RunWithContext(ctx context.Context) {
	f.started = true
	<-ctx.Done()
	<-f.release
	f.stopped = true
}

func TestFrontendInformersHasSynced(t *testing.T) {
	workers := []*frontendTestInformer{{}, {}, {}, {}}
	informers := &frontendInformers{
		clusterInformer:                 workers[0],
		nodePoolInformer:                workers[1],
		serviceProviderClusterInformer:  workers[2],
		serviceProviderNodePoolInformer: workers[3],
	}
	require.False(t, informers.HasSynced(), "unstarted informers must not be synced")
	for _, worker := range workers {
		worker.synced = true
	}
	require.True(t, informers.HasSynced(), "all four informers are synced")
	for i, name := range []string{"clusters", "nodePools", "serviceProviderClusters", "serviceProviderNodePools"} {
		t.Run(name, func(t *testing.T) {
			workers[i].synced = false
			require.False(t, informers.HasSynced(), "%s must gate aggregate sync", name)
			workers[i].synced = true
		})
	}
}

func TestFrontendInformersRunWithContextJoinsAllWorkers(t *testing.T) {
	for blocked, name := range []string{"clusters", "nodePools", "serviceProviderClusters", "serviceProviderNodePools"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				workers := []*frontendTestInformer{
					{release: make(chan struct{})}, {release: make(chan struct{})},
					{release: make(chan struct{})}, {release: make(chan struct{})},
				}
				defer close(workers[blocked].release)
				informers := &frontendInformers{
					clusterInformer:                 workers[0],
					nodePoolInformer:                workers[1],
					serviceProviderClusterInformer:  workers[2],
					serviceProviderNodePoolInformer: workers[3],
				}
				done := make(chan struct{})
				go func() {
					informers.RunWithContext(ctx)
					close(done)
				}()
				synctest.Wait()
				for i, worker := range workers {
					require.True(t, worker.started, "worker %d must start", i)
					if i != blocked {
						close(worker.release)
					}
				}
				cancel()
				synctest.Wait()
				for i, worker := range workers {
					require.Equal(t, i != blocked, worker.stopped, "worker %d shutdown state", i)
				}
				select {
				case <-done:
					t.Fatal("RunWithContext returned before the final worker stopped")
				default:
				}
				// The deferred release lets the last worker finish; synctest joins all goroutines.
			})
		})
	}
}

func TestFrontendInformersConstructors(t *testing.T) {
	for _, name := range []string{"default", "customRelist"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			var informers FrontendInformers
			if name == "default" {
				informers = NewFrontendInformers(ctx, db.ResourcesGlobalListers(), db)
			} else {
				relist := time.Second
				informers = NewFrontendInformersWithRelistDuration(ctx, db.ResourcesGlobalListers(), db, &relist)
			}
			clusterInformer, clusters := informers.Clusters()
			nodePoolInformer, nodePools := informers.NodePools()
			serviceProviderClusterInformer, serviceProviderClusters := informers.ServiceProviderClusters()
			serviceProviderNodePoolInformer, serviceProviderNodePools := informers.ServiceProviderNodePools()
			require.NotNil(t, clusters)
			require.NotNil(t, nodePools)
			require.NotNil(t, serviceProviderClusters)
			require.NotNil(t, serviceProviderNodePools)
			require.False(t, informers.HasSynced())
			done := make(chan struct{})
			go func() {
				informers.RunWithContext(ctx)
				close(done)
			}()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("timed out waiting for informers to stop")
				}
			}()
			require.True(t, cache.WaitForCacheSync(ctx.Done(), informers.HasSynced))
			for _, informer := range []cache.SharedIndexInformer{clusterInformer, nodePoolInformer, serviceProviderClusterInformer, serviceProviderNodePoolInformer} {
				require.True(t, informer.HasSynced())
			}
		})
	}
}
