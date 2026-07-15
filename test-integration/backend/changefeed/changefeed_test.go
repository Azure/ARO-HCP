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

package changefeed_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	backendinformers "github.com/Azure/ARO-HCP/backend/pkg/informers"
	backendlisters "github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dbinformers "github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-0000000000aa"
	testResourceGroup  = "rg-changefeed"

	// eventDeadline bounds how long any single test waits for a
	// change-feed event to arrive. The cosmos emulator's change feed
	// has noticeable latency (>1s in practice), so this is generous.
	eventDeadline = 20 * time.Second

	// silenceDeadline bounds how long the "no notification on deletion"
	// test waits while watching the result channel. We can't prove the
	// absence of an event in finite time, but a deletion that *would*
	// show up should arrive within the same eventDeadline window — so
	// waiting that long and seeing nothing is strong evidence.
	silenceDeadline = eventDeadline
)

type clusterChangeFeedWatcher = dbinformers.ChangeFeedWatcher[
	api.HCPOpenShiftCluster,
	*api.HCPOpenShiftCluster,
	database.GenericDocument[api.HCPOpenShiftCluster],
]

type clusterChangeFeedListWatcher = dbinformers.ChangeFeedListWatcher[
	api.HCPOpenShiftCluster,
	*api.HCPOpenShiftCluster,
	database.GenericDocument[api.HCPOpenShiftCluster],
]

