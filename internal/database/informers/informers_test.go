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

package informers_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testNodePool = "np"
)

// Management cluster identifiers. The *ID values are the resourceIDs the
// fixtures stamp into Spec.ManagementCluster; the lowercased-string forms are
// the partition-key values passed to lister.ListForManagementCluster, which
// after the *Desire API change is the lowercased(rid.String()).
var (
	testMgmtAID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	testMgmtBID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/2/managementclusters/mgmt-b"))
	testMgmtA = strings.ToLower(testMgmtAID.String())
	testMgmtB = strings.ToLower(testMgmtBID.String())
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
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mgmt,
			KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)},
		},
	}
}

// waitForSync runs the informers and blocks until each one's HasSynced is true.
// We use the shorter relist duration for tests (250ms) to keep them fast.
func startAndSync(t *testing.T, ctx context.Context, info informers.KubeApplierInformers) {
	t.Helper()
	go info.RunWithContext(ctx)
	apply, _ := info.ApplyDesires()
	delete, _ := info.DeleteDesires()
	read, _ := info.ReadDesires()
	if !cache.WaitForCacheSync(ctx.Done(), apply.HasSynced, delete.HasSynced, read.HasSynced) {
		t.Fatal("informers did not sync")
	}
}

func TestKubeApplierInformers_ListByManagementCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		// mgmt-a: cluster-scoped + nodepool-scoped under cluster c
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "a1"),
			testMgmtAID),
		newApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
			testMgmtAID),
		// mgmt-b: cluster-scoped under a different cluster
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
			testMgmtBID),
	})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}

	relistDuration := 250 * time.Millisecond
	info := informers.NewKubeApplierInformersWithRelistDuration(ctx, mock.GlobalListers(), &relistDuration)
	startAndSync(t, ctx, info)

	_, lister := info.ApplyDesires()

	all, err := lister.List(ctx)
	if err != nil {
		t.Fatalf("ApplyDesireLister.List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ApplyDesireLister.List len = %d, want 3", len(all))
	}

	gotA, err := lister.ListForManagementCluster(ctx, testMgmtA)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-a: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("ListForManagementCluster mgmt-a: len = %d, want 2", len(gotA))
	}
	for _, d := range gotA {
		mc := d.GetManagementCluster()
		if mc == nil || !strings.EqualFold(mc.String(), testMgmtA) {
			t.Errorf("ListForManagementCluster mgmt-a returned desire with mgmt=%v", mc)
		}
	}

	gotB, err := lister.ListForManagementCluster(ctx, testMgmtB)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-b: %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("ListForManagementCluster mgmt-b: len = %d, want 1", len(gotB))
	}
}

func TestKubeApplierInformers_ListForCluster_UnionsClusterAndNodePool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "a1"),
			testMgmtAID),
		newApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
			testMgmtAID),
		// Different cluster: should NOT show up under our cluster's index.
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
			testMgmtBID),
	})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}

	relistDuration := 250 * time.Millisecond
	info := informers.NewKubeApplierInformersWithRelistDuration(ctx, mock.GlobalListers(), &relistDuration)
	startAndSync(t, ctx, info)

	_, lister := info.ApplyDesires()

	got, err := lister.ListForCluster(ctx, testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ListForCluster: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListForCluster len = %d, want 2 (cluster + nodepool under cluster)", len(got))
	}

	gotNP, err := lister.ListForNodePool(ctx, testSub, testRG, testCluster, testNodePool)
	if err != nil {
		t.Fatalf("ListForNodePool: %v", err)
	}
	if len(gotNP) != 1 {
		t.Errorf("ListForNodePool len = %d, want 1 (nodepool only)", len(gotNP))
	}
}

func TestKubeApplierInformers_GetByID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "a1"),
			testMgmtAID),
	})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}

	relistDuration := 250 * time.Millisecond
	info := informers.NewKubeApplierInformersWithRelistDuration(ctx, mock.GlobalListers(), &relistDuration)
	startAndSync(t, ctx, info)

	_, lister := info.ApplyDesires()

	got, err := lister.GetForCluster(ctx, testSub, testRG, testCluster, "a1")
	if err != nil {
		t.Fatalf("GetForCluster a1: %v", err)
	}
	if got == nil {
		t.Fatal("GetForCluster a1: nil result")
	}
	mc := got.GetManagementCluster()
	if mc == nil || !strings.EqualFold(mc.String(), testMgmtA) {
		t.Errorf("GetForCluster a1: management = %v, want %q", mc, testMgmtA)
	}
}

// Compile-time assertion: the listers package's interface is satisfied by the
// implementation returned by the informer factory.
var (
	_ listers.ApplyDesireLister  = (listers.ApplyDesireLister)(nil)
	_ listers.DeleteDesireLister = (listers.DeleteDesireLister)(nil)
	_ listers.ReadDesireLister   = (listers.ReadDesireLister)(nil)
)
