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

package controllerutils

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
)

const (
	testClusterARMID  = "/subscriptions/sub1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
	testNodePoolARMID = testClusterARMID + "/nodePools/np1"
)

type stringSyncer struct {
	cooldown controllerutil.CooldownChecker
}

func (s *stringSyncer) MakeKey(rid *azcorearm.ResourceID) string {
	if rid == nil {
		return ""
	}
	return rid.String()
}

func (s *stringSyncer) SyncOnce(context.Context, string) error { return nil }

func (s *stringSyncer) CooldownChecker() controllerutil.CooldownChecker {
	if s.cooldown == nil {
		return alwaysAllowCooldown{}
	}
	return s.cooldown
}

type alwaysAllowCooldown struct{}

func (alwaysAllowCooldown) CanSync(context.Context, any) bool { return true }

type neverAllowCooldown struct{}

func (neverAllowCooldown) CanSync(context.Context, any) bool { return false }

func newTestWatchingController() (*genericWatchingController[string], *azcorearm.ResourceID, *azcorearm.ResourceID) {
	clusterID := api.Must(azcorearm.ParseResourceID(testClusterARMID))
	npID := api.Must(azcorearm.ParseResourceID(testNodePoolARMID))
	syncer := &stringSyncer{}
	c := newGenericWatchingController("test", clusterID.ResourceType, syncer)
	return c, clusterID, npID
}

func popAllQueue(c *genericWatchingController[string]) []string {
	var keys []string
	for c.queue.Len() > 0 {
		k, shutdown := c.queue.Get()
		if shutdown {
			panic("queue shut down unexpectedly")
		}
		c.queue.Done(k)
		c.queue.Forget(k)
		keys = append(keys, k)
	}
	return keys
}

func TestEnqueueResourceIDAddWithMaxDepth(t *testing.T) {
	clusterID := api.Must(azcorearm.ParseResourceID(testClusterARMID))
	npID := api.Must(azcorearm.ParseResourceID(testNodePoolARMID))

	tests := []struct {
		name     string
		resource *azcorearm.ResourceID
		changed  bool
		maxDepth int
		wantKeys []string
	}{
		{
			name:     "nil resource",
			changed:  true,
			maxDepth: -1,
			wantKeys: nil,
		},
		{
			name:     "direct cluster match changed",
			resource: clusterID,
			changed:  true,
			maxDepth: 0,
			wantKeys: []string{clusterID.String()},
		},
		{
			name:     "node pool maxDepth 0 does not walk parent",
			resource: npID,
			changed:  true,
			maxDepth: 0,
			wantKeys: nil,
		},
		{
			name:     "node pool maxDepth 1 enqueues cluster",
			resource: npID,
			changed:  true,
			maxDepth: 1,
			wantKeys: []string{clusterID.String()},
		},
		{
			name:     "node pool negative maxDepth enqueues cluster",
			resource: npID,
			changed:  true,
			maxDepth: -1,
			wantKeys: []string{clusterID.String()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestWatchingController()
			c.EnqueueResourceIDAddWithMaxDepth(tt.resource, tt.changed, tt.maxDepth)
			got := popAllQueue(c)
			require.Equal(t, tt.wantKeys, got)
		})
	}
}

func TestEnqueueResourceIDAddWithMaxDepth_changedAndCooldown(t *testing.T) {
	clusterID := api.Must(azcorearm.ParseResourceID(testClusterARMID))
	syncer := &stringSyncer{cooldown: neverAllowCooldown{}}
	c := newGenericWatchingController("cooldown", clusterID.ResourceType, syncer)

	tests := []struct {
		name         string
		changed      bool
		wantNumElems int
	}{
		{name: "unchanged skipped when CanSync false", changed: false, wantNumElems: 0},
		{name: "changed enqueues despite cooldown", changed: true, wantNumElems: 1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c.EnqueueResourceIDAddWithMaxDepth(clusterID, testCase.changed, -1)
			got := popAllQueue(c)
			require.Len(t, got, testCase.wantNumElems)
			if testCase.wantNumElems == 1 {
				require.Equal(t, clusterID.String(), got[0])
			}
		})
	}
}

func TestEnqueueCosmosWithMaxDepth(t *testing.T) {
	_, clusterID, npID := newTestWatchingController()

	tests := []struct {
		name string
		run  func(*genericWatchingController[string])
		want []string
	}{
		{
			name: "add from node pool metadata",
			run: func(c *genericWatchingController[string]) {
				c.enqueueCosmosAddWithMaxDepth(&arm.CosmosMetadata{ResourceID: npID}, 1)
			},
			want: []string{clusterID.String()},
		},
		{
			name: "update same etag uses unchanged path",
			run: func(c *genericWatchingController[string]) {
				etag := azcore.ETag("e1")
				oldObj := &arm.CosmosMetadata{ResourceID: npID, CosmosETag: etag}
				newObj := &arm.CosmosMetadata{ResourceID: npID, CosmosETag: etag}
				c.enqueueCosmosUpdateWithMaxDepth(oldObj, newObj, 1)
			},
			want: []string{clusterID.String()},
		},
		{
			name: "update different etag",
			run: func(c *genericWatchingController[string]) {
				oldObj := &arm.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("a")}
				newObj := &arm.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("b")}
				c.enqueueCosmosUpdateWithMaxDepth(oldObj, newObj, 1)
			},
			want: []string{clusterID.String()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestWatchingController()
			tt.run(c)
			require.Equal(t, tt.want, popAllQueue(c))
		})
	}
}

type capturingNotifier struct {
	addFunc    func(any)
	updateFunc func(any, any)
}

func (n *capturingNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	f, ok := handler.(cache.ResourceEventHandlerFuncs)
	if !ok {
		return nil, fmt.Errorf("expected ResourceEventHandlerFuncs, got %T", handler)
	}
	n.addFunc = f.AddFunc
	n.updateFunc = f.UpdateFunc
	return nil, nil
}

func TestQueueForInformersWithMaxDepth(t *testing.T) {
	_, clusterID, npID := newTestWatchingController()

	tests := []struct {
		name string
		run  func(t *testing.T, c *genericWatchingController[string], n *capturingNotifier)
	}{
		{
			name: "Add handler respects maxDepth",
			run: func(t *testing.T, c *genericWatchingController[string], n *capturingNotifier) {
				require.NotNil(t, n.addFunc)
				n.addFunc(&arm.CosmosMetadata{ResourceID: npID})
				require.Equal(t, []string{clusterID.String()}, popAllQueue(c))
			},
		},
		{
			name: "Update handler respects maxDepth",
			run: func(t *testing.T, c *genericWatchingController[string], n *capturingNotifier) {
				require.NotNil(t, n.updateFunc)
				oldObj := &arm.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("a")}
				newObj := &arm.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("b")}
				n.updateFunc(oldObj, newObj)
				require.Equal(t, []string{clusterID.String()}, popAllQueue(c))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestWatchingController()
			n := &capturingNotifier{}
			require.NoError(t, c.QueueForInformersWithMaxDepth(time.Minute, 1, n))
			tt.run(t, c, n)
		})
	}

	t.Run("joins notifier errors", func(t *testing.T) {
		c, _, _ := newTestWatchingController()
		bad := &errNotifier{err: errors.New("register failed")}
		err := c.QueueForInformersWithMaxDepth(time.Minute, -1, bad)
		require.Error(t, err)
		require.ErrorIs(t, err, bad.err)
	})
}

type errNotifier struct {
	err error
}

func (e *errNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	return nil, e.err
}
