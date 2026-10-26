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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// namespaceJSON renders a mirrored namespace as the kube-applier would store it.
func namespaceJSON(t *testing.T, name, uid, phase string, deletionTimestamp *time.Time) []byte {
	t.Helper()
	deletion := ""
	if deletionTimestamp != nil {
		deletion = fmt.Sprintf(`"deletionTimestamp": %q,`, deletionTimestamp.UTC().Format(time.RFC3339))
	}
	return []byte(fmt.Sprintf(`{
		"apiVersion": "v1",
		"kind": "Namespace",
		"metadata": {"name": %q, "uid": %q, %s "resourceVersion": "1"},
		"spec": {"finalizers": ["kubernetes"]},
		"status": {"phase": %q}
	}`, name, uid, deletion, phase))
}

// newObservedSyncer wires a syncer whose ReadDesire already carries the given
// mirrored namespace, so SyncOnce runs straight through to the gates.
func newObservedSyncer(
	t *testing.T,
	ctx context.Context,
	cleanup *fleetapi.StuckClusterCleanup,
	namespaceContent []byte,
) (*preflightSyncer, *fleetcosmosstoragetesting.MockFleetDBClient) {
	t.Helper()

	mockFleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{cleanup})
	require.NoError(t, err)

	managementClusterResourceID, err := fleetapihelpers.ToManagementClusterResourceID(retentionTestStamp)
	require.NoError(t, err)

	readDesireName := ReadDesireNameFor(cleanup.ResourceID.Name)
	desireIDString := kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(retentionTestStamp, readDesireName)
	readDesire := controllerutil.BuildReadDesire(desireIDString, managementClusterResourceID,
		namespaceTargetFor(cleanup.Spec.NamespaceName))
	readDesire.Status.KubeContent = &runtime.RawExtension{Raw: namespaceContent}

	kubeApplierClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	kubeApplierClients.Register(managementClusterResourceID, kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient())

	return &preflightSyncer{
		fleetDBClient:        mockFleetDB,
		kubeApplierDBClients: kubeApplierClients,
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{readDesire},
		},
		applyDesireLister: &kubeapplierlistertesting.SliceApplyDesireLister{},
	}, mockFleetDB
}

func newPendingCleanup(t *testing.T, namespaceName string) *fleetapi.StuckClusterCleanup {
	t.Helper()
	name := stuckCleanupNameForTest(namespaceName)
	resourceID, err := fleetapihelpers.ToStuckClusterCleanupResourceID(retentionTestStamp, name)
	require.NoError(t, err)
	return &fleetapi.StuckClusterCleanup{
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
			Phase: fleetapi.StuckClusterCleanupPhasePending,
		},
	}
}

// The defect this gate was added for, exercised through SyncOnce rather than
// against evaluateEligibility directly: a mistyped namespace name landing on a
// healthy namespace passes every ownership check, because no cluster record
// refers to it either. Only the eligibility gate refuses it.
func TestSyncOnceRefusesHealthyNamespace(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-production-typo-target"

	cleanup := newPendingCleanup(t, namespaceName)
	content := namespaceJSON(t, namespaceName, "11111111-2222-3333-4444-555555555555", "Active", nil)
	syncer, mockFleetDB := newObservedSyncer(t, ctx, cleanup, content)

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))

	stored, err := mockFleetDB.Stamps().ManagementClusters(retentionTestStamp).
		StuckClusterCleanups().Get(ctx, stuckCleanupNameForTest(namespaceName))
	require.NoError(t, err)

	require.Equal(t, fleetapi.StuckClusterCleanupPhaseRefused, stored.Status.Phase,
		"a healthy namespace must be refused, not inspected")
	require.NotNil(t, stored.Status.Preflight)
	require.NotNil(t, stored.Status.Preflight.NamespaceEligible)
	require.False(t, *stored.Status.Preflight.NamespaceEligible)
	require.Equal(t, "NamespaceNotTerminating", stored.Status.Preflight.IneligibleReason)
}

// A namespace that has only just started deleting is teardown in progress, not
// a wedge; acting on it would race HyperShift's deprovision finalizer.
func TestSyncOnceRefusesNamespaceDeletingNormally(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-production-just-started"

	cleanup := newPendingCleanup(t, namespaceName)
	recent := time.Now().Add(-30 * time.Second)
	content := namespaceJSON(t, namespaceName, "22222222-3333-4444-5555-666666666666", namespacePhaseTerminating, &recent)
	syncer, mockFleetDB := newObservedSyncer(t, ctx, cleanup, content)

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))

	stored, err := mockFleetDB.Stamps().ManagementClusters(retentionTestStamp).
		StuckClusterCleanups().Get(ctx, stuckCleanupNameForTest(namespaceName))
	require.NoError(t, err)

	require.Equal(t, fleetapi.StuckClusterCleanupPhaseRefused, stored.Status.Phase)
	require.Equal(t, "DeletionInProgress", stored.Status.Preflight.IneligibleReason)
}

// A genuinely wedged namespace clears eligibility and reaches the ownership
// checks. Without identifiers those cannot complete, so the preflight fails
// closed and reports Inspected with an incomplete verdict rather than a pass.
func TestSyncOnceAdmitsWedgedNamespaceAndFailsClosed(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const namespaceName = "ocm-production-genuinely-stuck"

	cleanup := newPendingCleanup(t, namespaceName)
	longAgo := time.Now().Add(-72 * time.Hour)
	content := namespaceJSON(t, namespaceName, "33333333-4444-5555-6666-777777777777", namespacePhaseTerminating, &longAgo)
	syncer, mockFleetDB := newObservedSyncer(t, ctx, cleanup, content)

	require.NoError(t, syncer.SyncOnce(ctx, keyFor(namespaceName)))

	stored, err := mockFleetDB.Stamps().ManagementClusters(retentionTestStamp).
		StuckClusterCleanups().Get(ctx, stuckCleanupNameForTest(namespaceName))
	require.NoError(t, err)

	require.Equal(t, fleetapi.StuckClusterCleanupPhaseInspected, stored.Status.Phase)
	require.NotNil(t, stored.Status.Preflight.NamespaceEligible)
	require.True(t, *stored.Status.Preflight.NamespaceEligible, "a wedged namespace must clear eligibility")
	require.Equal(t, "ChecksIncomplete", stored.Status.Preflight.RefusalReason,
		"no identifiers were supplied, so ownership cannot be confirmed and preflight must fail closed")

	// The UID is pinned on first observation so a later recreation is detected.
	require.Equal(t, "33333333-4444-5555-6666-777777777777", stored.Spec.NamespaceUID)

	// Reaching a terminal phase starts the retention clock.
	require.NotNil(t, stored.Status.CompletedAt)
	require.False(t, stored.Status.CompletedAt.After(time.Now()),
		"the retention clock must not be stamped in the future")
}