// TestChangeFeedListWatcher exercises the change-feed-backed ListWatcher
// against both the in-memory databasetesting mock and the cosmos
// emulator (when FRONTEND_SIMULATION_TESTING=true). Each subtest gets
// its own fresh storage instance so they don't see each other's
// documents.
func TestChangeFeedListWatcher(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, env *changefeedTestEnv)
	}{
		{
			name: "new item created after list surfaces as Added",
			run: func(t *testing.T, env *changefeedTestEnv) {
				_, watcher := env.startListAndWatch(t)

				clusterRID := env.uniqueClusterResourceID("new-item")
				created := env.createCluster(t, clusterRID)

				evt := waitForEvent(t, watcher, eventDeadline)
				require.Equal(t, watch.Added, evt.Type, "expected Added")
				gotResourceID, gotVersion := metadataOf(t, evt.Object)
				require.Truef(t, strings.EqualFold(gotResourceID, clusterRID.String()),
					"event resourceID %q != created %q", gotResourceID, clusterRID.String())
				require.Equal(t, created.GetInstanceVersion(), gotVersion,
					"first event must carry the InstanceVersion the document was created with")
			},
		},
		{
			name: "existing item modified after list surfaces as Modified",
			run: func(t *testing.T, env *changefeedTestEnv) {
				clusterRID := env.uniqueClusterResourceID("modified")
				created := env.createCluster(t, clusterRID)

				_, watcher := env.startListAndWatch(t)

				updated := env.replaceCluster(t, created)

				evt := waitForEvent(t, watcher, eventDeadline)
				require.Equal(t, watch.Modified, evt.Type, "expected Modified")
				gotResourceID, gotVersion := metadataOf(t, evt.Object)
				require.Truef(t, strings.EqualFold(gotResourceID, clusterRID.String()),
					"event resourceID %q != updated %q", gotResourceID, clusterRID.String())
				require.Equal(t, updated.GetInstanceVersion(), gotVersion,
					"event must carry the InstanceVersion the document was updated to")
			},
		},
		{
			name: "item created just before list is filtered, subsequent update surfaces",
			run: func(t *testing.T, env *changefeedTestEnv) {
				// Create immediately followed by List: the change feed's
				// lookback window almost certainly includes the create's
				// timestamp, so the feed will surface the doc at v1. The
				// list, however, captures v1 in resourceIDToInstanceVersion
				// — so the watcher must drop the feed's v1 event.
				clusterRID := env.uniqueClusterResourceID("create-then-list")
				created := env.createCluster(t, clusterRID)

				_, watcher := env.startListAndWatch(t)

				updated := env.replaceCluster(t, created)

				// First (and only) event delivered to the watcher for this
				// resourceID must be the v2 update. If the v1 event leaks
				// through, this fails immediately on the type/version
				// check below.
				evt := waitForEvent(t, watcher, eventDeadline)
				require.Equal(t, watch.Modified, evt.Type, "expected Modified, not Added (v1 must have been gated by the list snapshot)")
				gotResourceID, gotVersion := metadataOf(t, evt.Object)
				require.Truef(t, strings.EqualFold(gotResourceID, clusterRID.String()),
					"event resourceID %q != updated %q", gotResourceID, clusterRID.String())
				require.Equal(t, updated.GetInstanceVersion(), gotVersion,
					"event InstanceVersion must match the update, not the create")
				require.Greaterf(t, gotVersion, created.GetInstanceVersion(),
					"delivered InstanceVersion %d must be strictly greater than the version List captured (%d)",
					gotVersion, created.GetInstanceVersion())

				// And nothing else should arrive: the v1 event was
				// dropped, the v2 event was delivered, and there are no
				// further mutations.
				assertNoEvent(t, watcher, 2*time.Second)
			},
		},
		{
			name: "deleted item produces no watch event",
			run: func(t *testing.T, env *changefeedTestEnv) {
				clusterRID := env.uniqueClusterResourceID("deleted")
				env.createCluster(t, clusterRID)

				_, watcher := env.startListAndWatch(t)

				// Drain any straggling event the list captured (the
				// cluster was created before the list, so it should
				// already be in the resourceIDToInstanceVersion map
				// — but the change feed may still surface it once;
				// the watcher's instanceVersion gate should drop it).
				// We give the change feed a short moment to do that
				// before issuing the delete.
				drainBriefly(watcher, 1*time.Second)

				env.deleteCluster(t, clusterRID)

				// The "latest version" change feed mode does not
				// surface deletes. Wait the full event deadline; any
				// delivered event is a failure of the contract.
				assertNoEvent(t, watcher, silenceDeadline)
			},
		},
		{
			name: "nested types under the same parent do not surface on the cluster watcher",
			run: func(t *testing.T, env *changefeedTestEnv) {
				// Pre-create a cluster so the list snapshot is non-empty
				// and the per-resourceID instanceVersion gate is exercised
				// for the cluster type alongside the other-type writes.
				clusterRID := env.uniqueClusterResourceID("nested-types-parent")
				existingCluster := env.createCluster(t, clusterRID)

				_, watcher := env.startListAndWatch(t)

				// Drain any straggling change-feed echo of the pre-list
				// create. The version gate should drop it, but give the
				// feed a moment to surface so the assertNoEvent below
				// is observing the steady state.
				drainBriefly(watcher, 1*time.Second)

				// Now write a bunch of *other* resource types under the
				// same subscription. None of these should surface on the
				// cluster watcher, and none should panic the type filter
				// or the deserialization path inside processDocument.
				nodePoolRID := env.uniqueNodePoolResourceID(clusterRID, "np-a")
				createdNP := env.createNodePool(t, nodePoolRID)
				env.replaceNodePool(t, createdNP)

				opRID := env.uniqueOperationResourceID("op-a")
				createdOp := env.createOperation(t, opRID, clusterRID)
				env.replaceOperation(t, createdOp)

				// Wait long enough for the change feed to have produced
				// events for all of the above. Any event delivered to
				// the cluster watcher within that window is a contract
				// violation: the desiredResourceType filter must drop
				// non-cluster docs, and CosmosToInternal must not have
				// crashed on a foreign-shape payload.
				assertNoEvent(t, watcher, silenceDeadline)

				// Sanity: the watcher is still alive and still
				// delivering events for the type it cares about. A
				// cluster update after all that noise should arrive.
				updated := env.replaceCluster(t, existingCluster)
				evt := waitForEvent(t, watcher, eventDeadline)
				require.Equal(t, watch.Modified, evt.Type, "expected Modified on the post-noise cluster update")
				gotResourceID, gotVersion := metadataOf(t, evt.Object)
				require.Truef(t, strings.EqualFold(gotResourceID, clusterRID.String()),
					"event resourceID %q != updated cluster %q", gotResourceID, clusterRID.String())
				require.Equal(t, updated.GetInstanceVersion(), gotVersion,
					"event must carry the InstanceVersion the cluster was updated to")
			},
		},
		{
			name: "concurrent writes never deliver a lower InstanceVersion than the list captured",
			run: func(t *testing.T, env *changefeedTestEnv) {
				clusterRID := env.uniqueClusterResourceID("race")
				created := env.createCluster(t, clusterRID)

				// Flood mutations starting before the list and
				// continuing through it. We use a real conditional
				// Replace loop (the only way to advance
				// InstanceVersion under the new CRUD layer) — refetch
				// after every successful update so the next replace
				// has a fresh etag.
				floodCtx, stopFlood := context.WithCancel(env.ctx)
				defer stopFlood()
				flooder := env.startFlooder(t, floodCtx, created)

				// Let the flooder build a backlog of versions before
				// we list. The change feed is asynchronous — we want
				// to capture an item that already has older versions
				// behind it on the change feed.
				time.Sleep(500 * time.Millisecond)

				_, watcher := env.startListAndWatch(t)

				// Wait for several events to make sure we're actually
				// receiving updates through the watch (not just
				// timing out), then stop the flood.
				const wantEvents = 5
				received := collectEvents(watcher, wantEvents, eventDeadline)
				stopFlood()
				flooder.waitForStop()

				// Collect any remaining buffered events for ~1s so
				// late deliveries get inspected too.
				received = append(received, drainEvents(watcher, 1*time.Second)...)

				require.GreaterOrEqualf(t, len(received), wantEvents,
					"expected at least %d events, got %d", wantEvents, len(received))

				// Contract under test: once the watcher has emitted
				// an event for a given resourceID with InstanceVersion
				// N, it must never emit a later event for the same
				// resourceID with version <= N.
				highestSeen := map[string]int64{}
				for i, evt := range received {
					rid, ver := metadataOf(t, evt.Object)
					ridLower := strings.ToLower(rid)
					prev, ok := highestSeen[ridLower]
					require.Falsef(t, ok && ver <= prev,
						"event %d for %s went backwards: prev=%d, now=%d", i, ridLower, prev, ver)
					if ver > highestSeen[ridLower] {
						highestSeen[ridLower] = ver
					}
				}

				// The flooder advanced past N versions; verify the
				// highest seen for our cluster is at least the
				// initial-create version (i.e., the watcher saw
				// post-list activity, not just nothing).
				finalVersion := highestSeen[strings.ToLower(clusterRID.String())]
				require.Greaterf(t, finalVersion, created.GetInstanceVersion(),
					"watcher did not see any update past the initial Create version (final=%d, initial=%d)",
					finalVersion, created.GetInstanceVersion())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
				env := newChangeFeedTestEnv(t, withMock)
				defer env.cleanup()
				tt.run(t, env)
			})
		})
	}
}

