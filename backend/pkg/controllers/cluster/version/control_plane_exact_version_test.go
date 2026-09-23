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
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
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
				coreapihelpers.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
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

// assertIntentFailedTrueWithMessage verifies the named controller document was
// written with an IntentFailed=True condition carrying the exact message. The
// message is asserted verbatim because the operation controller surfaces it to
// the customer as the ARM operation error.
func assertIntentFailedTrueWithMessage(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, controllerName, wantMessage string) {
	t.Helper()
	ctrlDoc, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(ctx, controllerName)
	require.NoError(t, err)
	cond := apimeta.FindStatusCondition(ctrlDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)
	require.NotNil(t, cond, "expected IntentFailed condition on controller doc")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, coreapi.VersionUpgradeNotAcceptedReason, cond.Reason)
	assert.Equal(t, wantMessage, cond.Message)
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

// newExactVersionSyncer builds a syncer wired to mockDB with a fixed clock and a
// Cluster Service mock that must not be called — every exact-version path short
// circuits before Cincinnati resolution.
func newExactVersionSyncer(t *testing.T, mockDB *corecosmosstoragetesting.MockResourcesDBClient) *controlPlaneVersionSyncer {
	t.Helper()
	return &controlPlaneVersionSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)),
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		clusterServiceClient:         ocm.NewMockClusterServiceClientSpec(gomock.NewController(t)),
	}
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceRejectsExactVersionBelowDesired verifies the write-time
// backstop for the admission race: admission reads the stored versions before the write commits, so a
// pin that passed admission can still arrive below a desired version that advanced in between. The
// controller must reject it rather than writing the downgrade through.
func TestControlPlaneDesiredVersionSyncer_SyncOnceRejectsExactVersionBelowDesired(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.21", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.21.5"))
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.21.5"), ptr.To(semver.MustParse("4.21.9")))

	before, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	etagBefore := before.GetEtag()

	require.NoError(t, newExactVersionSyncer(t, mockDB).SyncOnce(ctx, clusterKey))

	after, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Equal(t, etagBefore, after.GetEtag(), "expected no ServiceProviderCluster Replace for a rejected downgrade")
	require.NotNil(t, after.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, after.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.21.9")),
		"expected desired version to remain 4.21.9, got %s", after.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedTrueWithMessage(t, ctx, mockDB, controlPlaneDesiredVersionControllerName,
		`exact control plane version pin 4.21.5 is below the desired control plane version 4.21.9; `+
			`control plane downgrades are not supported. `+
			`Set the "aro-hcp.experimental.cluster.control-plane-exact-version" tag to 4.21.9 or higher`)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceRejectsExactVersionBelowActive verifies the pin is also
// compared against the version the control plane most recently ran, so a first pin on a running
// cluster whose desired version has not been resolved yet cannot downgrade it.
func TestControlPlaneDesiredVersionSyncer_SyncOnceRejectsExactVersionBelowActive(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.21", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.21.5"))
	// No desired version resolved yet; the control plane is already running 4.21.9.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.21.9"), nil)

	require.NoError(t, newExactVersionSyncer(t, mockDB).SyncOnce(ctx, clusterKey))

	after, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, after.Spec.ControlPlaneVersion.DesiredVersion, "expected the downgrading pin not to seed a desired version")

	assertIntentFailedTrueWithMessage(t, ctx, mockDB, controlPlaneDesiredVersionControllerName,
		`exact control plane version pin 4.21.5 is below the active control plane version 4.21.9; `+
			`control plane downgrades are not supported. `+
			`Set the "aro-hcp.experimental.cluster.control-plane-exact-version" tag to 4.21.9 or higher`)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceRejectsExactVersionBelowDesiredAndActive verifies that
