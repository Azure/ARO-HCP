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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolReadDesireResourceID returns the resource ID for the readonly
// NodePool ReadDesire associated with the test node pool. The slice lister
// matches on this ID to satisfy GetForNodePool.
func nodePoolReadDesireResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
			maestrohelpers.ReadDesireNameReadonlyNodePool)))
}

// newNodePoolReadDesireWithStatusVersion builds a ReadDesire whose
// Status.KubeContent.Raw carries a marshaled Hypershift NodePool with the
// given status.version. Use empty string to omit (simulating "kube-applier
// observed the resource but the controller hasn't filled status yet").
func newNodePoolReadDesireWithStatusVersion(t *testing.T, statusVersion string) *kubeapplier.ReadDesire {
	t.Helper()
	np := &v1beta1.NodePool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodePool",
			APIVersion: v1beta1.GroupVersion.String(),
		},
		Status: v1beta1.NodePoolStatus{
			Version: statusVersion,
		},
	}
	raw, err := json.Marshal(np)
	require.NoError(t, err)
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: nodePoolReadDesireResourceID(t)},
		Status: kubeapplier.ReadDesireStatus{
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

// newNodePoolReadDesireMissingKubeContent simulates the ReadDesire existing but
// the kube-applier not having observed the target yet (Status.KubeContent nil).
func newNodePoolReadDesireMissingKubeContent(t *testing.T) *kubeapplier.ReadDesire {
	t.Helper()
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: nodePoolReadDesireResourceID(t)},
		Status:         kubeapplier.ReadDesireStatus{},
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
		seedDB                func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		desires               func(t *testing.T) []*kubeapplier.ReadDesire
		expectedError         bool
		expectedErrorContains string
		// validateAfter inspects the SPNP after sync. nil means "no SPNP write expected".
		validateAfter func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "ServiceProviderNodePool not in cache returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				// Seed only the NodePool — the cached SPNP lookup will miss
				// and NeedsWork should short-circuit before we touch the
				// ReadDesire.
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "4.19.7")}
			},
		},
		{
			name: "ReadDesire absent leaves existing SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
		{
			name: "ReadDesire without kubeContent leaves existing SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireMissingKubeContent(t)}
			},
		},
		{
			name: "NodePool Status.Version empty leaves existing SPNP untouched",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "")}
			},
		},
		{
			name: "NodePool Status.Version unparseable returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "not-a-semver")}
			},
			expectedError:         true,
			expectedErrorContains: "failed to parse NodePool Status.Version",
		},
		{
			name: "version unchanged: no rewrite, active versions stable",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "4.19.7")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 1)
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
			},
		},
		{
			name: "version changed: new tip prepended, previous kept as second entry",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "4.19.15")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 2)
				assert.True(t, semver.MustParse("4.19.15").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[1].Version))
			},
		},
		{
			name: "ParseTolerant accepts non-strict semver from hypershift",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.7")
			},
			desires: func(t *testing.T) []*kubeapplier.ReadDesire {
				// hypershift sometimes reports versions like "4.19" (no patch)
				return []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "4.19")}
			},
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				spnp, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).
					Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				require.Len(t, spnp.Status.NodePoolVersion.ActiveVersions, 2)
				expected := api.Must(semver.ParseTolerant("4.19"))
				assert.True(t, expected.EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version))
				assert.True(t, semver.MustParse("4.19.7").EQ(*spnp.Status.NodePoolVersion.ActiveVersions[1].Version))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := databasetesting.NewMockResourcesDBClient()
			tt.seedDB(t, runCtx, mockDB)

			var desires []*kubeapplier.ReadDesire
			if tt.desires != nil {
				desires = tt.desires(t)
			}

			syncer := &nodePoolActiveVersionSyncer{
				cooldownChecker:               &alwaysSyncCooldownChecker{},
				serviceProviderNodePoolLister: &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
				resourcesDBClient:             mockDB,
				readDesireLister:              &internallistertesting.SliceReadDesireLister{Desires: desires},
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
	mockDB := databasetesting.NewMockResourcesDBClient()

	createTestNodePoolWithVersion(t, runCtx, mockDB, "4.19.15")
	createServiceProviderNodePoolWithVersion(t, runCtx, mockDB, "4.19.7")

	spnpCRUD := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	before, err := spnpCRUD.Get(runCtx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	beforeETag := before.CosmosETag

	syncer := &nodePoolActiveVersionSyncer{
		cooldownChecker:               &alwaysSyncCooldownChecker{},
		serviceProviderNodePoolLister: &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
		resourcesDBClient:             mockDB,
		readDesireLister: &internallistertesting.SliceReadDesireLister{
			Desires: []*kubeapplier.ReadDesire{newNodePoolReadDesireWithStatusVersion(t, "4.19.7")},
		},
	}

	require.NoError(t, syncer.SyncOnce(runCtx, controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}))

	after, err := spnpCRUD.Get(runCtx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	assert.Equal(t, beforeETag, after.CosmosETag, "no write expected when active versions unchanged")
}

// TestNodePoolActiveVersionSyncer_NeedsWork exercises the predicate directly so
// failure modes there don't depend on the SyncOnce control flow.
func TestNodePoolActiveVersionSyncer_NeedsWork(t *testing.T) {
	syncer := &nodePoolActiveVersionSyncer{}

	assert.False(t, syncer.NeedsWork(nil), "nil SPNP should mean no work")
	assert.True(t, syncer.NeedsWork(&api.ServiceProviderNodePool{}), "any non-nil SPNP is enough — the actual version delta is decided after the ReadDesire fetch")
}
