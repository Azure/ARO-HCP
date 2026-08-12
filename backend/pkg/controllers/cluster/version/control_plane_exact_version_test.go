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

package version

import (
	"context"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// pinExactControlPlaneVersion sets ExperimentalFeatures.ControlPlaneExactVersion
// on the existing test cluster document.
func pinExactControlPlaneVersion(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, exact semver.Version) {
	t.Helper()
	clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
	existing, err := clusterCRUD.Get(ctx, testClusterName)
	require.NoError(t, err)
	updated := existing.DeepCopy()
	updated.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = ptr.To(exact)
	_, err = clusterCRUD.Replace(ctx, updated, nil)
	require.NoError(t, err)
}

// createServiceProviderClusterNoActiveVersions seeds a ServiceProviderCluster
// with no active versions and no desired version, which is the initial-install
// window handled by controlPlaneInitialVersionSyncer.
func createServiceProviderClusterNoActiveVersions(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				coreapi.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
			)),
		},
	}
	spc.SetPartitionKey(testSubscriptionID)
	_, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)
}

// assertIntentFailedFalse verifies the named controller document was written
// with an IntentFailed=False condition (the success path of
// reportDesiredVersionResolution).
func assertIntentFailedFalse(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, controllerName string) {
	t.Helper()
	ctrlDoc, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(ctx, controllerName)
	require.NoError(t, err)
	cond := apimeta.FindStatusCondition(ctrlDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)
	require.NotNil(t, cond, "expected IntentFailed condition on controller doc")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
}

// TestControlPlaneInitialVersionSyncer_SyncOnceUsesExactVersion verifies that
// when the cluster pins an exact control plane version, the initial version
// controller stores it directly as the desired version and never consults
// Cincinnati.
func TestControlPlaneInitialVersionSyncer_SyncOnceUsesExactVersion(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.17", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.17.3"))
	// No active versions yet: this is the initial-install window.
	createServiceProviderClusterNoActiveVersions(t, ctx, mockDB)

	ctrl := gomock.NewController(t)
	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	// Cincinnati must not be consulted on the exact-version path.
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Times(0)

	syncer := &controlPlaneInitialVersionSyncer{
		desiredVersionSyncerCommon: desiredVersionSyncerCommon{
			clock:                        clocktesting.NewFakePassiveClock(now),
			resourcesDBClient:            mockDB,
			serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
			cincinnatiClientCache:        mockClientCache,
		},
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion, "expected desired version to be seeded from the exact-version pin")
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.17.3")),
		"expected desired version 4.17.3, got %s", spc.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneInitialVersionControllerName)
}

// TestControlPlaneUpgradeVersionSyncer_SyncOnceUsesExactVersion verifies that
// when the cluster pins an exact control plane version, the upgrade controller
// stores it directly as the desired version and skips z/y-stream resolution
// (no Cincinnati calls).
func TestControlPlaneUpgradeVersionSyncer_SyncOnceUsesExactVersion(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.17", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.17.3"))
	// Active versions present (upgrade window) and no desired version yet.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.17.1"), nil)

	ctrl := gomock.NewController(t)
	mockClientCache := cincinnati.NewMockClientCache(ctrl)
	// Cincinnati must not be consulted on the exact-version path.
	mockClientCache.EXPECT().GetOrCreateClient(gomock.Any()).Times(0)

	syncer := &controlPlaneUpgradeVersionSyncer{
		desiredVersionSyncerCommon: desiredVersionSyncerCommon{
			clock:                        clocktesting.NewFakePassiveClock(now),
			resourcesDBClient:            mockDB,
			serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
			cincinnatiClientCache:        mockClientCache,
		},
		clusterServiceClient: ocm.NewMockClusterServiceClientSpec(ctrl),
		subscriptionLister: &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID)),
			Properties: &coreapi.SubscriptionProperties{},
		}}},
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion, "expected desired version to be set from the exact-version pin")
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.17.3")),
		"expected desired version 4.17.3, got %s", spc.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
}