// a pin below both stored versions is rejected against both: clearing only the one named would leave
// the pin still below the other, so the customer would have to fix the tag twice.
func TestControlPlaneDesiredVersionSyncer_SyncOnceRejectsExactVersionBelowDesiredAndActive(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.21", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.21.2"))
	// An upgrade to 4.21.9 is in flight from 4.21.7; the pin is below both.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.21.7"), ptr.To(semver.MustParse("4.21.9")))

	require.NoError(t, newExactVersionSyncer(t, mockDB).SyncOnce(ctx, clusterKey))

	after, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, after.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, after.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.21.9")),
		"expected desired version to remain 4.21.9, got %s", after.Spec.ControlPlaneVersion.DesiredVersion)

	assertIntentFailedTrueWithMessage(t, ctx, mockDB, controlPlaneDesiredVersionControllerName,
		`exact control plane version pin 4.21.2 is below the desired control plane version 4.21.9; `+
			`control plane downgrades are not supported. `+
			`Set the "aro-hcp.experimental.cluster.control-plane-exact-version" tag to 4.21.9 or higher`+"\n"+
			`exact control plane version pin 4.21.2 is below the active control plane version 4.21.7; `+
			`control plane downgrades are not supported. `+
			`Set the "aro-hcp.experimental.cluster.control-plane-exact-version" tag to 4.21.7 or higher`)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceAllowsUnchangedExactVersionBelowActive verifies the
// regression the downgrade check must not cause: a cluster whose pin already equals its stored desired
// version keeps reconciling even when the control plane has since advanced past it. Re-litigating an
// applied pin would latch IntentFailed on a cluster that needs no write at all.
func TestControlPlaneDesiredVersionSyncer_SyncOnceAllowsUnchangedExactVersionBelowActive(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.21", "stable")
	pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse("4.21.5"))
	// The pin was already applied; the control plane then advanced out of band to 4.21.9.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.21.9"), ptr.To(semver.MustParse("4.21.5")))

	before, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	etagBefore := before.GetEtag()

	require.NoError(t, newExactVersionSyncer(t, mockDB).SyncOnce(ctx, clusterKey))

	after, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Equal(t, etagBefore, after.GetEtag(), "expected no ServiceProviderCluster Replace for an unchanged pin")

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceExactVersionNightlyOrdering pins the documented
// pre-release behaviour: blang semver orders pre-release identifiers lexically, so nightly builds
// compare correctly within one stream but not across streams.
func TestControlPlaneDesiredVersionSyncer_SyncOnceExactVersionNightlyOrdering(t *testing.T) {
	for _, tt := range []struct {
		name         string
		pin          string
		active       string
		wantRejected bool
	}{
		{
			name:   "newer nightly in the same stream advances",
			pin:    "4.21.0-0.nightly-2026-09-01-000000",
			active: "4.21.0-0.nightly-2026-08-05-123456",
		},
		{
			name:         "older nightly in the same stream is rejected",
			pin:          "4.21.0-0.nightly-2026-08-05-123456",
			active:       "4.21.0-0.nightly-2026-09-01-000000",
			wantRejected: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clusterKey := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

			createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.21", nightlyChannelGroup)
			pinExactControlPlaneVersion(t, ctx, mockDB, semver.MustParse(tt.pin))
			createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse(tt.active), nil)

			require.NoError(t, newExactVersionSyncer(t, mockDB).SyncOnce(ctx, clusterKey))

			after, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			if tt.wantRejected {
				assert.Nil(t, after.Spec.ControlPlaneVersion.DesiredVersion, "expected the older nightly pin not to seed a desired version")
				assertIntentFailedTrueWithMessage(t, ctx, mockDB, controlPlaneDesiredVersionControllerName,
					`exact control plane version pin `+tt.pin+` is below the active control plane version `+tt.active+`; `+
						`control plane downgrades are not supported. `+
						`Set the "aro-hcp.experimental.cluster.control-plane-exact-version" tag to `+tt.active+` or higher`)
				return
			}

			require.NotNil(t, after.Spec.ControlPlaneVersion.DesiredVersion, "expected the newer nightly pin to seed a desired version")
			assert.True(t, after.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse(tt.pin)),
				"expected desired version %s, got %s", tt.pin, after.Spec.ControlPlaneVersion.DesiredVersion)
			assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
		})
	}
}
