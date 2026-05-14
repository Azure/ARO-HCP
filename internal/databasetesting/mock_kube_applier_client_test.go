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

	kubeapplierapi "github.com/Azure/ARO-HCP/internal/apis/kubeapplier"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	testSub        = "00000000-0000-0000-0000-000000000001"
	testRG         = "myrg"
	testCluster    = "mycluster"
	testNodePool   = "mynodepool"
	testMgmt       = "mgmt-1"
	testDesireName = "mydesire"
)

func mustParse(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func newClusterApplyDesire(t *testing.T) *kubeapplierapi.ApplyDesire {
	t.Helper()
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: mustParse(t,
				kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testDesireName)),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testMgmt,
			KubeContent: &runtime.RawExtension{
				Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x"}}`),
			},
		},
	}
}

func newNodePoolReadDesire(t *testing.T) *kubeapplierapi.ReadDesire {
	t.Helper()
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: mustParse(t,
				kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
					testSub, testRG, testCluster, testNodePool, testDesireName)),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: testMgmt,
			TargetItem: kubeapplierapi.ResourceReference{
				Resource: "configmaps", Namespace: "default", Name: "x",
			},
		},
	}
}

func TestMockKubeApplierCreateAndGet_ClusterScoped(t *testing.T) {
	ctx := context.Background()
	mock := NewMockKubeApplierDBClient()
	desire := newClusterApplyDesire(t)

	parent := database.ResourceParent{
		SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster,
	}
	crud, err := mock.KubeApplier(testMgmt).ApplyDesires(parent)
	if err != nil {
		t.Fatalf("ApplyDesires(parent): %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := crud.Get(ctx, testDesireName)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.ManagementCluster != testMgmt {
		t.Errorf("ManagementCluster = %q want %q", got.Spec.ManagementCluster, testMgmt)
	}
	if got.Spec.KubeContent == nil || !strings.Contains(string(got.Spec.KubeContent.Raw), "ConfigMap") {
		t.Errorf("KubeContent did not round-trip: %v", got.Spec.KubeContent)
	}
}

func TestMockKubeApplierCreateAndGet_NodePoolScoped(t *testing.T) {
	ctx := context.Background()
	mock := NewMockKubeApplierDBClient()
	desire := newNodePoolReadDesire(t)

	parent := database.ResourceParent{
		SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster, NodePoolName: testNodePool,
	}
	crud, err := mock.KubeApplier(testMgmt).ReadDesires(parent)
	if err != nil {
		t.Fatalf("ReadDesires(parent): %v", err)
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

	parent := database.ResourceParent{
		SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster,
	}
	crud, err := mock.KubeApplier(testMgmt).ApplyDesires(parent)
	if err != nil {
		t.Fatalf("ApplyDesires(parent): %v", err)
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
		&kubeapplierapi.ApplyDesire{
			CosmosMetadata: resourcesapi.CosmosMetadata{
				ResourceID: mustParse(t,
					kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(
						testSub, testRG, testCluster, testNodePool, "other")),
			},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: testMgmt,
				KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"y"}}`)},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}

	iter, err := mock.GlobalListers().ApplyDesires().List(ctx, nil)
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

	parent := database.ResourceParent{
		SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster,
	}
	crud, err := kubeMock.KubeApplier(testMgmt).ApplyDesires(parent)
	if err != nil {
		t.Fatalf("ApplyDesires(parent): %v", err)
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
