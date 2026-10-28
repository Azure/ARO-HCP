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

package stuckcleanup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	backendcontrollerutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const retentionTestStamp = "1"

// newTerminalCleanup builds a settled request whose retention clock started
// completedAgo in the past. A zero completedAgo leaves CompletedAt unset,
// standing in for a document written before the field existed.
func newTerminalCleanup(t *testing.T, namespaceName string, completedAgo time.Duration) *fleetapi.StuckClusterCleanup {
	t.Helper()
	name := stuckCleanupNameForTest(namespaceName)
	resourceID, err := fleetapihelpers.ToStuckClusterCleanupResourceID(retentionTestStamp, name)
	require.NoError(t, err)

	cleanup := &fleetapi.StuckClusterCleanup{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(retentionTestStamp),
		},
		Spec: fleetapi.StuckClusterCleanupSpec{
			Mode:          fleetapi.StuckClusterCleanupModeInspect,
			NamespaceName: namespaceName,
			RequestReason: "AROSLSRE-2477 sweep",
		},
		Status: fleetapi.StuckClusterCleanupStatus{
			Phase: fleetapi.StuckClusterCleanupPhaseInspected,
		},
	}
	if completedAgo > 0 {
		completedAt := metav1.NewTime(time.Now().Add(-completedAgo))
		cleanup.Status.CompletedAt = &completedAt
	}
	return cleanup
}

// stuckCleanupNameForTest mirrors the admin API's naming so the test addresses
// the same document the handler would create. It is intentionally a local copy:
// the handler lives in another module, and importing it here would couple the
// controller's tests to the admin server.
func stuckCleanupNameForTest(namespaceName string) string {
	return "ns-" + strings.ToLower(namespaceName)
}

func newRetentionSyncer(t *testing.T, ctx context.Context, cleanups ...*fleetapi.StuckClusterCleanup) (*preflightSyncer, *fleetcosmosstoragetesting.MockFleetDBClient) {
	t.Helper()
	resources := make([]any, 0, len(cleanups))
	for _, cleanup := range cleanups {
		resources = append(resources, cleanup)
	}

	var mockFleetDB *fleetcosmosstoragetesting.MockFleetDBClient
	var err error
	if len(resources) > 0 {
		mockFleetDB, err = fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, resources)
		require.NoError(t, err)
	} else {
		mockFleetDB = fleetcosmosstoragetesting.NewMockFleetDBClient()
	}

	return &preflightSyncer{
		fleetDBClient: mockFleetDB,
		// No kube-applier client is registered for this management cluster, so
		// the ReadDesire deletion is a no-op. That is the path a real stamp
		// takes when its container is unreachable, and it must not wedge
		// retention.
		kubeApplierDBClients: kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients(),
	}, mockFleetDB
}

func keyFor(namespaceName string) backendcontrollerutils.StuckClusterCleanupKey {
	return backendcontrollerutils.StuckClusterCleanupKey{
		StampIdentifier: retentionTestStamp,
		Name:            stuckCleanupNameForTest(namespaceName),
	}
}

// A settled request that predates the retention field gets a fresh clock rather
// than being retired on sight.
func TestReconcileTerminalStampsMissingCompletedAt(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-stage-nostamp"

	cleanup := newTerminalCleanup(t, namespaceName, 0)
	syncer, mockFleetDB := newRetentionSyncer(t, ctx, cleanup)
	crud := mockFleetDB.Stamps().ManagementClusters(retentionTestStamp).StuckClusterCleanups()

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))

	stored, err := crud.Get(ctx, stuckCleanupNameForTest(namespaceName))
	require.NoError(t, err, "a request with no retention stamp must not be deleted on sight")
	require.NotNil(t, stored.Status.CompletedAt, "the retention clock must be started")
}

func TestReconcileTerminalKeepsRequestInsideRetentionWindow(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-stage-recent"

	cleanup := newTerminalCleanup(t, namespaceName, terminalRetentionPeriod/2)
	syncer, mockFleetDB := newRetentionSyncer(t, ctx, cleanup)
	crud := mockFleetDB.Stamps().ManagementClusters(retentionTestStamp).StuckClusterCleanups()

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))

	_, err := crud.Get(ctx, stuckCleanupNameForTest(namespaceName))
	require.NoError(t, err, "findings must remain readable inside the retention window")
}

func TestReconcileTerminalRetiresExpiredRequest(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-stage-expired"

	cleanup := newTerminalCleanup(t, namespaceName, terminalRetentionPeriod+time.Hour)
	syncer, mockFleetDB := newRetentionSyncer(t, ctx, cleanup)
	crud := mockFleetDB.Stamps().ManagementClusters(retentionTestStamp).StuckClusterCleanups()

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))

	_, err := crud.Get(ctx, stuckCleanupNameForTest(namespaceName))
	require.True(t, cosmosstorageutils.IsNotFoundError(err),
		"an expired request must be retired; nothing else collects these documents, got err=%v", err)
}

// Reconciling a request that has already been deleted must be a no-op rather
// than an error: the queue can outlive the document.
func TestSyncOnceToleratesMissingRequest(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	syncer, _ := newRetentionSyncer(t, ctx)
	require.NoError(t, syncer.SyncOnce(ctx, keyFor("ocm-stage-gone")))
}

// Retirement is idempotent: a second pass over an already-retired request must
// not error.
func TestReconcileTerminalIsIdempotent(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-stage-twice"

	cleanup := newTerminalCleanup(t, namespaceName, terminalRetentionPeriod+time.Hour)
	syncer, _ := newRetentionSyncer(t, ctx, cleanup)

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))
	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))
}
