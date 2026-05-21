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

package listertesting

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
)

func newTestManagementCluster(name, shardID string) *fleet.ManagementCluster {
	resourceID := api.Must(fleet.ToManagementClusterResourceID(name))
	return &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Status: fleet.ManagementClusterStatus{
			ClusterServiceProvisionShardID: ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/" + shardID))),
		},
	}
}

func TestSliceManagementClusterLister(t *testing.T) {
	mc1 := newTestManagementCluster("m1", "11111111-1111-1111-1111-111111111111")
	mc2 := newTestManagementCluster("m2", "22222222-2222-2222-2222-222222222222")

	lister := &SliceManagementClusterLister{
		ManagementClusters: []*fleet.ManagementCluster{mc1, mc2},
	}

	ctx := context.Background()

	t.Run("List returns all management clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching management cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, "m1")
		require.NoError(t, err)
		assert.Equal(t, "m1", result.ResourceID.Parent.Name)
	})

	t.Run("Get returns not found for non-existent management cluster", func(t *testing.T) {
		_, err := lister.Get(ctx, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("GetByCSProvisionShard returns matching management cluster", func(t *testing.T) {
		csShardID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/11111111-1111-1111-1111-111111111111"))
		result, err := lister.GetByCSProvisionShardID(ctx, csShardID.ID())
		require.NoError(t, err)
		assert.Equal(t, "m1", result.ResourceID.Parent.Name)
	})

	t.Run("GetByCSProvisionShard returns not found for non-existent shard", func(t *testing.T) {
		csShardID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/99999999-9999-9999-9999-999999999999"))
		_, err := lister.GetByCSProvisionShardID(ctx, csShardID.ID())
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("GetByCSProvisionShard returns error for duplicate shards", func(t *testing.T) {
		mc3 := newTestManagementCluster("m3", "11111111-1111-1111-1111-111111111111")
		dupLister := &SliceManagementClusterLister{
			ManagementClusters: []*fleet.ManagementCluster{mc1, mc3},
		}
		csShardID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/11111111-1111-1111-1111-111111111111"))
		_, err := dupLister.GetByCSProvisionShardID(ctx, csShardID.ID())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected at most 1")
	})
}

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testNodePool = "np"
)

// Management cluster identifiers. *ID values go into Spec.ManagementCluster
// and are also what callers pass to ListForManagementCluster.
var (
	testMgmtAID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	testMgmtBID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/2/managementclusters/mgmt-b"))
	testMgmtA = strings.ToLower(testMgmtAID.String())
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func newApplyDesire(t *testing.T, idStr string, mgmt *azcorearm.ResourceID) *kubeapplier.ApplyDesire {
	t.Helper()
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: mustParseID(t, idStr)},
		Spec:           kubeapplier.ApplyDesireSpec{ManagementCluster: mgmt},
	}
}

// fixtureDesires returns four ApplyDesires:
//   - clusterA: mgmt-a, cluster-scoped, name=a1
//   - clusterA-np: mgmt-a, nodepool-scoped, name=a2
//   - clusterB: mgmt-b, cluster-scoped (different cluster), name=b1
//   - clusterB-other-rg: mgmt-b, cluster-scoped, different rg
func fixtureDesires(t *testing.T) []*kubeapplier.ApplyDesire {
	t.Helper()
	return []*kubeapplier.ApplyDesire{
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "a1"),
			testMgmtAID),
		newApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
			testMgmtAID),
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
			testMgmtBID),
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, "other-rg", testCluster, "b2"),
			testMgmtBID),
	}
}

func TestSliceApplyDesireLister_List(t *testing.T) {
	ctx := context.Background()
	l := &SliceApplyDesireLister{Desires: fixtureDesires(t)}
	got, err := l.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("List() len = %d, want 4", len(got))
	}
}

