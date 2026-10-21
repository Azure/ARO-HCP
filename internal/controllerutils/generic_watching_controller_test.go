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
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	testClusterARMID  = "/subscriptions/sub1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
	testNodePoolARMID = testClusterARMID + "/nodePools/np1"
)

type stringSyncer struct {
	cooldown CooldownChecker
	syncErr  error
}

func (s *stringSyncer) MakeKey(rid *azcorearm.ResourceID) string {
	if rid == nil {
		return ""
	}
	return rid.String()
}

func (s *stringSyncer) SyncOnce(context.Context, string) error { return s.syncErr }

func (s *stringSyncer) CooldownChecker() CooldownChecker {
	if s.cooldown == nil {
		return alwaysAllowCooldown{}
	}
	return s.cooldown
}

func testReconcileTotal() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_reconcile_total",
	}, []string{"controller"})
}

type alwaysAllowCooldown struct{}

func (alwaysAllowCooldown) CanSync(context.Context, any) bool { return true }

type neverAllowCooldown struct{}

func (neverAllowCooldown) CanSync(context.Context, any) bool { return false }

func newTestWatchingController() (*GenericWatchingController[string], *azcorearm.ResourceID, *azcorearm.ResourceID) {
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID))
	npID := metadataapi.Must(azcorearm.ParseResourceID(testNodePoolARMID))
	syncer := &stringSyncer{}
	c := NewGenericWatchingController("test", clusterID.ResourceType, syncer, testReconcileTotal())
	return c, clusterID, npID
}

func popAllQueue(c *GenericWatchingController[string]) []string {
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

func TestInterestingResources(t *testing.T) {
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID))
	npID := metadataapi.Must(azcorearm.ParseResourceID(testNodePoolARMID))
	readID := metadataapi.Must(azcorearm.ParseResourceID(testNodePoolARMID + "/readDesires/status"))
	type interest struct {
		resourceType azcorearm.ResourceType
		name         string
	}
	tests := []struct {
		name      string
		interests []interest
		resource  *azcorearm.ResourceID
		maxDepth  int
		want      bool
	}{
		{name: "no filter preserves child events", resource: readID, maxDepth: -1, want: true},
		{name: "nil resource", interests: []interest{{clusterID.ResourceType, "*"}}, maxDepth: -1},
		{name: "exact name", interests: []interest{{clusterID.ResourceType, "c1"}}, resource: clusterID, want: true},
		{name: "different name", interests: []interest{{clusterID.ResourceType, "c2"}}, resource: clusterID},
		{name: "wildcard name", interests: []interest{{clusterID.ResourceType, "*"}}, resource: clusterID, want: true},
		{name: "wildcard does not match another type", interests: []interest{{clusterID.ResourceType, "*"}}, resource: npID, maxDepth: -1},
		{name: "type and name are case insensitive", interests: []interest{{azcorearm.NewResourceType("MICROSOFT.REDHATOPENSHIFT", "HCPOPENSHIFTCLUSTERS/NODEPOOLS"), "NP1"}}, resource: npID, maxDepth: 1, want: true},
		{name: "resource casing is ignored", interests: []interest{{npID.ResourceType, "np1"}}, resource: metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testNodePoolARMID))), maxDepth: 1, want: true},
		{name: "multiple names accumulate", interests: []interest{{clusterID.ResourceType, "c1"}, {clusterID.ResourceType, "c2"}}, resource: clusterID, want: true},
		{name: "multiple types accumulate", interests: []interest{{clusterID.ResourceType, "c1"}, {npID.ResourceType, "np1"}}, resource: clusterID, want: true},
		{name: "accepted child does not require parent registration", interests: []interest{{npID.ResourceType, "np1"}}, resource: npID, maxDepth: 1, want: true},
		{name: "accepted grandchild skips unregistered ancestors", interests: []interest{{readID.ResourceType, "status"}}, resource: readID, maxDepth: 2, want: true},
		{name: "accepted child still respects zero depth", interests: []interest{{npID.ResourceType, "*"}}, resource: npID},
		{name: "accepted grandchild still respects depth limit", interests: []interest{{readID.ResourceType, "*"}}, resource: readID, maxDepth: 1},
		{name: "accepted grandchild supports unlimited depth", interests: []interest{{readID.ResourceType, "*"}}, resource: readID, maxDepth: -1, want: true},
		{name: "names are not globs", interests: []interest{{npID.ResourceType, "np*"}}, resource: npID, maxDepth: -1},
		{name: "accepted resource without matching ancestor", interests: []interest{{azcorearm.SubscriptionResourceType, "*"}}, resource: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub1")), maxDepth: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestWatchingController()
			t.Cleanup(c.queue.ShutDown)
			for _, interest := range tt.interests {
				c.AddInterestingResource(interest.resourceType, interest.name)
			}
			c.EnqueueResourceIDAddWithMaxDepth(tt.resource, true, tt.maxDepth)
			keys := popAllQueue(c)
			if tt.want {
				require.Len(t, keys, 1, "accepted events must enqueue the owning cluster")
				require.Equal(t, strings.ToLower(testClusterARMID), strings.ToLower(keys[0]))
			} else {
				require.Empty(t, keys, "uninteresting or out-of-scope events must not enqueue")
			}
		})
	}
}

