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

package billing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestBillingOrphanCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, uid, reference string
		missingCluster       bool
		wantCandidate        bool
		wantReasons          []string
	}{
		{name: "absent cluster", missingCluster: true, wantCandidate: true},
		{name: "referenced", uid: "doc", reference: "doc"},
		{name: "reference protects legacy UID", reference: "doc"},
		{name: "creation awaiting link", uid: "doc", wantReasons: []string{"UnresolvedClusterBillingIdentity"}},
		{name: "legacy awaiting backfill", wantReasons: []string{"UnresolvedClusterBillingIdentity"}},
		{name: "different UID without link", uid: "other", wantReasons: []string{"UnresolvedClusterBillingIdentity"}},
		{name: "UID contradicts reference", uid: "doc", reference: "other", wantReasons: []string{"UnresolvedClusterBillingIdentity"}},
		{name: "other inconsistent identity", uid: "other", reference: "third", wantReasons: []string{"UnresolvedClusterBillingIdentity"}},
		{name: "cluster consistently references another record", uid: "other", reference: "other", wantCandidate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := mustParseTime("2026-10-21T12:00:00Z")
			var reasons []string
			logger := funcr.NewJSON(func(line string) {
				var entry map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &entry))
				if entry["event"] == "billing_orphan_identity_unresolved" {
					reasons = append(reasons, entry["reason"].(string))
				}
			}, funcr.Options{})
			ctx := utils.ContextWithLogger(t.Context(), logger)
			doc := billingcosmosstorage.NewBillingDocument("doc", newTestClusterResourceID(t))
			doc.CreationTime = now.Add(-time.Hour)
			db := billingcosmosstoragetesting.NewMockBillingDBClient()
			crud := db.BillingDocs(testSubscriptionID)
			require.NoError(t, crud.Create(ctx, doc))
			cached, err := crud.GetByID(ctx, doc.ID)
			require.NoError(t, err)
			clusters := &corelistertesting.SliceClusterLister{}
			if !tc.missingCluster {
				cluster := newTestCluster(t, tc.uid, coreapi.ProvisioningStateSucceeded, nil)
				cluster.ServiceProviderProperties.BillingDocumentCosmosID = tc.reference
				clusters.Clusters = []*coreapi.Cluster{cluster}
			}
			controller := NewOrphanedBillingCleanupController(clocktesting.NewFakePassiveClock(now), db, clusters,
				&corelistertesting.SliceBillingLister{BillingDocuments: []*billingcosmosstorage.BillingDocument{cached}})
			require.NoError(t, controller.SyncOnce(ctx, "default"))
			persisted, err := crud.GetByID(ctx, doc.ID)
			require.NoError(t, err)
			if diff := cmp.Diff(tc.wantCandidate, persisted.OrphanedSince != nil); diff != "" {
				t.Fatalf("candidate (-want +got):\n%s", diff)
			}
			require.Nil(t, persisted.DeletionTime)
			if diff := cmp.Diff(tc.wantReasons, reasons); diff != "" {
				t.Fatalf("diagnostics (-want +got):\n%s", diff)
			}
		})
	}
}

type failingOrphanClusterLister struct {
	corelisters.ClusterLister
	err error
}

func (l failingOrphanClusterLister) Get(context.Context, string, string, string) (*coreapi.Cluster, error) {
	return nil, l.err
}

