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

package placement

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

func pendingClusterResourceID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testClusterSubscriptionID +
			"/resourceGroups/" + testClusterResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name))
}

// spcForCluster builds a ServiceProviderCluster for a cluster name with the given
// Spec (intent) and Status (observed) placements. Pass nil for either to leave it unset.
func spcForCluster(name string, specPlacement, statusPlacement *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	clusterRID := pendingClusterResourceID(name)
	spcRID := metadataapi.Must(azcorearm.ParseResourceID(
		clusterRID.String() + "/" + coreapi.ServiceProviderClusterResourceTypeName + "/" + coreapi.ServiceProviderClusterResourceName))
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcRID, PartitionKey: strings.ToLower(testClusterSubscriptionID)},
	}
	if specPlacement != nil {
		spc.Spec.ManagementClusterResourceID = specPlacement
	}
	if statusPlacement != nil {
		spc.Status.ManagementClusterResourceID = statusPlacement
	}
	return spc
}

func pendingStrings(ids []*azcorearm.ResourceID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.ToLower(id.String()))
	}
	return out
}

func TestPendingCleanupSyncer_SyncOnce(t *testing.T) {
	ctx := context.Background()

	const thisStamp = "1"
	thisMC := metadataapi.Must(fleetapi.ToManagementClusterResourceID(thisStamp))
	otherMC := metadataapi.Must(fleetapi.ToManagementClusterResourceID("2"))

	// Effective placement = Status (CS reality) when set, else Spec.
	//   a: Status here, Spec nil                 => keep
	//   b: Status other, Spec here (Status wins) => remove
	//   c: Status nil, Spec here (Spec fallback) => keep
	//   d: Status nil, Spec nil (in progress)    => keep
	//   e: Status other                          => remove
	//   f: SPC missing                           => remove
	serviceProviderClusters := []*coreapi.ServiceProviderCluster{
		spcForCluster("a", nil, thisMC),
		spcForCluster("b", thisMC, otherMC),
		spcForCluster("c", thisMC, nil),
		spcForCluster("d", nil, nil),
		spcForCluster("e", nil, otherMC),
	}
	spcLister := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: serviceProviderClusters}

	pending := []*azcorearm.ResourceID{
		pendingClusterResourceID("a"),
		pendingClusterResourceID("b"),
		pendingClusterResourceID("c"),
		pendingClusterResourceID("d"),
		pendingClusterResourceID("e"),
		pendingClusterResourceID("f"),
	}
	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
	doc := &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(thisStamp)),
			PartitionKey: thisStamp,
		},
		Status: fleetapi.ManagementClusterSchedulingStatus{PendingAssignedClusters: pending},
	}
	created, err := fleetDB.Stamps().ManagementClusters(thisStamp).Scheduling().Create(ctx, doc, nil)
	require.NoError(t, err)

	// The syncer reads the scheduling document from the informer-cache lister
	// (seeded with the created doc so its etag matches the fleet DB for the
	// Replace write path).
	schedulingLister := &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: []*fleetapi.ManagementClusterScheduling{created}}
	syncer := &pendingCleanupSyncer{serviceProviderClusterLister: spcLister, managementClusterSchedulingLister: schedulingLister, fleetDBClient: fleetDB}
	require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ManagementClusterKey{StampIdentifier: thisStamp}))

	updated, err := fleetDB.Stamps().ManagementClusters(thisStamp).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)

	kept := pendingStrings(updated.Status.PendingAssignedClusters)
	assert.ElementsMatch(t, []string{
		strings.ToLower(pendingClusterResourceID("a").String()),
		strings.ToLower(pendingClusterResourceID("c").String()),
		strings.ToLower(pendingClusterResourceID("d").String()),
	}, kept, "keep entries whose effective placement (Status first, else Spec) points here or is still nil")
}

func TestPendingCleanupSyncer_SyncOnce_NoChangeWhenAllValid(t *testing.T) {
	ctx := context.Background()

	const thisStamp = "1"
	thisMC := metadataapi.Must(fleetapi.ToManagementClusterResourceID(thisStamp))

	spcLister := &corelistertesting.SliceServiceProviderClusterLister{
		ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spcForCluster("a", nil, thisMC)},
	}

	fleetDB := fleetcosmosstoragetesting.NewMockFleetDBClient()
	doc := &fleetapi.ManagementClusterScheduling{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(fleetapi.ToManagementClusterSchedulingResourceID(thisStamp)),
			PartitionKey: thisStamp,
		},
		Status: fleetapi.ManagementClusterSchedulingStatus{PendingAssignedClusters: []*azcorearm.ResourceID{pendingClusterResourceID("a")}},
	}
	created, err := fleetDB.Stamps().ManagementClusters(thisStamp).Scheduling().Create(ctx, doc, nil)
	require.NoError(t, err)
	beforeETag := created.CosmosETag
	require.NotEmpty(t, beforeETag, "test fixture: created scheduling doc should carry an etag")

	schedulingLister := &fleetlistertesting.SliceManagementClusterSchedulingLister{Schedulings: []*fleetapi.ManagementClusterScheduling{created}}
	syncer := &pendingCleanupSyncer{serviceProviderClusterLister: spcLister, managementClusterSchedulingLister: schedulingLister, fleetDBClient: fleetDB}
	require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ManagementClusterKey{StampIdentifier: thisStamp}))

	updated, err := fleetDB.Stamps().ManagementClusters(thisStamp).Scheduling().Get(ctx, fleetapi.SchedulingResourceName)
	require.NoError(t, err)
	require.Len(t, updated.Status.PendingAssignedClusters, 1)
	assert.Equal(t, strings.ToLower(pendingClusterResourceID("a").String()), strings.ToLower(updated.Status.PendingAssignedClusters[0].String()))
	// The sweep changed nothing, so the semantic-deepequals guard must skip the
	// Replace entirely: a write would have bumped the server-assigned etag.
	assert.Equal(t, beforeETag, updated.CosmosETag, "no Replace should occur when the sweep changes nothing (semantic deepequals skip)")
}