func TestInterestingResourcesInformerEvents(t *testing.T) {
	for _, cooldownAllowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("cooldownAllowed=%t", cooldownAllowed), func(t *testing.T) {
			c, clusterID, npID := newTestWatchingController()
			t.Cleanup(c.queue.ShutDown)
			if !cooldownAllowed {
				c.syncer.(*stringSyncer).cooldown = neverAllowCooldown{}
			}
			c.AddInterestingResource(npID.ResourceType, "np1")
			n := &capturingNotifier{}
			require.NoError(t, c.QueueForInformersWithMaxDepth(time.Minute, 1, n))
			for _, resource := range []*azcorearm.ResourceID{npID, clusterID} {
				oldObj := &coreapi.CosmosMetadata{ResourceID: resource, CosmosETag: "old"}
				newObj := &coreapi.CosmosMetadata{ResourceID: resource, CosmosETag: "new"}
				for _, event := range []string{"add", "changed update", "resync"} {
					switch event {
					case "add":
						n.addFunc(newObj)
					case "changed update":
						n.updateFunc(oldObj, newObj)
					case "resync":
						n.updateFunc(newObj, newObj)
					}
					keys := popAllQueue(c)
					if resource == npID && (event != "resync" || cooldownAllowed) {
						require.Equal(t, []string{clusterID.String()}, keys, "%s should enqueue accepted resource", event)
					} else {
						require.Empty(t, keys, "%s should respect filtering and resync cooldown", event)
					}
				}
			}
			// Explicit work scheduling is independent of informer filtering.
			c.Enqueue(clusterID.String())
			require.Equal(t, []string{clusterID.String()}, popAllQueue(c))
		})
	}
}

func TestEnqueueResourceIDAddWithMaxDepth(t *testing.T) {
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID))
	npID := metadataapi.Must(azcorearm.ParseResourceID(testNodePoolARMID))

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
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID))
	syncer := &stringSyncer{cooldown: neverAllowCooldown{}}
	c := NewGenericWatchingController("cooldown", clusterID.ResourceType, syncer, testReconcileTotal())

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
		run  func(*GenericWatchingController[string])
		want []string
	}{
		{
			name: "add from node pool metadata",
			run: func(c *GenericWatchingController[string]) {
				c.enqueueCosmosAddWithMaxDepth(&coreapi.CosmosMetadata{ResourceID: npID}, 1)
			},
			want: []string{clusterID.String()},
		},
		{
			name: "update same etag uses unchanged path",
			run: func(c *GenericWatchingController[string]) {
				etag := azcore.ETag("e1")
				oldObj := &coreapi.CosmosMetadata{ResourceID: npID, CosmosETag: etag}
				newObj := &coreapi.CosmosMetadata{ResourceID: npID, CosmosETag: etag}
				c.enqueueCosmosUpdateWithMaxDepth(oldObj, newObj, 1)
			},
			want: []string{clusterID.String()},
		},
		{
			name: "update different etag",
			run: func(c *GenericWatchingController[string]) {
				oldObj := &coreapi.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("a")}
				newObj := &coreapi.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("b")}
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
		run  func(t *testing.T, c *GenericWatchingController[string], n *capturingNotifier)
	}{
		{
			name: "Add handler respects maxDepth",
			run: func(t *testing.T, c *GenericWatchingController[string], n *capturingNotifier) {
				require.NotNil(t, n.addFunc)
				n.addFunc(&coreapi.CosmosMetadata{ResourceID: npID})
				require.Equal(t, []string{clusterID.String()}, popAllQueue(c))
			},
		},
		{
			name: "Update handler respects maxDepth",
			run: func(t *testing.T, c *GenericWatchingController[string], n *capturingNotifier) {
				require.NotNil(t, n.updateFunc)
				oldObj := &coreapi.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("a")}
				newObj := &coreapi.CosmosMetadata{ResourceID: npID, CosmosETag: azcore.ETag("b")}
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

func TestProcessNextWorkItemIncrementsReconcileTotal(t *testing.T) {
	clusterID := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID))
	reconcileTotal := testReconcileTotal()
	syncer := &stringSyncer{}
	c := NewGenericWatchingController("test-metrics", clusterID.ResourceType, syncer, reconcileTotal)

	c.queue.Add(clusterID.String())
	c.processNextWorkItem(context.Background())

	count := testutil.ToFloat64(reconcileTotal.WithLabelValues("test-metrics"))
	require.Equal(t, float64(1), count)

	c.queue.Add(clusterID.String())
	c.processNextWorkItem(context.Background())

	count = testutil.ToFloat64(reconcileTotal.WithLabelValues("test-metrics"))
	require.Equal(t, float64(2), count)
}
