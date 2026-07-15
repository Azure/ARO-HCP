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

package listertesting_test

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testNodePool = "np"
)

// Management cluster identifiers. testMgmtID goes into Spec.ManagementCluster
// and is what callers pass to ListForManagementCluster; testMgmt is the
// lowercased-string form used for string comparisons in test assertions.
var (
	testMgmtID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	testMgmt        = strings.ToLower(testMgmtID.String())
	testMgmtOtherID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-other"))
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func TestDBApplyDesireLister_RoundTripViaMock(t *testing.T) {
	ctx := context.Background()

	clusterScoped := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplier.ToClusterScopedApplyDesireResourceIDString(
				testSub, testRG, testCluster, "cluster-d")),
			PartitionKey: strings.ToLower(testMgmtID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: testMgmtID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			ServerSideApply:   &kubeapplier.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)}},
		},
	}
	nodePoolScoped := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
				testSub, testRG, testCluster, testNodePool, "np-d")),
			PartitionKey: strings.ToLower(testMgmtID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: testMgmtID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			ServerSideApply:   &kubeapplier.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"Secret"}`)}},
		},
	}

	mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{clusterScoped, nodePoolScoped})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}
	// Wrap the single per-MC mock in the plural registry under testMgmtID so the
	// DB-backed lister's ListForManagementCluster(testMgmtID) call resolves to
	// this mock via clients.For(rid).
	clients := databasetesting.NewMockKubeApplierDBClients()
	clients.Register(testMgmtID, mock)
	lister := &listertesting.SliceManagementClusterLister{
		ManagementClusters: []*fleet.ManagementCluster{
			{CosmosMetadata: api.CosmosMetadata{ResourceID: testMgmtID}, ResourceID: testMgmtID},
		},
	}
	l := &listertesting.DBApplyDesireLister{Clients: clients, Lister: lister}

	all, err := l.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List len = %d, want 2", len(all))
	}

	if got, err := l.GetForCluster(ctx, testSub, testRG, testCluster, "cluster-d"); err != nil {
		t.Errorf("GetForCluster cluster-d: %v", err)
	} else if mc := got.GetManagementCluster(); mc == nil || !strings.EqualFold(mc.String(), testMgmt) {
		t.Errorf("GetForCluster cluster-d: management = %v, want %q", mc, testMgmt)
	}

	if got, err := l.GetForNodePool(ctx, testSub, testRG, testCluster, testNodePool, "np-d"); err != nil {
		t.Errorf("GetForNodePool np-d: %v", err)
	} else if got == nil {
		t.Errorf("GetForNodePool np-d: nil result")
	}

	// Cluster-scoped Get for the nodepool name should NotFound.
	if _, err := l.GetForCluster(ctx, testSub, testRG, testCluster, "np-d"); !database.IsNotFoundError(err) {
		t.Errorf("GetForCluster np-d: want NotFound, got %v", err)
	}

	// PartitionListers gets just this management cluster's docs.
	scoped, err := l.ListForManagementCluster(ctx, testMgmtID)
	if err != nil {
		t.Fatalf("ListForManagementCluster: %v", err)
	}
	if len(scoped) != 2 {
		t.Errorf("ListForManagementCluster len = %d, want 2", len(scoped))
	}

	// A different mgmt cluster has nothing in this store.
	emptyScope, err := l.ListForManagementCluster(ctx, testMgmtOtherID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-other: %v", err)
	}
	if len(emptyScope) != 0 {
		t.Errorf("ListForManagementCluster mgmt-other len = %d, want 0", len(emptyScope))
	}
}
