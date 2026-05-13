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
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testNodePool = "np"
	testMgmt     = "mgmt-a"
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

	clusterScoped := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				testSub, testRG, testCluster, "cluster-d")),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testMgmt,
			KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)},
		},
	}
	nodePoolScoped := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
				testSub, testRG, testCluster, testNodePool, "np-d")),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testMgmt,
			KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"Secret"}`)},
		},
	}

	mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{clusterScoped, nodePoolScoped})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}
	l := &listertesting.DBApplyDesireLister{Client: mock}

	all, err := l.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List len = %d, want 2", len(all))
	}

	if got, err := l.GetForCluster(ctx, testSub, testRG, testCluster, "cluster-d"); err != nil {
		t.Errorf("GetForCluster cluster-d: %v", err)
	} else if got.GetManagementCluster() != testMgmt {
		t.Errorf("GetForCluster cluster-d: management = %q, want %q", got.GetManagementCluster(), testMgmt)
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
	scoped, err := l.ListForManagementCluster(ctx, testMgmt)
	if err != nil {
		t.Fatalf("ListForManagementCluster: %v", err)
	}
	if len(scoped) != 2 {
		t.Errorf("ListForManagementCluster len = %d, want 2", len(scoped))
	}

	// A different mgmt cluster has nothing in this store.
	emptyScope, err := l.ListForManagementCluster(ctx, "mgmt-other")
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-other: %v", err)
	}
	if len(emptyScope) != 0 {
		t.Errorf("ListForManagementCluster mgmt-other len = %d, want 0", len(emptyScope))
	}
}