func TestOrphanBillingGracePeriod(t *testing.T) {
	now := mustParseTime("2026-10-21T12:00:00Z")
	old := now.Add(-24 * time.Hour)
	for _, tc := range []struct {
		name                   string
		since, deleted         *time.Time
		uid, reference         string
		lookupErr              error
		concurrentCutoff       *time.Time
		missingResourceID      bool
		missingDocumentID      bool
		mismatchedSubscription bool
		wantSince, wantDeleted *time.Time
		wantConflict           bool
		wantUnchanged          bool
	}{
		{name: "first missing observation only records candidate", wantSince: &now},
		{name: "before grace", since: ptr.To(old.Add(time.Nanosecond)), wantSince: ptr.To(old.Add(time.Nanosecond))},
		{name: "at grace boundary", since: &old, wantSince: &old, wantDeleted: &now},
		{name: "future observation waits", since: ptr.To(now.Add(time.Hour)), wantSince: ptr.To(now.Add(time.Hour))},
		{name: "zero observation restarts grace", since: ptr.To(time.Time{}), wantSince: &now},
		{name: "referenced cancels candidate", since: &old, uid: "doc", reference: "doc"},
		{name: "incomplete linking cancels candidate", since: &old, uid: "doc"},
		{name: "ambiguous identity cancels candidate", since: &old, uid: "other"},
		{name: "missing resource ID preserves document", since: &old, missingResourceID: true, wantSince: &old, wantUnchanged: true},
		{name: "missing document ID preserves document", since: &old, missingDocumentID: true, wantSince: &old, wantUnchanged: true},
		{name: "conflicting subscription preserves document", since: &old, mismatchedSubscription: true, wantSince: &old, wantUnchanged: true},
		{name: "different linked record after grace", uid: "other", reference: "other", since: &old, wantSince: &old, wantDeleted: &now},
		{name: "existing cutoff preserved", since: &old, deleted: &old, wantSince: &old, wantDeleted: &old},
		{name: "lookup failure does not authorize cutoff", since: &old, lookupErr: context.DeadlineExceeded, wantSince: &old},
		{name: "concurrent cutoff prevents starting candidate", concurrentCutoff: &old, wantDeleted: &old, wantConflict: true},
		{name: "concurrent cutoff prevents overwriting cutoff", concurrentCutoff: &old, since: &old, wantSince: &old, wantDeleted: &old, wantConflict: true},
		{name: "concurrent cutoff prevents clearing candidate", concurrentCutoff: &old, since: &old, uid: "doc", reference: "doc", wantSince: &old, wantDeleted: &old, wantConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			db := billingcosmosstoragetesting.NewMockBillingDBClient()
			crud := db.BillingDocs(testSubscriptionID)
			doc := billingcosmosstorage.NewBillingDocument("doc", newTestClusterResourceID(t))
			doc.CreationTime = now.Add(-365 * 24 * time.Hour)
			doc.OrphanedSince, doc.DeletionTime = tc.since, tc.deleted
			doc.LastBillingTimeOfMngtEvent = &old
			require.NoError(t, crud.Create(ctx, doc))
			before, err := crud.GetByID(ctx, doc.ID)
			require.NoError(t, err)
			cached := before.DeepCopy()
			if tc.concurrentCutoff != nil {
				var concurrent billingcosmosstorage.BillingDocumentPatchOperations
				concurrent.SetDeletionTime(*tc.concurrentCutoff)
				require.NoError(t, crud.PatchByID(ctx, doc.ID, concurrent))
			}
			if tc.missingResourceID {
				cached.ResourceID = nil
			}
			if tc.missingDocumentID {
				cached.ID = ""
			}
			if tc.mismatchedSubscription {
				cached.ResourceID.SubscriptionID = "different-subscription"
			}
			clusters := &corelistertesting.SliceClusterLister{}
			if tc.uid != "" || tc.reference != "" {
				cluster := newTestCluster(t, tc.uid, coreapi.ProvisioningStateSucceeded, &doc.CreationTime)
				cluster.ServiceProviderProperties.BillingDocumentCosmosID = tc.reference
				clusters.Clusters = []*coreapi.Cluster{cluster}
			}
			lister := corelisters.ClusterLister(clusters)
			if tc.lookupErr != nil {
				lister = failingOrphanClusterLister{ClusterLister: clusters, err: tc.lookupErr}
			}
			controller := NewOrphanedBillingCleanupController(clocktesting.NewFakePassiveClock(now), db, lister,
				&corelistertesting.SliceBillingLister{BillingDocuments: []*billingcosmosstorage.BillingDocument{cached}})
			err = controller.SyncOnce(ctx, "default")
			switch {
			case tc.lookupErr != nil:
				require.ErrorIs(t, err, tc.lookupErr)
			case tc.wantConflict:
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "%v", err)
			default:
				require.NoError(t, err)
			}
			persisted, err := crud.GetByID(ctx, doc.ID)
			require.NoError(t, err)
			if tc.wantUnchanged {
				if diff := cmp.Diff(before, persisted, cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
					if a == nil || b == nil {
						return a == b
					}
					return a.String() == b.String()
				})); diff != "" {
					t.Fatalf("document changed (-want +got):\n%s", diff)
				}
			}
			if diff := cmp.Diff([]*time.Time{tc.wantSince, tc.wantDeleted, &old}, []*time.Time{persisted.OrphanedSince, persisted.DeletionTime, persisted.LastBillingTimeOfMngtEvent}); diff != "" {
				t.Fatalf("timestamps (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]*time.Time{tc.since, tc.deleted}, []*time.Time{cached.OrphanedSince, cached.DeletionTime}); diff != "" {
				t.Fatalf("cached input mutated (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBillingDeletionPatchPreservesCutoff(t *testing.T) {
	earlier := mustParseTime("2026-10-01T10:00:00Z")
	later := mustParseTime("2026-10-01T11:00:00Z")
	for _, tc := range []struct {
		name           string
		existingCutoff *time.Time
		wantCutoff     time.Time
		wantConflict   bool
	}{
		{name: "unset cutoff", wantCutoff: later},
		{name: "condition preserves earlier cutoff", existingCutoff: &earlier, wantCutoff: earlier, wantConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := billingcosmosstoragetesting.NewMockBillingDBClient()
			crud := db.BillingDocs(testSubscriptionID)
			doc := billingcosmosstorage.NewBillingDocument("doc", newTestClusterResourceID(t))
			doc.CreationTime = earlier.Add(-time.Hour)
			doc.DeletionTime = tc.existingCutoff
			require.NoError(t, crud.Create(t.Context(), doc))
			var proposed billingcosmosstorage.BillingDocumentPatchOperations
			proposed.SetDeletionTime(later)
			err := crud.PatchByID(t.Context(), doc.ID, proposed)
			if tc.wantConflict {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "%v", err)
			} else {
				require.NoError(t, err)
			}
			persisted, err := crud.GetByID(t.Context(), doc.ID)
			require.NoError(t, err)
			if diff := cmp.Diff(&tc.wantCutoff, persisted.DeletionTime); diff != "" {
				t.Fatalf("cutoff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOrphanBillingSweepContinuesAfterMissingDocument(t *testing.T) {
	now := mustParseTime("2026-10-21T12:00:00Z")
	expired := now.Add(-48 * time.Hour)
	for _, tc := range []struct {
		name                          string
		since, wantSince, wantDeleted *time.Time
	}{
		{name: "record candidacy", wantSince: &now},
		{name: "record cutoff", since: &expired, wantSince: &expired, wantDeleted: &now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			db := billingcosmosstoragetesting.NewMockBillingDBClient()
			missing := billingcosmosstorage.NewBillingDocument("removed", newTestClusterResourceID(t))
			missing.OrphanedSince = tc.since
			active := billingcosmosstorage.NewBillingDocument("active", newTestClusterResourceID(t))
			active.CreationTime = expired.Add(-time.Hour)
			active.OrphanedSince = tc.since
			crud := db.BillingDocs(testSubscriptionID)
			require.NoError(t, crud.Create(ctx, active))
			controller := NewOrphanedBillingCleanupController(clocktesting.NewFakePassiveClock(now), db,
				&corelistertesting.SliceClusterLister{},
				&corelistertesting.SliceBillingLister{BillingDocuments: []*billingcosmosstorage.BillingDocument{missing, active}})
			require.NoError(t, controller.SyncOnce(ctx, "default"))
			persisted, err := crud.GetByID(ctx, active.ID)
			require.NoError(t, err)
			if diff := cmp.Diff([]*time.Time{tc.wantSince, tc.wantDeleted}, []*time.Time{persisted.OrphanedSince, persisted.DeletionTime}); diff != "" {
				t.Fatalf("remaining document (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOrphanBillingRestartAndReferenceRecovery(t *testing.T) {
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	start := mustParseTime("2026-10-21T12:00:00Z")
	db := billingcosmosstoragetesting.NewMockBillingDBClient()
	crud := db.BillingDocs(testSubscriptionID)
	doc := billingcosmosstorage.NewBillingDocument("doc", newTestClusterResourceID(t))
	doc.CreationTime = start.Add(-time.Hour)
	require.NoError(t, crud.Create(ctx, doc))
	for _, step := range []struct {
		name                   string
		elapsed                time.Duration
		referenced             bool
		wantSince, wantDeleted *time.Time
	}{
		{name: "start", wantSince: &start},
		{name: "reference reappears", elapsed: 12 * time.Hour, referenced: true},
		{name: "new candidacy", elapsed: 13 * time.Hour, wantSince: ptr.To(start.Add(13 * time.Hour))},
		{name: "old deadline cannot authorize cutoff", elapsed: 24 * time.Hour, wantSince: ptr.To(start.Add(13 * time.Hour))},
		{name: "new deadline", elapsed: 37 * time.Hour, wantSince: ptr.To(start.Add(13 * time.Hour)), wantDeleted: ptr.To(start.Add(37 * time.Hour))},
	} {
		t.Run(step.name, func(t *testing.T) {
			cached, err := crud.GetByID(ctx, doc.ID)
			require.NoError(t, err)
			clusters := &corelistertesting.SliceClusterLister{}
			if step.referenced {
				cluster := newTestCluster(t, "doc", coreapi.ProvisioningStateSucceeded, &doc.CreationTime)
				cluster.ServiceProviderProperties.BillingDocumentCosmosID = "doc"
				clusters.Clusters = []*coreapi.Cluster{cluster}
			}
			// A fresh controller has no remembered observations from earlier passes.
			controller := NewOrphanedBillingCleanupController(clocktesting.NewFakePassiveClock(start.Add(step.elapsed)), db, clusters,
				&corelistertesting.SliceBillingLister{BillingDocuments: []*billingcosmosstorage.BillingDocument{cached}})
			require.NoError(t, controller.SyncOnce(ctx, "default"))
			persisted, err := crud.GetByID(ctx, doc.ID)
			require.NoError(t, err)
			if diff := cmp.Diff([]*time.Time{step.wantSince, step.wantDeleted}, []*time.Time{persisted.OrphanedSince, persisted.DeletionTime}); diff != "" {
				t.Fatalf("timestamps (-want +got):\n%s", diff)
			}
		})
	}
}
