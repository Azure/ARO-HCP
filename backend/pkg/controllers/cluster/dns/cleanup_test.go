// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

type clusterReadError struct {
	corelisters.ClusterLister
	err error
}

func (l clusterReadError) Get(context.Context, string, string, string) (*coreapi.Cluster, error) {
	return nil, l.err
}

func TestCleanupSyncOnce(t *testing.T) {
	owner := testCluster("bcdf2.example.com", "cluster")
	deleting := owner.DeepCopy()
	stamp := metav1.NewTime(testNow)
	deleting.ServiceProviderProperties.DeletionTimestamp = &stamp
	completed := deleting.DeepCopy()
	completed.ServiceProviderProperties.PendingClusterServiceID = nil
	completed.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &stamp
	replacement := owner.DeepCopy()
	replacement.ServiceProviderProperties.ClusterUID = "replacement"
	failure := errors.New("injected failure")
	conflict := corecosmosstoragetesting.NewPreconditionFailedError()
	tests := []struct {
		name                         string
		cachedOwner, liveOwner       *coreapi.Cluster
		cachedErr, liveErr, writeErr error
		deletionAge                  *time.Duration
		wantMark, wantDelete         bool
		wantReads                    int
		wantErr                      string
	}{
		{name: "active owner", cachedOwner: owner},
		{name: "deleting owner", cachedOwner: deleting},
		{name: "CS cleaned but cluster still exists", cachedOwner: completed},
		{name: "stale absent cache", liveOwner: owner, wantReads: 1},
		{name: "stale replacement cache", cachedOwner: replacement, liveOwner: owner, wantReads: 1},
		{name: "owner gone", wantMark: true, wantReads: 1},
		{name: "old incarnation gone", cachedOwner: replacement, liveOwner: replacement, wantMark: true, wantReads: 1},
		{name: "cache error", cachedErr: failure, wantErr: failure.Error()},
		{name: "live error", liveErr: failure, wantReads: 1, wantErr: failure.Error()},
		{name: "hold begins with failed replace", writeErr: failure, wantMark: true, wantReads: 1, wantErr: failure.Error()},
		{name: "stale mark conflict", writeErr: conflict, wantMark: true, wantReads: 1},
		{name: "before boundary", deletionAge: durationPointer(deletedClaimRetention - time.Nanosecond)},
		{name: "future timestamp", deletionAge: durationPointer(-time.Hour)},
		{name: "exact boundary", deletionAge: durationPointer(deletedClaimRetention), wantDelete: true, wantReads: 1},
		{name: "after boundary", deletionAge: durationPointer(deletedClaimRetention + time.Hour), wantDelete: true, wantReads: 1},
		{name: "recheck live owner at expiry", deletionAge: durationPointer(deletedClaimRetention), liveOwner: owner, wantReads: 1},
		{name: "expiry live error", deletionAge: durationPointer(deletedClaimRetention), liveErr: failure, wantReads: 1, wantErr: failure.Error()},
		{name: "stale delete conflict", deletionAge: durationPointer(deletedClaimRetention), writeErr: conflict, wantDelete: true, wantReads: 1},
		{name: "delete failure", deletionAge: durationPointer(deletedClaimRetention), writeErr: failure, wantDelete: true, wantReads: 1, wantErr: failure.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := testClaim()
			claim.CreatedAt = metav1.NewTime(testNow.Add(-30 * 24 * time.Hour))
			claim.CosmosETag = "observed-etag"
			if tt.deletionAge != nil {
				stamp := metav1.NewTime(testNow.Add(-*tt.deletionAge))
				claim.DeletionTimestamp = &stamp
			}
			before := claim.DeepCopy()
			resources := newRecordingResources()
			resources.clusters.getObject = tt.liveOwner
			resources.clusters.getErr = tt.liveErr
			fleet := &recordingFleet{}
			fleet.claims.writeErr = tt.writeErr
			owners := &corelistertesting.SliceClusterLister{}
			if tt.cachedOwner != nil {
				owners.Clusters = []*coreapi.Cluster{tt.cachedOwner}
			}
			var ownerLister corelisters.ClusterLister = owners
			if tt.cachedErr != nil {
				ownerLister = clusterReadError{err: tt.cachedErr}
			}
			controller := &cleanupController{resourcesDBClient: resources, fleetDBClient: fleet, clusterLister: ownerLister, dnsClaimLister: claimLister(t, claim, nil), clock: clocktesting.NewFakePassiveClock(testNow)}
			err := controller.SyncOnce(testContext(t), claimKey{BaseDomain: claim.BaseDomain})
			if len(tt.wantErr) > 0 {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantReads, resources.clusters.gets)
			var wantReplacements []*fleetapi.DNSClaim
			if tt.wantMark {
				want := claim.DeepCopy()
				stamp := metav1.NewTime(testNow)
				want.DeletionTimestamp = &stamp
				wantReplacements = []*fleetapi.DNSClaim{want}
			}
			compare(t, wantReplacements, fleet.claims.replacements)
			if tt.wantDelete {
				compare(t, []string{"bcdf2"}, fleet.claims.deletes)
				require.Len(t, fleet.claims.deleteETags, 1)
				require.Equal(t, claim.CosmosETag, fleet.claims.deleteETags[0])
			} else {
				require.Empty(t, fleet.claims.deletes)
			}
			require.Empty(t, fleet.claims.creates)
			require.Empty(t, resources.clusters.replacements)
			require.Empty(t, resources.providers.replacements)
			compare(t, before, claim)
		})
	}
}

