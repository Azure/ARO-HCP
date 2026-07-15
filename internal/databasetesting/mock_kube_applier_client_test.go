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

package databasetesting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	testSub        = "00000000-0000-0000-0000-000000000001"
	testRG         = "myrg"
	testCluster    = "mycluster"
	testNodePool   = "mynodepool"
	testDesireName = "mydesire"
)

// testMgmtID is the resourceID stamped into Spec.ManagementCluster; testMgmt
// is its lowercased-string form, used as the Cosmos partition key.
var (
	testMgmtID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-1"))
	testMgmt = strings.ToLower(testMgmtID.String())
)

func mustParse(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func newClusterApplyDesire(t *testing.T) *kubeapplier.ApplyDesire {
	t.Helper()
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParse(t,
				kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testDesireName)),
			PartitionKey: strings.ToLower(testMgmtID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: testMgmtID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{
					Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x"}}`),
				},
			},
		},
	}
}

func newNodePoolReadDesire(t *testing.T) *kubeapplier.ReadDesire {
	t.Helper()
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParse(t,
				kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
					testSub, testRG, testCluster, testNodePool, testDesireName)),
			PartitionKey: strings.ToLower(testMgmtID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testMgmtID,
			TargetItem: kubeapplier.ResourceReference{
				Resource: "configmaps", Namespace: "default", Name: "x",
			},
		},
	}
}

func TestMockKubeApplierCreateAndGet_ClusterScoped(t *testing.T) {
	ctx := context.Background()
	mock := NewMockKubeApplierDBClient()
	desire := newClusterApplyDesire(t)

	crud, err := mock.ApplyDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ApplyDesiresForCluster: %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := crud.Get(ctx, testDesireName)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mc := got.Spec.ManagementCluster; mc == nil || !strings.EqualFold(mc.String(), testMgmt) {
		t.Errorf("ManagementCluster = %v want %q", mc, testMgmt)
	}
	if got.Spec.ServerSideApply == nil || got.Spec.ServerSideApply.KubeContent == nil || !strings.Contains(string(got.Spec.ServerSideApply.KubeContent.Raw), "ConfigMap") {
		t.Errorf("KubeContent did not round-trip: %v", got.Spec.ServerSideApply)
	}
}

func TestMockKubeApplierCreateAndGet_NodePoolScoped(t *testing.T) {
	ctx := context.Background()
	mock := NewMockKubeApplierDBClient()
	desire := newNodePoolReadDesire(t)

	crud, err := mock.ReadDesiresForNodePool(testSub, testRG, testCluster, testNodePool)
	if err != nil {
		t.Fatalf("ReadDesiresForNodePool: %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := crud.Get(ctx, testDesireName)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.TargetItem.Name != "x" {
		t.Errorf("TargetItem.Name = %q want %q", got.Spec.TargetItem.Name, "x")
	}
}

func TestMockKubeApplier_PartitionKeyEnvelope(t *testing.T) {
	// Ensure InternalToCosmosKubeApplier writes the management cluster name
	// into the cosmos document's partitionKey field rather than the subscription ID.
	ctx := context.Background()
	mock := NewMockKubeApplierDBClient()
	desire := newClusterApplyDesire(t)

	crud, err := mock.ApplyDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ApplyDesiresForCluster: %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	allDocs := mock.GetAllDocuments()
	if len(allDocs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(allDocs))
	}
	for _, raw := range allDocs {
		var td database.TypedDocument
		if err := json.Unmarshal(raw, &td); err != nil {
			t.Fatalf("unmarshal typed doc: %v", err)
		}
		if td.PartitionKey != strings.ToLower(testMgmt) {
			t.Errorf("partitionKey = %q want %q", td.PartitionKey, strings.ToLower(testMgmt))
		}
		if td.PartitionKey == strings.ToLower(testSub) {
			t.Errorf("partitionKey should not equal subscription ID")
		}
	}
}

func TestMockKubeApplierGlobalLister_UnionsClusterAndNodePoolScopes(t *testing.T) {
	ctx := context.Background()
	mock, err := NewMockKubeApplierDBClientWithResources(ctx, []any{
		newClusterApplyDesire(t),
		// A second ApplyDesire under a node pool, using a different desire name.
		&kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: mustParse(t,
					kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
						testSub, testRG, testCluster, testNodePool, "other")),
				PartitionKey: strings.ToLower(testMgmtID.String()),
			},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: testMgmtID,
				Type:              kubeapplier.ApplyDesireTypeServerSideApply,
				ServerSideApply:   &kubeapplier.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"y"}}`)}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}

	iter, err := mock.Listers().ApplyDesires().List(ctx, nil)
	if err != nil {
		t.Fatalf("global ApplyDesires().List: %v", err)
	}
	count := 0
	for _, d := range iter.Items(ctx) {
		if d == nil {
			t.Errorf("nil ApplyDesire from iterator")
			continue
		}
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 ApplyDesires (cluster + nodepool), got %d", count)
	}
}

func TestMockKubeApplier_IsolatedFromMockResourcesDBClient(t *testing.T) {
	// Sanity: the kube-applier mock and the regular mock have completely
	// separate document stores. Documents written to one are not visible to
	// the other (mirroring the production container split).
	ctx := context.Background()
	kubeMock := NewMockKubeApplierDBClient()
	dbMock := NewMockResourcesDBClient()
	desire := newClusterApplyDesire(t)

	crud, err := kubeMock.ApplyDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ApplyDesiresForCluster: %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := len(dbMock.GetAllDocuments()); got != 0 {
		t.Errorf("MockResourcesDBClient saw %d documents from a kube-applier write; expected 0", got)
	}
	if got := len(kubeMock.GetAllDocuments()); got != 1 {
		t.Errorf("MockKubeApplierDBClient missing the document it just stored: %d", got)
	}
}
