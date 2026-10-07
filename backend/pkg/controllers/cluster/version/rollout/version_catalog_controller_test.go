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

package rollout

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

type catalogTestLister struct {
	fleetlisters.ControlPlaneVersionRolloutLister
	rollouts []*fleetapi.ControlPlaneVersionRollout
	err      error
}

func (l *catalogTestLister) List(context.Context) ([]*fleetapi.ControlPlaneVersionRollout, error) {
	return l.rollouts, l.err
}

func TestReconcileVersionCatalog(t *testing.T) {
	t.Parallel()
	metadata := coreapi.CosmosMetadata{
		ResourceID:   metadataapi.Must(coreapihelpers.ToOpenShiftVersionCatalogResourceID(coreapi.OpenShiftVersionCatalogName)),
		PartitionKey: strings.ToLower(coreapi.ProviderNamespace),
	}
	existingMetadata := metadata
	existingMetadata.ExistingCosmosUID = "catalog-uid"
	existingMetadata.CosmosETag = "catalog-etag"
	existingMetadata.InstanceVersion = 7
	entries := []coreapi.OpenShiftVersionCatalogEntry{
		{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "candidate"}},
		{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "fast"}, Available: true},
		{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "nightly"}},
		{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}},
		{Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "nightly"}, Available: true},
		{Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, Available: true},
	}
	existing := &coreapi.OpenShiftVersionCatalog{CosmosMetadata: existingMetadata, Entries: entries}
	missingVersion := newTestRollout("stable-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	missingVersion.Spec.Version = coreapi.VersionProfile{}
	rollouts := []*fleetapi.ControlPlaneVersionRollout{
		newTestRollout("stable-4.21", v("4.21.9"), fleetapi.ControlPlaneVersionRolloutStatus{ClusterCountByDesiredExactVersion: map[string]int64{"4.21.5": 3}}),
		newTestRollout("candidate-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("stable-4.19", v("4.19.9"), fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("fast-4.20", v("4.20.3"), fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("stable-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("nightly-4.21", v("4.21.0-0.nightly"), fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("nightly-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
		newTestRollout("nightly-4.19", v("4.19.0-0.nightly"), fleetapi.ControlPlaneVersionRolloutStatus{}),
	}
	for _, tc := range []struct {
		name     string
		rollouts []*fleetapi.ControlPlaneVersionRollout
		existing *coreapi.OpenShiftVersionCatalog
		want     *coreapi.OpenShiftVersionCatalog
	}{
		{name: "initial empty", want: &coreapi.OpenShiftVersionCatalog{CosmosMetadata: metadata, Entries: []coreapi.OpenShiftVersionCatalogEntry{}}},
		{name: "sorted public projection at floor", rollouts: rollouts, want: &coreapi.OpenShiftVersionCatalog{CosmosMetadata: metadata, Entries: entries}},
		{name: "patch status and ordering do not affect projection", rollouts: rollouts, existing: existing},
		{name: "empty unchanged", existing: &coreapi.OpenShiftVersionCatalog{Entries: []coreapi.OpenShiftVersionCatalogEntry{}}},
		{name: "nil entries unchanged", existing: &coreapi.OpenShiftVersionCatalog{}},
		{name: "remove all", existing: existing, want: &coreapi.OpenShiftVersionCatalog{CosmosMetadata: existingMetadata, Entries: []coreapi.OpenShiftVersionCatalogEntry{}}},
		{
			name: "withdraw entries and availability", existing: existing,
			rollouts: []*fleetapi.ControlPlaneVersionRollout{newTestRollout("fast-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{})},
			want:     &coreapi.OpenShiftVersionCatalog{CosmosMetadata: existingMetadata, Entries: []coreapi.OpenShiftVersionCatalogEntry{{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "fast"}}}},
		},
		{
			name: "selection alone makes available", existing: existing,
			rollouts: []*fleetapi.ControlPlaneVersionRollout{newTestRollout("stable-4.20", v("4.20.1"), fleetapi.ControlPlaneVersionRolloutStatus{})},
			want:     &coreapi.OpenShiftVersionCatalog{CosmosMetadata: existingMetadata, Entries: []coreapi.OpenShiftVersionCatalogEntry{{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}, Available: true}}},
		},
		{name: "missing profile defers initial projection", rollouts: []*fleetapi.ControlPlaneVersionRollout{rollouts[0], missingVersion}},
		{name: "missing profile preserves existing projection", rollouts: []*fleetapi.ControlPlaneVersionRollout{rollouts[0], missingVersion}, existing: existing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := tc.existing.DeepCopy()
			var beforeRollouts []*fleetapi.ControlPlaneVersionRollout
			for _, rollout := range tc.rollouts {
				beforeRollouts = append(beforeRollouts, rollout.DeepCopy())
			}
			desired := reconcileVersionCatalog(tc.rollouts, tc.existing)
			require.Empty(t, cmp.Diff(tc.want, desired, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "catalog projection must match")
			require.Empty(t, cmp.Diff(before, tc.existing, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "existing catalog must not be mutated")
			require.Empty(t, cmp.Diff(beforeRollouts, tc.rollouts, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "rollouts must not be mutated or reordered")
			if desired != nil && tc.existing != nil {
				require.NotSame(t, tc.existing, desired)
				require.NotSame(t, tc.existing.ResourceID, desired.ResourceID, "metadata must be deep copied")
				desired.ResourceID.Name = "changed"
				if len(desired.Entries) > 0 {
					desired.Entries[0].Available = !desired.Entries[0].Available
				}
				require.Empty(t, cmp.Diff(before, tc.existing, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "result must not alias input")
			}
		})
	}
}

func TestVersionCatalogSyncErrorsAndNoOp(t *testing.T) {
	t.Parallel()
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	lister := &catalogTestLister{}
	syncer := &versionCatalogSyncer{resourcesDBClient: db, rolloutLister: lister}
	require.NoError(t, syncer.SyncOnce(t.Context(), versionCatalogKey{}))
	before := db.GetAllDocuments()
	require.NoError(t, syncer.SyncOnce(t.Context(), versionCatalogKey{}))
	lister.err = errors.New("list failed")
	require.ErrorIs(t, syncer.SyncOnce(t.Context(), versionCatalogKey{}), lister.err)
	require.Empty(t, cmp.Diff(before, db.GetAllDocuments()), "no-op and failed reads must not write")
}

func TestVersionCatalogWaitsForProfile(t *testing.T) {
	t.Parallel()
	rollout := newTestRollout("nightly-4.21", v("4.21.0-0.nightly"), fleetapi.ControlPlaneVersionRolloutStatus{})
	before := rollout.DeepCopy()
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	syncer := &versionCatalogSyncer{
		resourcesDBClient: db,
		rolloutLister:     &catalogTestLister{rollouts: []*fleetapi.ControlPlaneVersionRollout{rollout}},
	}
	require.NoError(t, syncer.SyncOnce(t.Context(), versionCatalogKey{}))
	catalog, err := db.OpenShiftVersionCatalogs().Get(t.Context(), coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]coreapi.OpenShiftVersionCatalogEntry{{Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "nightly"}, Available: true}}, catalog.Entries))
	require.Empty(t, cmp.Diff(before, rollout, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "catalog projection must not mutate inputs")
	rollout.Spec.Version = coreapi.VersionProfile{}
	documents := db.GetAllDocuments()
	require.NoError(t, syncer.SyncOnce(t.Context(), versionCatalogKey{}))
	require.Empty(t, cmp.Diff(documents, db.GetAllDocuments()), "absent profiles must not change the published catalog")
}

type catalogTestInformer struct {
	cache.SharedIndexInformer
	handler cache.ResourceEventHandler
	options cache.HandlerOptions
	synced  atomic.Bool
}

func (i *catalogTestInformer) HasSynced() bool { return i.synced.Load() }

func (i *catalogTestInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	i.handler = handler
	i.options = options
	return nil, nil
}

type catalogTestInformers struct {
	fleetinformers.FleetInformers
	informer *catalogTestInformer
	lister   fleetlisters.ControlPlaneVersionRolloutLister
}

func (f catalogTestInformers) ControlPlaneVersionRollouts() (cache.SharedIndexInformer, fleetlisters.ControlPlaneVersionRolloutLister) {
	return f.informer, f.lister
}

func TestVersionCatalogQueue(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	informer := &catalogTestInformer{}
	controller := NewOpenShiftVersionCatalogController(db, catalogTestInformers{
		informer: informer, lister: fleetlisters.NewControlPlaneVersionRolloutLister(indexer),
	})
	require.NotNil(t, informer.options.ResyncPeriod, "rollout callbacks must request informer resync")
	require.Equal(t, 5*time.Minute, *informer.options.ResyncPeriod)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		controller.Run(ctx, 2)
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	require.Never(t, func() bool { return len(db.GetAllDocuments()) != 0 }, 150*time.Millisecond, 10*time.Millisecond,
		"must not publish before Fleet cache sync")
	informer.synced.Store(true)
	waitFor := func(entries []coreapi.OpenShiftVersionCatalogEntry) {
		t.Helper()
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			catalog, err := db.OpenShiftVersionCatalogs().Get(ctx, coreapi.OpenShiftVersionCatalogName)
			require.NoError(t, err, "catalog must be published")
			// A nil expectation means an empty catalog, regardless of slice representation.
			require.Empty(t, cmp.Diff(entries, catalog.Entries, cmpopts.EquateEmpty()), "published catalog entries must match")
		}, 5*time.Second, 10*time.Millisecond, "catalog must converge after the event")
	}
	waitFor(nil) // Startup publishes even when the initial list is empty.
	rollout := newTestRollout("stable-4.20", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	require.NoError(t, indexer.Add(rollout))
	informer.handler.OnAdd(rollout, false)
	want := []coreapi.OpenShiftVersionCatalogEntry{{Version: coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}}}
	waitFor(want)
	updated := rollout.DeepCopy()
	updated.Spec.BestExactVersion = v("4.20.1")
	require.NoError(t, indexer.Update(updated))
	informer.handler.OnUpdate(rollout, updated)
	want[0].Available = true
	waitFor(want)

	// An unchanged rollout's resync callback repairs a stale catalog.
	catalog, err := db.OpenShiftVersionCatalogs().Get(ctx, coreapi.OpenShiftVersionCatalogName)
	require.NoError(t, err)
	catalog.Entries = nil
	_, err = db.OpenShiftVersionCatalogs().Replace(ctx, catalog, nil)
	require.NoError(t, err)
	informer.handler.OnUpdate(updated, updated)
	waitFor(want)

	require.NoError(t, indexer.Delete(updated))
	informer.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: updated})
	waitFor(nil)
	require.NoError(t, indexer.Add(updated))
	informer.handler.OnAdd(updated, false)
	waitFor(want)
	require.NoError(t, indexer.Delete(updated))
	informer.handler.OnDelete(updated)
	waitFor(nil)
}
