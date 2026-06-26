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

package mismatch

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID  = "a433a095-1277-44f1-8453-8d61a4d848c2"
	testResourceGroup   = "rg"
	testLiveCluster     = "live-cluster"
	testMissingCluster  = "missing-cluster"
	testLiveNodePool    = "live-np"
	testMissingNodePool = "missing-np"
	testMgmtClusterA    = "mc-a"
	testMgmtClusterB    = "mc-b"
)

// TestSynchronizeSubscription_OrphanedDesires is table-driven over scenarios that
// vary the mix of resource-container docs (clusters/nodepools), per-MC desires,
// and expected post-sweep state. Helper builders construct each fixture so each
// scenario is short to read.
//
// The sweep covers the new per-management-cluster container model: each MC has
// its own MockKubeApplierDBClient registered in a MockKubeApplierDBClients
// (plural). The orphan controller iterates the management-cluster lister, opens
// an UntypedCRUD against each MC's container, and deletes any *Desire whose
// parent cluster/nodepool is gone — using DeleteByCosmosID with the partitionKey
// from the listed row, never relying on the resourceID encoding a partition key.
func TestSynchronizeSubscription_OrphanedDesires(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mgmtA := mustParseResourceID(t, "/providers/microsoft.redhatopenshift/stamps/test/managementclusters/"+testMgmtClusterA)
	mgmtB := mustParseResourceID(t, "/providers/microsoft.redhatopenshift/stamps/test/managementclusters/"+testMgmtClusterB)

	clusterScopedDesire := func(t *testing.T, mc *azcorearm.ResourceID, clusterName, desireName string) *kubeapplier.ApplyDesire {
		return newApplyDesire(t, mc, kubeapplier.ToClusterScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroup, clusterName, desireName))
	}
	nodePoolScopedDesire := func(t *testing.T, mc *azcorearm.ResourceID, clusterName, nodePoolName, desireName string) *kubeapplier.ApplyDesire {
		return newApplyDesire(t, mc, kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroup, clusterName, nodePoolName, desireName))
	}

	type rawDoc struct {
		cosmosID string
		json     []byte
	}
	type mcContents struct {
		mc      *azcorearm.ResourceID
		desires []*kubeapplier.ApplyDesire
		raw     []rawDoc // direct StoreDocument inserts (for invalid fixtures)
	}
	type assertion struct {
		cosmosID  string
		mc        *azcorearm.ResourceID
		present   bool // true ⇒ document must remain; false ⇒ must be deleted
		assertMsg string
	}

	for _, tt := range []struct {
		name           string
		buildResources func(t *testing.T) []any
		buildMCs       func(t *testing.T) []mcContents
		expected       func(t *testing.T, mcs []mcContents) []assertion
	}{
		{
			name: "mixed orphans across two management clusters",
			buildResources: func(t *testing.T) []any {
				return []any{
					subscription(t),
					cluster(t, testLiveCluster),
					nodePool(t, testLiveCluster, testLiveNodePool),
				}
			},
			buildMCs: func(t *testing.T) []mcContents {
				return []mcContents{
					{
						mc: mgmtA,
						desires: []*kubeapplier.ApplyDesire{
							clusterScopedDesire(t, mgmtA, testLiveCluster, "live-cluster-desire"),
							nodePoolScopedDesire(t, mgmtA, testLiveCluster, testLiveNodePool, "live-np-desire"),
							nodePoolScopedDesire(t, mgmtA, testLiveCluster, testMissingNodePool, "missing-np-desire"),
						},
					},
					{
						mc: mgmtB,
						desires: []*kubeapplier.ApplyDesire{
							clusterScopedDesire(t, mgmtB, testMissingCluster, "missing-cluster-desire"),
						},
					},
				}
			},
			expected: func(t *testing.T, mcs []mcContents) []assertion {
				return []assertion{
					{mc: mgmtA, cosmosID: cosmosIDForDesire(t, mcs[0].desires[0]), present: true, assertMsg: "desire under live cluster must survive"},
					{mc: mgmtA, cosmosID: cosmosIDForDesire(t, mcs[0].desires[1]), present: true, assertMsg: "desire under live nodepool must survive"},
					{mc: mgmtA, cosmosID: cosmosIDForDesire(t, mcs[0].desires[2]), present: false, assertMsg: "desire under missing nodepool (mc-a) must be deleted"},
					{mc: mgmtB, cosmosID: cosmosIDForDesire(t, mcs[1].desires[0]), present: false, assertMsg: "desire under missing cluster (mc-b) must be deleted"},
				}
			},
		},
		{
			name: "no orphans when every parent is live",
			buildResources: func(t *testing.T) []any {
				return []any{
					subscription(t),
					cluster(t, testLiveCluster),
					nodePool(t, testLiveCluster, testLiveNodePool),
				}
			},
			buildMCs: func(t *testing.T) []mcContents {
				return []mcContents{{
					mc: mgmtA,
					desires: []*kubeapplier.ApplyDesire{
						clusterScopedDesire(t, mgmtA, testLiveCluster, "live-cluster-desire"),
						nodePoolScopedDesire(t, mgmtA, testLiveCluster, testLiveNodePool, "live-np-desire"),
					},
				}}
			},
			expected: func(t *testing.T, mcs []mcContents) []assertion {
				return []assertion{
					{mc: mgmtA, cosmosID: cosmosIDForDesire(t, mcs[0].desires[0]), present: true, assertMsg: "cluster-scoped desire under live cluster must survive"},
					{mc: mgmtA, cosmosID: cosmosIDForDesire(t, mcs[0].desires[1]), present: true, assertMsg: "nodepool-scoped desire under live nodepool must survive"},
				}
			},
		},
		{
			name: "empty management cluster sweep is a no-op",
			buildResources: func(t *testing.T) []any {
				return []any{subscription(t)}
			},
			buildMCs: func(t *testing.T) []mcContents {
				return []mcContents{{mc: mgmtA}}
			},
			expected: func(t *testing.T, mcs []mcContents) []assertion { return nil },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resourcesClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.buildResources(t))
			require.NoError(t, err)

			mcs := tt.buildMCs(t)
			kubeApplierClients := databasetesting.NewMockKubeApplierDBClients()
			mockByMC := map[string]*databasetesting.MockKubeApplierDBClient{}
			var mcFleet []*fleet.ManagementCluster
			for _, m := range mcs {
				docs := make([]any, 0, len(m.desires))
				for _, d := range m.desires {
					docs = append(docs, d)
				}
				mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, docs)
				require.NoError(t, err)
				for _, r := range m.raw {
					mock.StoreDocument(r.cosmosID, r.json)
				}
				kubeApplierClients.Register(m.mc, mock)
				mockByMC[strings.ToLower(m.mc.String())] = mock
				mcFleet = append(mcFleet, &fleet.ManagementCluster{
					CosmosMetadata: api.CosmosMetadata{ResourceID: m.mc, PartitionKey: strings.ToLower(m.mc.SubscriptionID)},
					ResourceID:     m.mc,
				})
			}

			c := &deleteOrphanedCosmosResources{
				name:                    "DeleteOrphanedCosmosResources",
				resourcesDBClient:       resourcesClient,
				kubeApplierDBClients:    kubeApplierClients,
				managementClusterLister: &listertesting.SliceManagementClusterLister{ManagementClusters: mcFleet},
			}

			require.NoError(t, c.synchronizeSubscription(ctx, testSubscriptionID))

			for _, e := range tt.expected(t, mcs) {
				mock := mockByMC[strings.ToLower(e.mc.String())]
				require.NotNil(t, mock, "expected mock for management cluster %s", e.mc)
				_, found := mock.GetDocument(e.cosmosID)
				assert.Equal(t, e.present, found, "%s (cosmosID %s)", e.assertMsg, e.cosmosID)
			}
		})
	}
}

