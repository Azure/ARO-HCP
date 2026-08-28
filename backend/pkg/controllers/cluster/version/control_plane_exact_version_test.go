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
// with no active versions and no desired version, which is the initial-seed
// window handled by controlPlaneVersionSyncer.
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
// with an IntentFailed=False condition (the success path, via clearIntentFailed).
func assertIntentFailedFalse(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, controllerName string) {
	t.Helper()
	ctrlDoc, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(ctx, controllerName)
	require.NoError(t, err)
	cond := apimeta.FindStatusCondition(ctrlDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)
	require.NotNil(t, cond, "expected IntentFailed condition on controller doc")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceSeedsExactVersion verifies that when the cluster pins
// an exact control plane version and no desired version is set yet, the controller seeds it directly
// as the desired version and never consults Cincinnati.
func TestControlPlaneDesiredVersionSyncer_SyncOnceSeedsExactVersion(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.17", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.17.3"))
	// No desired version yet: this is the initial-seed window.
	createServiceProviderClusterNoActiveVersions(t, ctx, mockDB)

	syncer := &controlPlaneVersionSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion, "expected desired version to be seeded from the exact-version pin")
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.17.3")),
		"expected desired version 4.17.3, got %s", spc.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceUsesExactVersion verifies that
// when the cluster pins an exact control plane version, the upgrade controller
// stores it directly as the desired version and skips z/y-stream resolution
// (no Cincinnati calls).
func TestControlPlaneDesiredVersionSyncer_SyncOnceUsesExactVersion(t *testing.T) {
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
	// A desired version is already seeded (upgrade window); the exact pin advances it.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.17.1"), ptr.To(semver.MustParse("4.17.1")))

	ctrl := gomock.NewController(t)

	syncer := &controlPlaneVersionSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		clusterServiceClient:         ocm.NewMockClusterServiceClientSpec(ctrl),
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion, "expected desired version to be set from the exact-version pin")
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.17.3")),
		"expected desired version 4.17.3, got %s", spc.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceExactVersionSkipsNoOpReplace verifies that when the
// exact pin equals the already-stored desired version — via a distinct *semver.Version pointer to the
// same value — the upgrade controller does not issue a ServiceProviderCluster Replace. A no-op write
// is detected by the ServiceProviderCluster's etag being unchanged (Replace injects a new etag).
func TestControlPlaneDesiredVersionSyncer_SyncOnceExactVersionSkipsNoOpReplace(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.21", "stable")
	// The exact pin and the already-stored desired version are the same value but distinct pointers.
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.21.12"))
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.21.10"), ptr.To(semver.MustParse("4.21.12")))

	before, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	etagBefore := before.GetEtag()
	require.NotEmpty(t, etagBefore, "expected the seeded ServiceProviderCluster to have an etag")

	ctrl := gomock.NewController(t)

	syncer := &controlPlaneVersionSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		clusterServiceClient:         ocm.NewMockClusterServiceClientSpec(ctrl),
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	after, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Equal(t, etagBefore, after.GetEtag(), "expected no ServiceProviderCluster Replace when the exact pin already matches the stored desired version")
	require.NotNil(t, after.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, after.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.21.12")),
		"expected desired version to remain 4.21.12, got %s", after.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
}