// changefeedTestEnv encapsulates the per-test storage state and the
// ChangeFeedListWatcher under test. Each test owns its own backing
// store (mock or cosmos emulator); resource IDs are further namespaced
// per subtest to defend against any cross-test contamination on the
// shared emulator instance.
type changefeedTestEnv struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc

	storage           integrationutils.StorageIntegrationTestInfo
	resourcesDBClient database.ResourcesDBClient
	listWatcher       *clusterChangeFeedListWatcher

	listStarted bool
}

func newChangeFeedTestEnv(t *testing.T, withMock bool) *changefeedTestEnv {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))

	var (
		storage integrationutils.StorageIntegrationTestInfo
		err     error
	)
	if withMock {
		storage, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
	} else {
		storage, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
	}
	require.NoError(t, err, "create test storage")

	resourcesDBClient := storage.ResourcesDBClient()

	listWatcher := dbinformers.NewChangeFeedListWatcher[
		api.HCPOpenShiftCluster,
		*api.HCPOpenShiftCluster,
		database.GenericDocument[api.HCPOpenShiftCluster],
	](
		[]azcorearm.ResourceType{api.ClusterResourceType},
		utilsclock.RealClock{},
		resourcesDBClient.ResourcesGlobalListers().Clusters(),
		resourcesDBClient,
		30*time.Minute,
	)

	return &changefeedTestEnv{
		t:                 t,
		ctx:               ctx,
		cancel:            cancel,
		storage:           storage,
		resourcesDBClient: resourcesDBClient,
		listWatcher:       listWatcher,
	}
}