func durationPointer(d time.Duration) *time.Duration { return &d }

func TestCleanupHoldSurvivesRestartAndStaleDelete(t *testing.T) {
	ctx := testContext(t)
	claim := testClaim()
	fleet, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{claim})
	require.NoError(t, err)
	resources := corecosmosstoragetesting.NewMockResourcesDBClient()
	clock := clocktesting.NewFakePassiveClock(testNow)
	stored, err := fleet.DNSClaims("example.com").Get(ctx, "bcdf2")
	require.NoError(t, err)
	controller := &cleanupController{resourcesDBClient: resources, fleetDBClient: fleet, clusterLister: &corelistertesting.SliceClusterLister{}, dnsClaimLister: claimLister(t, stored, nil), clock: clock}
	require.NoError(t, controller.SyncOnce(ctx, claimKey{BaseDomain: claim.BaseDomain}))
	stored, err = fleet.DNSClaims("example.com").Get(ctx, "bcdf2")
	require.NoError(t, err)
	require.Equal(t, testNow, stored.DeletionTimestamp.Time)
	clock.SetTime(testNow.Add(deletedClaimRetention - time.Second))
	controller = &cleanupController{resourcesDBClient: resources, fleetDBClient: fleet, clusterLister: &corelistertesting.SliceClusterLister{}, dnsClaimLister: claimLister(t, stored, nil), clock: clock}
	require.NoError(t, controller.SyncOnce(ctx, claimKey{BaseDomain: claim.BaseDomain}))
	retained, err := fleet.DNSClaims("example.com").Get(ctx, "bcdf2")
	require.NoError(t, err)
	compare(t, stored, retained)
	// The stale observation must not delete a newer claim incarnation.
	replacement := stored.DeepCopy()
	replacement.ClusterUUID = "new-owner"
	replacement.DeletionTimestamp = nil
	replacement, err = fleet.DNSClaims("example.com").Replace(ctx, replacement, nil)
	require.NoError(t, err)
	clock.SetTime(testNow.Add(deletedClaimRetention))
	require.NoError(t, controller.SyncOnce(ctx, claimKey{BaseDomain: claim.BaseDomain}))
	retained, err = fleet.DNSClaims("example.com").Get(ctx, "bcdf2")
	require.NoError(t, err)
	compare(t, replacement, retained)
	// A current, expired observation can be soft-deleted through ordinary CRUD.
	replacement.DeletionTimestamp = stored.DeletionTimestamp
	replacement, err = fleet.DNSClaims("example.com").Replace(ctx, replacement, nil)
	require.NoError(t, err)
	controller.dnsClaimLister = claimLister(t, replacement, nil)
	require.NoError(t, controller.SyncOnce(ctx, claimKey{BaseDomain: claim.BaseDomain}))
	_, err = fleet.DNSClaims("example.com").Get(ctx, "bcdf2")
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
}