func cosmosIDForDesire(t *testing.T, d *kubeapplier.ApplyDesire) string {
	t.Helper()
	return api.Must(arm.ResourceIDToCosmosID(d.ResourceID))
}

func mustParseResourceID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	return api.Must(azcorearm.ParseResourceID(s))
}

func subscription(t *testing.T) *arm.Subscription {
	t.Helper()
	rid := api.Must(arm.ToSubscriptionResourceID(testSubscriptionID))
	return &arm.Subscription{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(rid.SubscriptionID)},
		ResourceID:     rid,
		State:          arm.SubscriptionStateRegistered,
	}
}

func cluster(t *testing.T, name string) *api.HCPOpenShiftCluster {
	t.Helper()
	rid := api.Must(api.ToClusterResourceID(testSubscriptionID, testResourceGroup, name))
	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(rid.SubscriptionID)},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   rid,
				Name: name,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func nodePool(t *testing.T, clusterName, nodePoolName string) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	rid := api.Must(api.ToNodePoolResourceID(testSubscriptionID, testResourceGroup, clusterName, nodePoolName))
	return &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(rid.SubscriptionID)},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   rid,
				Name: nodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
}

func newApplyDesire(t *testing.T, managementCluster *azcorearm.ResourceID, resourceIDString string) *kubeapplier.ApplyDesire {
	t.Helper()
	rid := api.Must(azcorearm.ParseResourceID(resourceIDString))
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(rid.SubscriptionID)},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x","namespace":"default"}}`)},
		},
	}
}