func (e *changefeedTestEnv) cleanup() {
	// Block until the change feed watcher (and every goroutine it spawned)
	// has fully wound down. Those goroutines log through a logger bound to
	// e.t, and that logger panics if it fires after the test function has
	// returned. Stop has to run before storage.Cleanup or e.cancel(), so the
	// watcher is still happy to drain instead of fighting a torn-down DB.
	e.listWatcher.Stop()

	cleanupCtx := utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(e.t))
	e.storage.Cleanup(cleanupCtx)
	e.cancel()
}

// uniqueClusterResourceID returns a cluster resource ID that is unique
// to this subtest (so re-runs and parallel paths don't collide).
func (e *changefeedTestEnv) uniqueClusterResourceID(name string) *azcorearm.ResourceID {
	e.t.Helper()
	clusterName := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
		testSubscriptionID, testResourceGroup, clusterName)))
}

// startListAndWatch primes the listWatcher: it calls List (which spins
// up the embedded ChangeFeedWatcher and opens delivery) and returns
// both the listed items and the Watch() result. After this call the
// watcher is delivering events from the change feed.
func (e *changefeedTestEnv) startListAndWatch(t *testing.T) ([]string, *clusterChangeFeedWatcher) {
	t.Helper()
	require.False(t, e.listStarted, "startListAndWatch can only be called once per env")
	e.listStarted = true

	listObj, err := e.listWatcher.List(e.ctx, metav1.ListOptions{})
	require.NoError(t, err, "List")

	list, ok := listObj.(*metav1.List)
	require.Truef(t, ok, "List returned %T, want *metav1.List", listObj)
	listed := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Object == nil {
			continue
		}
		if cluster, ok := item.Object.(*api.HCPOpenShiftCluster); ok && cluster.ResourceID != nil {
			listed = append(listed, strings.ToLower(cluster.ResourceID.String()))
		}
	}

	watcherIface, err := e.listWatcher.Watch(e.ctx, metav1.ListOptions{})
	require.NoError(t, err, "Watch")
	watcher, ok := watcherIface.(*clusterChangeFeedWatcher)
	require.Truef(t, ok, "Watch returned %T, want *ChangeFeedWatcher", watcherIface)

	return listed, watcher
}

// createCluster creates a minimal cluster document via the production
// CRUD layer. Returns the round-tripped object so the caller has the
// authoritative InstanceVersion / CosmosETag.
func (e *changefeedTestEnv) createCluster(t *testing.T, resourceID *azcorearm.ResourceID) *api.HCPOpenShiftCluster {
	t.Helper()
	cluster := newClusterFixture(resourceID)
	created, err := e.resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		Create(e.ctx, cluster, nil)
	require.NoError(t, err, "Create cluster")
	return created
}

// replaceCluster does a conditional Replace using the existing
// CosmosETag and returns the round-tripped object.
func (e *changefeedTestEnv) replaceCluster(t *testing.T, existing *api.HCPOpenShiftCluster) *api.HCPOpenShiftCluster {
	t.Helper()
	updated := newClusterFixture(existing.ResourceID)
	updated.CosmosETag = existing.CosmosETag
	updated.InstanceVersion = existing.GetInstanceVersion()
	// Touch a field so this is a real mutation.
	updated.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateSucceeded
	replaced, err := e.resourcesDBClient.HCPClusters(existing.ResourceID.SubscriptionID, existing.ResourceID.ResourceGroupName).
		Replace(e.ctx, updated, nil)
	require.NoError(t, err, "Replace cluster")
	return replaced
}

// uniqueNodePoolResourceID returns a nodepool resource ID nested under
// the given cluster.
func (e *changefeedTestEnv) uniqueNodePoolResourceID(clusterRID *azcorearm.ResourceID, name string) *azcorearm.ResourceID {
	e.t.Helper()
	npName := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/nodePools/%s", clusterRID.String(), npName)))
}

// uniqueOperationResourceID returns an operation status resource ID
// under the test subscription.
func (e *changefeedTestEnv) uniqueOperationResourceID(name string) *azcorearm.ResourceID {
	e.t.Helper()
	opName := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	return api.Must(azcorearm.ParseResourceID(fmt.Sprintf(
		"/subscriptions/%s/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/%s",
		testSubscriptionID, opName)))
}

