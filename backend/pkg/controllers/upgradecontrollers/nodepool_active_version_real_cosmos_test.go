// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package upgradecontrollers

import (
	"context"
	"embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolActiveVersionRealCosmosFS holds JSON documents captured directly
// from CosmosDB on a real cluster (subscription
// e8c5a115-842d-4d7e-98ad-cfb2c50b209e, resource group
// rg-np-version-upgrade-pckjw8-79qhmc, cluster
// np-version-upgrade-cluster-pckjw8, node pool npupgrade-4-20) where the
// ServiceProviderNodePool's status.nodePoolVersion stayed empty even though
// the kube-applier-mirrored Hypershift NodePool reported
// status.nodesInfo.nodeVersions[].ocpVersion = "4.20.24". Embedding the
// exact documents lets this test serve as a regression check against the
// shape of the observed inputs.
//
//go:embed artifacts/TestNodePoolActiveVersionSyncer_RealCosmosFixture/*.json
var nodePoolActiveVersionRealCosmosFS embed.FS

// loadCosmosResource reads an embedded Cosmos JSON document
// (`{"properties": {...resource fields...}}`) and unmarshals the
// `properties` subtree into *T. Cosmos infrastructure fields (_etag, _rid,
// _self, etc.) live outside `properties` and are ignored.
func loadCosmosResource[T any](t *testing.T, fsys embed.FS, path string) *T {
	t.Helper()
	raw, err := fsys.ReadFile(path)
	require.NoError(t, err, "read embedded fixture %s", path)
	var doc struct {
		Properties json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc), "outer parse of %s", path)
	out := new(T)
	require.NoError(t, json.Unmarshal(doc.Properties, out), "properties parse of %s", path)
	return out
}

// seedSubscription inserts a minimal registered subscription. The Cosmos
// capture in /tmp/nodeversionfail did not include the subscription document
// (subscriptions live in a separate container we did not snapshot), so this
// is the only piece of the parent chain we still synthesize.
func seedSubscription(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, subscriptionID string) {
	t.Helper()
	subscriptionRID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + subscriptionID))
	_, err := mockDB.Subscriptions().Create(ctx, &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   subscriptionRID,
			PartitionKey: strings.ToLower(subscriptionRID.SubscriptionID),
		},
		ResourceID: subscriptionRID,
		State:      arm.SubscriptionStateRegistered,
		Properties: &arm.SubscriptionProperties{TenantId: ptr.To("test-tenant-id")},
	}, nil)
	require.NoError(t, err)
}

// TestNodePoolActiveVersionSyncer_RealCosmosFixture is a regression test built
// from Cosmos documents captured on a real cluster. The captured SPNP had an
// empty status.nodePoolVersion despite the kube-applier-mirrored Hypershift
// NodePool reporting ocpVersion=4.20.24 in status.nodesInfo.nodeVersions.
// Replaying those two documents through the syncer must produce
// ActiveVersions=[{Version: 4.20.24}] — anything else means we have
// regressed on the parsing path that handles real-world payloads.
func TestNodePoolActiveVersionSyncer_RealCosmosFixture(t *testing.T) {
	const artifactsRoot = "artifacts/TestNodePoolActiveVersionSyncer_RealCosmosFixture"

	runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := databasetesting.NewMockResourcesDBClient()

	cluster := loadCosmosResource[api.HCPOpenShiftCluster](t, nodePoolActiveVersionRealCosmosFS, artifactsRoot+"/cluster.json")
	nodePool := loadCosmosResource[api.HCPOpenShiftClusterNodePool](t, nodePoolActiveVersionRealCosmosFS, artifactsRoot+"/nodepool.json")
	spnp := loadCosmosResource[api.ServiceProviderNodePool](t, nodePoolActiveVersionRealCosmosFS, artifactsRoot+"/serviceprovidernodepool.json")
	readDesire := loadCosmosResource[kubeapplier.ReadDesire](t, nodePoolActiveVersionRealCosmosFS, artifactsRoot+"/readonlyhypershiftnodepool.json")

	// Sanity-check the inputs match what was captured: empty active versions
	// going in, NodeVersions present on the mirrored Hypershift NodePool.
	require.Empty(t, spnp.Status.NodePoolVersion.ActiveVersions, "captured SPNP must start with no active versions")
	require.NotNil(t, readDesire.Status.KubeContent, "captured ReadDesire must have a kubeContent body")

	require.NotNil(t, spnp.ResourceID)
	nodePoolRID := spnp.ResourceID.Parent
	clusterRID := nodePoolRID.Parent

	// Subscription is the only piece of the parent chain we synthesize —
	// see seedSubscription. Cluster, NodePool, and SPNP are inserted
	// verbatim from the captured Cosmos JSON.
	seedSubscription(t, runCtx, mockDB, clusterRID.SubscriptionID)

	// The captured Cosmos JSON does not carry PartitionKey on the embedded
	// CosmosMetadata; populate it here so the mock CRUD (which mirrors the
	// real one) accepts the documents.
	partitionKey := strings.ToLower(clusterRID.SubscriptionID)
	cluster.PartitionKey = partitionKey
	nodePool.PartitionKey = partitionKey
	spnp.PartitionKey = partitionKey

	_, err := mockDB.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Create(runCtx, cluster, nil)
	require.NoError(t, err, "create cluster from captured cosmos doc")

	_, err = mockDB.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		NodePools(clusterRID.Name).Create(runCtx, nodePool, nil)
	require.NoError(t, err, "create node pool from captured cosmos doc")

	_, err = mockDB.ServiceProviderNodePools(
		clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, nodePoolRID.Name,
	).Create(runCtx, spnp, nil)
	require.NoError(t, err, "create ServiceProviderNodePool from captured cosmos doc")

	syncer := &nodePoolActiveVersionSyncer{
		serviceProviderNodePoolLister: &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
		resourcesDBClient:             mockDB,
		readDesireLister: &internallistertesting.SliceReadDesireLister{
			Desires: []*kubeapplier.ReadDesire{readDesire},
		},
	}

	_, err = syncer.SyncOnce(runCtx, controllerutils.HCPNodePoolKey{
		SubscriptionID:    clusterRID.SubscriptionID,
		ResourceGroupName: clusterRID.ResourceGroupName,
		HCPClusterName:    clusterRID.Name,
		HCPNodePoolName:   nodePoolRID.Name,
	})
	require.NoError(t, err)

	after, err := mockDB.ServiceProviderNodePools(
		clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, nodePoolRID.Name,
	).Get(runCtx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	require.Len(t, after.Status.NodePoolVersion.ActiveVersions, 1,
		"expected the single observed OCPVersion to be promoted into ActiveVersions")
	expected := semver.MustParse("4.20.24")
	assert.True(t, expected.EQ(*after.Status.NodePoolVersion.ActiveVersions[0].Version),
		"expected ActiveVersions[0] to be 4.20.24, got %s", after.Status.NodePoolVersion.ActiveVersions[0].Version)
}
