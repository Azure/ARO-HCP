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

package version

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolReadDesireResourceID returns the resource ID for the readonly
// NodePool ReadDesire associated with the test node pool. The slice lister
// matches on this ID to satisfy GetForNodePool.
func nodePoolReadDesireResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
			kubeapplierhelpers.ReadDesireNameReadonlyNodePool)))
}

// newNodePoolReadDesireWithNodeVersions builds a ReadDesire whose
// Status.KubeContent.Raw carries a marshaled Hypershift NodePool whose
// Status.NodesInfo.NodeVersions lists the given OCP versions (one entry per
// argument). Pass no arguments to simulate "kube-applier observed the resource
// but no node versions have been reported yet."
func newNodePoolReadDesireWithNodeVersions(t *testing.T, ocpVersions ...string) *kubeapplierapi.ReadDesire {
	t.Helper()
	var nvs []v1beta1.NodeVersion
	for _, v := range ocpVersions {
		nvs = append(nvs, v1beta1.NodeVersion{OCPVersion: v})
	}
	np := &v1beta1.NodePool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodePool",
			APIVersion: v1beta1.GroupVersion.String(),
		},
		Status: v1beta1.NodePoolStatus{
			NodesInfo: v1beta1.NodePoolNodesInfo{
				NodeVersions: nvs,
			},
		},
	}
	raw, err := json.Marshal(np)
	require.NoError(t, err)
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: nodePoolReadDesireResourceID(t)},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

// newNodePoolReadDesireMissingKubeContent simulates the ReadDesire existing but
// the kube-applier not having observed the target yet (Status.KubeContent nil).
func newNodePoolReadDesireMissingKubeContent(t *testing.T) *kubeapplierapi.ReadDesire {
	t.Helper()
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: nodePoolReadDesireResourceID(t)},
		Status:         kubeapplierapi.ReadDesireStatus{},
	}
}

func TestNodePoolActiveVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	tests := []struct {
		name                  string
		seedDB                func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		desires               func(t *testing.T) []*kubeapplierapi.ReadDesire
		expectedError         bool
		expectedErrorContains string
		// validateAfter inspects the SPNP after sync. nil means "no SPNP write expected".
		validateAfter func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name: "ServiceProviderNodePool not in cache returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				// Seed only the NodePool — the cached SPNP lookup will miss
				// and NeedsWork should short-circuit before we touch the
				// ReadDesire.
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.7")}
			},
		},
		{
			name: "ReadDesire absent leaves existing SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
		{
			name: "ReadDesire without kubeContent leaves existing SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireMissingKubeContent(t)}
			},
		},
		{
			name: "empty NodeVersions leaves existing SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t)}
			},
		},
		{
			name: "NodeVersions entries all empty/unparseable leaves SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "", "not-a-semver")}
			},
		},
		{
			name: "version unchanged: no rewrite, active versions stable",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.7")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
		{
			name: "single new version replaces the previous one",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.15")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.15").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))

				np, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				require.Len(t, np.Status.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.15").EQ(*np.Status.ActiveVersions[0].Version))
			},
		},
		{
			name: "in-progress upgrade: both versions surfaced, newest first",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.7", "4.19.15")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 2)
				assert.True(t, semver.MustParse("4.19.15").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[1].Version))

				np, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				require.Len(t, np.Status.ActiveVersions, 2)
				assert.True(t, semver.MustParse("4.19.15").EQ(*np.Status.ActiveVersions[0].Version))
				assert.True(t, semver.MustParse("4.19.7").EQ(*np.Status.ActiveVersions[1].Version))
			},
		},
		{
			name: "duplicate OCPVersion entries are deduped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				// Two NodeVersion entries with the same OCPVersion (different
				// KubeletVersion in real life) must collapse to one entry.
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.15", "4.19.15")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.15").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
		{
			name: "unparseable entries are skipped, parseable ones still recorded",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.15", "not-a-semver")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.15").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
		{
			name: "ParseTolerant accepts non-strict semver from hypershift",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				// hypershift sometimes reports versions like "4.19" (no patch)
				return []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				expected := metadataapi.Must(semver.ParseTolerant("4.19"))
				assert.True(t, expected.EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			tt.seedDB(t, runCtx, mockDB)

			var desires []*kubeapplierapi.ReadDesire
			if tt.desires != nil {
				desires = tt.desires(t)
			}

			syncer := &nodePoolActiveVersionSyncer{
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
				nodePoolLister:                &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockDB},
				resourcesDBClient:             mockDB,
				readDesireLister:              &kubeapplierlistertesting.SliceReadDesireLister{Desires: desires},
			}

			err := syncer.SyncOnce(runCtx, testKey)
			assertSyncResult(t, err, tt.expectedError, tt.expectedErrorContains)

			if tt.validateAfter != nil && !tt.expectedError {
				tt.validateAfter(t, runCtx, mockDB)
			}
		})
	}
}

// TestNodePoolActiveVersionSyncer_NoReplaceWhenVersionsUnchanged is a
// regression test ensuring we don't churn the SPNP fixture on every reconcile
// when nothing has changed — preserves the existing _etag.
func TestNodePoolActiveVersionSyncer_NoReplaceWhenVersionsUnchanged(t *testing.T) {
	runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestNodePoolWithVersion(t, runCtx, mockDB, "4.19.15")
	createServiceProviderNodePoolWithVersion(t, runCtx, mockDB, "4.19.7")

	spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	before, err := spnpCRUD.Get(runCtx, coreapi.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	beforeETag := before.CosmosETag

	syncer := &nodePoolActiveVersionSyncer{
		serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
		nodePoolLister:                &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockDB},
		resourcesDBClient:             mockDB,
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{newNodePoolReadDesireWithNodeVersions(t, "4.19.7")},
		},
	}

	require.NoError(t, syncer.SyncOnce(runCtx, controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}))

	after, err := spnpCRUD.Get(runCtx, coreapi.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	assert.Equal(t, beforeETag, after.CosmosETag, "no write expected when active versions unchanged")
}

// TestNodePoolActiveVersionSyncer_NeedsWork exercises the predicate directly so
// failure modes there don't depend on the SyncOnce control flow.
func TestNodePoolActiveVersionSyncer_NeedsWork(t *testing.T) {
	syncer := &nodePoolActiveVersionSyncer{}

	assert.False(t, syncer.NeedsWork(nil), "nil SPNP should mean no work")
	assert.True(t, syncer.NeedsWork(&coreapi.ServiceProviderNodePool{}), "any non-nil SPNP is enough — the actual version delta is decided after the ReadDesire fetch")
}