func (e *changefeedTestEnv) createNodePool(t *testing.T, resourceID *azcorearm.ResourceID) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	np := newNodePoolFixture(resourceID)
	clusterName := resourceID.Parent.Name
	created, err := e.resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		NodePools(clusterName).Create(e.ctx, np, nil)
	require.NoError(t, err, "Create nodepool")
	return created
}

func (e *changefeedTestEnv) replaceNodePool(t *testing.T, existing *api.HCPOpenShiftClusterNodePool) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	updated := newNodePoolFixture(existing.ResourceID)
	updated.CosmosETag = existing.CosmosETag
	updated.InstanceVersion = existing.GetInstanceVersion()
	updated.Properties.Replicas = existing.Properties.Replicas + 1
	clusterName := existing.ResourceID.Parent.Name
	replaced, err := e.resourcesDBClient.HCPClusters(existing.ResourceID.SubscriptionID, existing.ResourceID.ResourceGroupName).
		NodePools(clusterName).Replace(e.ctx, updated, nil)
	require.NoError(t, err, "Replace nodepool")
	return replaced
}

func (e *changefeedTestEnv) createOperation(t *testing.T, resourceID *azcorearm.ResourceID, externalID *azcorearm.ResourceID) *api.Operation {
	t.Helper()
	op := newOperationFixture(resourceID, externalID)
	created, err := e.resourcesDBClient.Operations(resourceID.SubscriptionID).Create(e.ctx, op, nil)
	require.NoError(t, err, "Create operation")
	return created
}

func (e *changefeedTestEnv) replaceOperation(t *testing.T, existing *api.Operation) *api.Operation {
	t.Helper()
	updated := newOperationFixture(existing.ResourceID, existing.ExternalID)
	updated.CosmosETag = existing.CosmosETag
	updated.InstanceVersion = existing.GetInstanceVersion()
	updated.Status = arm.ProvisioningStateProvisioning
	replaced, err := e.resourcesDBClient.Operations(existing.ResourceID.SubscriptionID).Replace(e.ctx, updated, nil)
	require.NoError(t, err, "Replace operation")
	return replaced
}

func (e *changefeedTestEnv) deleteCluster(t *testing.T, resourceID *azcorearm.ResourceID) {
	t.Helper()
	err := e.resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		Delete(e.ctx, resourceID.Name)
	require.NoError(t, err, "Delete cluster")
}

// flooder repeatedly Replaces the same cluster until its context is
// cancelled. Each Replace is conditional on the prior etag, so
// InstanceVersion advances monotonically.
type flooder struct {
	stopped     chan struct{}
	updateCount atomic.Int64
}

func (f *flooder) waitForStop() {
	<-f.stopped
}

func (e *changefeedTestEnv) startFlooder(t *testing.T, ctx context.Context, initial *api.HCPOpenShiftCluster) *flooder {
	t.Helper()
	f := &flooder{stopped: make(chan struct{})}
	go func() {
		defer close(f.stopped)
		current := initial
		for {
			if ctx.Err() != nil {
				return
			}
			next := newClusterFixture(current.ResourceID)
			next.CosmosETag = current.CosmosETag
			next.InstanceVersion = current.GetInstanceVersion()
			// vary a field so it's a meaningful change
			next.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateProvisioning
			if int(f.updateCount.Load())%2 == 0 {
				next.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateSucceeded
			}
			replaced, err := e.resourcesDBClient.HCPClusters(current.ResourceID.SubscriptionID, current.ResourceID.ResourceGroupName).
				Replace(ctx, next, nil)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				// transient errors (etag conflicts, etc.) — refetch and retry
				refetched, getErr := e.resourcesDBClient.HCPClusters(current.ResourceID.SubscriptionID, current.ResourceID.ResourceGroupName).
					Get(ctx, current.ResourceID.Name)
				if getErr != nil {
					if errors.Is(getErr, context.Canceled) || errors.Is(getErr, context.DeadlineExceeded) {
						return
					}
					continue
				}
				current = refetched
				continue
			}
			current = replaced
			f.updateCount.Add(1)
		}
	}()
	return f
}

func newClusterFixture(resourceID *azcorearm.ResourceID) *api.HCPOpenShiftCluster {
	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: resourceID.Name,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateAccepted,
			ClusterServiceID:  api.Ptr(api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/changefeed-test"))),
		},
	}
}