func TestSliceApplyDesireLister_GetForCluster(t *testing.T) {
	ctx := context.Background()
	l := &SliceApplyDesireLister{Desires: fixtureDesires(t)}

	// Existing cluster-scoped desire returns the right item.
	got, err := l.GetForCluster(ctx, testSub, testRG, testCluster, "a1")
	if err != nil {
		t.Fatalf("GetForCluster a1: %v", err)
	}
	if got == nil {
		t.Fatal("GetForCluster a1: nil result")
	}
	if mc := got.GetManagementCluster(); mc == nil || !strings.EqualFold(mc.String(), testMgmtA) {
		t.Errorf("GetForCluster a1: management = %v, want %q", mc, testMgmtA)
	}

	// A name that exists only as a nodepool-scoped desire is NotFound at the cluster scope.
	if _, err := l.GetForCluster(ctx, testSub, testRG, testCluster, "a2"); !database.IsNotFoundError(err) {
		t.Errorf("GetForCluster a2 (nodepool-only): want NotFound, got %v", err)
	}

	// Wrong subscription — NotFound.
	if _, err := l.GetForCluster(ctx, "different-sub", testRG, testCluster, "a1"); !database.IsNotFoundError(err) {
		t.Errorf("GetForCluster wrong sub: want NotFound, got %v", err)
	}
}

func TestSliceApplyDesireLister_GetForNodePool(t *testing.T) {
	ctx := context.Background()
	l := &SliceApplyDesireLister{Desires: fixtureDesires(t)}

	got, err := l.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "a2")
	if err != nil {
		t.Fatalf("GetForNodePool a2: %v", err)
	}
	if got == nil {
		t.Fatal("GetForNodePool a2: nil")
	}

	// A name that only exists as cluster-scoped is NotFound at nodepool scope.
	if _, err := l.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "a1"); !database.IsNotFoundError(err) {
		t.Errorf("GetForNodePool a1 (cluster-only): want NotFound, got %v", err)
	}
}

func TestSliceApplyDesireLister_ListForManagementCluster(t *testing.T) {
	ctx := context.Background()
	l := &SliceApplyDesireLister{Desires: fixtureDesires(t)}

	gotA, err := l.ListForManagementCluster(ctx, testMgmtAID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-a: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("ListForManagementCluster mgmt-a: len = %d, want 2 (cluster + nodepool)", len(gotA))
	}

	gotB, err := l.ListForManagementCluster(ctx, testMgmtBID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-b: %v", err)
	}
	if len(gotB) != 2 {
		t.Errorf("ListForManagementCluster mgmt-b: len = %d, want 2", len(gotB))
	}

	// Case-insensitive: a resourceID parsed from the same path but with mixed case
	// still matches the fixtures (which stamped the lowercased form into Spec).
	upperRID := mustParseID(t, strings.ToUpper(testMgmtAID.String()))
	gotUpperA, err := l.ListForManagementCluster(ctx, upperRID)
	if err != nil {
		t.Fatalf("ListForManagementCluster uppercased mgmt-a: %v", err)
	}
	if len(gotUpperA) != 2 {
		t.Errorf("ListForManagementCluster uppercased mgmt-a: len = %d, want 2 (case-insensitive)", len(gotUpperA))
	}
}

func TestSliceApplyDesireLister_ListForCluster_IncludesNodePoolScoped(t *testing.T) {
	ctx := context.Background()
	l := &SliceApplyDesireLister{Desires: fixtureDesires(t)}

	got, err := l.ListForCluster(ctx, testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ListForCluster: %v", err)
	}
	// Should pick up both a1 (cluster-scoped) AND a2 (nodepool-scoped under this cluster).
	if len(got) != 2 {
		t.Errorf("ListForCluster len = %d, want 2 (cluster + nodepool under cluster)", len(got))
	}

	// Different cluster name yields different (smaller) set.
	gotOther, err := l.ListForCluster(ctx, testSub, testRG, "other-cluster")
	if err != nil {
		t.Fatalf("ListForCluster other-cluster: %v", err)
	}
	if len(gotOther) != 1 {
		t.Errorf("ListForCluster other-cluster len = %d, want 1", len(gotOther))
	}
}

func TestSliceApplyDesireLister_ListForNodePool_OnlyNodePoolScoped(t *testing.T) {
	ctx := context.Background()
	l := &SliceApplyDesireLister{Desires: fixtureDesires(t)}

	got, err := l.ListForNodePool(ctx, testSub, testRG, testCluster, testNodePool)
	if err != nil {
		t.Fatalf("ListForNodePool: %v", err)
	}
	// Only the nodepool-scoped a2 should match — NOT the cluster-scoped a1.
	if len(got) != 1 {
		t.Errorf("ListForNodePool len = %d, want 1 (nodepool-scoped only)", len(got))
	}
}