func newNodePoolFixture(resourceID *azcorearm.ResourceID) *api.HCPOpenShiftClusterNodePool {
	return &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: resourceID.Name,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: arm.ProvisioningStateAccepted,
			Replicas:          3,
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/changefeed-test/node_pools/" + resourceID.Name))),
		},
	}
}

func newOperationFixture(resourceID *azcorearm.ResourceID, externalID *azcorearm.ResourceID) *api.Operation {
	now := time.Now().UTC()
	return &api.Operation{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		OperationID:        resourceID,
		ExternalID:         externalID,
		Request:            api.OperationRequestCreate,
		Status:             arm.ProvisioningStateAccepted,
		StartTime:          now,
		LastTransitionTime: now,
	}
}

// metadataOf extracts the resourceID and InstanceVersion from an event
// object. The change-feed watcher always emits the typed object pointer
// (CosmosToInternal'd), so we can cast through CosmosMetadataAccessor.
func metadataOf(t *testing.T, obj any) (string, int64) {
	t.Helper()
	accessor, ok := obj.(arm.CosmosMetadataAccessor)
	require.Truef(t, ok, "event object %T does not implement CosmosMetadataAccessor", obj)
	rid := accessor.GetResourceID()
	require.NotNil(t, rid, "event object has nil ResourceID")
	return rid.String(), accessor.GetInstanceVersion()
}

func waitForEvent(t *testing.T, watcher *clusterChangeFeedWatcher, timeout time.Duration) watch.Event {
	t.Helper()
	select {
	case evt, ok := <-watcher.ResultChan():
		require.True(t, ok, "watcher result channel closed before delivering an event")
		return evt
	case <-time.After(timeout):
		t.Fatalf("no watch event received within %s", timeout)
	}
	return watch.Event{}
}

func assertNoEvent(t *testing.T, watcher *clusterChangeFeedWatcher, within time.Duration) {
	t.Helper()
	select {
	case evt, ok := <-watcher.ResultChan():
		if !ok {
			return // closed channel is acceptable
		}
		t.Fatalf("unexpected watch event %v for object %T", evt.Type, evt.Object)
	case <-time.After(within):
	}
}

// drainBriefly consumes whatever is on the channel for the given window.
func drainBriefly(watcher *clusterChangeFeedWatcher, within time.Duration) {
	deadline := time.After(within)
	for {
		select {
		case _, ok := <-watcher.ResultChan():
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
}

// collectEvents reads up to count events or fails the test on timeout.
func collectEvents(watcher *clusterChangeFeedWatcher, count int, timeout time.Duration) []watch.Event {
	out := make([]watch.Event, 0, count)
	deadline := time.After(timeout)
	for len(out) < count {
		select {
		case evt, ok := <-watcher.ResultChan():
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-deadline:
			return out
		}
	}
	return out
}

// drainEvents reads any events available within the window and returns them.
func drainEvents(watcher *clusterChangeFeedWatcher, within time.Duration) []watch.Event {
	var out []watch.Event
	deadline := time.After(within)
	for {
		select {
		case evt, ok := <-watcher.ResultChan():
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-deadline:
			return out
		}
	}
}

// TestActiveOperationInformer verifies that the active-operation informer
// (which uses WithShouldDeliverItemFn to filter out terminal operations):
//  1. Delivers an Added event and the lister finds the operation.
//  2. Delivers a Deleted event when the operation transitions to terminal.
//  3. The lister returns not-found after the operation becomes terminal.
func TestActiveOperationInformer(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))

		var (
			storage integrationutils.StorageIntegrationTestInfo
			err     error
		)
		if withMock {
			storage, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
		} else {
			storage, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
		}
		require.NoError(t, err, "create test storage")

		resourcesDBClient := storage.ResourcesDBClient()

		// Build the active operation informer using the same constructor
		// the backend uses — it wires WithShouldDeliverItemFn to filter
		// out terminal operations.
		activeOpInformer := backendinformers.NewActiveOperationInformerWithRelistDuration(
			resourcesDBClient.ResourcesGlobalListers().ActiveOperations(),
			resourcesDBClient,
			30*time.Minute,
		)
		activeOpLister := backendlisters.NewActiveOperationLister(activeOpInformer.GetIndexer())

		// Track events delivered by the informer.
		events := make(chan watch.Event, 10)
		_, err = activeOpInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				events <- watch.Event{Type: watch.Added, Object: obj.(*api.Operation)}
			},
			UpdateFunc: func(_, obj interface{}) {
				events <- watch.Event{Type: watch.Modified, Object: obj.(*api.Operation)}
			},
			DeleteFunc: func(obj interface{}) {
				events <- watch.Event{Type: watch.Deleted, Object: obj.(*api.Operation)}
			},
		})
		require.NoError(t, err, "AddEventHandler")

		// Start the informer and wait for the initial list to sync.
		informerDone := make(chan struct{})
		go func() {
			defer close(informerDone)
			activeOpInformer.RunWithContext(ctx)
		}()
		defer func() {
			cancel()
			<-informerDone
			cleanupCtx := utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(t))
			storage.Cleanup(cleanupCtx)
		}()
		require.True(t, cache.WaitForCacheSync(ctx.Done(), activeOpInformer.HasSynced),
			"informer cache did not sync")

		// Create an active (non-terminal) operation.
		clusterRID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/active-op-test-%d",
			testSubscriptionID, testResourceGroup, time.Now().UnixNano())))
		opRID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf(
			"/subscriptions/%s/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/active-op-%d",
			testSubscriptionID, time.Now().UnixNano())))
		op := newOperationFixture(opRID, clusterRID)
		created, err := resourcesDBClient.Operations(opRID.SubscriptionID).Create(ctx, op, nil)
		require.NoError(t, err, "Create operation")

		// Wait for the Added event from the informer.
		evt := waitForChannelEvent(t, events, eventDeadline)
		require.Equal(t, watch.Added, evt.Type, "expected Added for new active operation")
		addedOp := evt.Object.(*api.Operation)
		require.Truef(t, strings.EqualFold(addedOp.GetResourceID().String(), opRID.String()),
			"Added event resourceID %q != created %q", addedOp.GetResourceID().String(), opRID.String())
		require.Equal(t, arm.ProvisioningStateAccepted, addedOp.Status,
			"Added event must carry the non-terminal status")

		// The lister must find the active operation.
		listedOp, err := activeOpLister.Get(ctx, testSubscriptionID, opRID.Name)
		require.NoError(t, err, "lister.Get must find the active operation")
		require.Truef(t, strings.EqualFold(listedOp.GetResourceID().String(), opRID.String()),
			"lister returned wrong operation: %q", listedOp.GetResourceID().String())

		// Transition the operation to terminal (Succeeded).
		updated := newOperationFixture(opRID, clusterRID)
		updated.CosmosETag = created.CosmosETag
		updated.InstanceVersion = created.GetInstanceVersion()
		updated.Status = arm.ProvisioningStateSucceeded
		_, err = resourcesDBClient.Operations(opRID.SubscriptionID).Replace(ctx, updated, nil)
		require.NoError(t, err, "Replace operation to terminal")

		// The informer should deliver a Deleted event because
		// shouldDeliverItemFn returns false for terminal operations.
		evt = waitForChannelEvent(t, events, eventDeadline)
		require.Equal(t, watch.Deleted, evt.Type, "expected Deleted when operation becomes terminal")
		deletedOp := evt.Object.(*api.Operation)
		require.Truef(t, strings.EqualFold(deletedOp.GetResourceID().String(), opRID.String()),
			"Deleted event resourceID %q != operation %q", deletedOp.GetResourceID().String(), opRID.String())
		require.Equal(t, arm.ProvisioningStateSucceeded, deletedOp.Status,
			"Deleted event must carry the terminal status that caused removal")

		// The lister must no longer find the operation.
		_, err = activeOpLister.Get(ctx, testSubscriptionID, opRID.Name)
		require.Error(t, err, "lister.Get must return an error after the operation became terminal")
	})
}

func waitForChannelEvent(t *testing.T, ch <-chan watch.Event, timeout time.Duration) watch.Event {
	t.Helper()
	select {
	case evt := <-ch:
		return evt
	case <-time.After(timeout):
		t.Fatalf("no event received within %s", timeout)
	}
	return watch.Event{}
}

// keep imports honest if the file ever stops using sync/atomic etc.
var _ = sync.Once{}
