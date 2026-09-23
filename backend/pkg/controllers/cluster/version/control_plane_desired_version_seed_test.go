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

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestControlPlaneDesiredVersionSyncer_SyncOnceSeedsChannelTip verifies that, for a cluster with no
// desired version yet and a non-nightly channel, the controller seeds the desired version to the tip
// (offset 0) of the customer's minor channel via the OpenShift update service.
func TestControlPlaneDesiredVersionSyncer_SyncOnceSeedsChannelTip(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.20", "candidate")
	// No desired version yet: this is the initial-seed window.
	createServiceProviderClusterNoActiveVersions(t, ctx, mockDB)

	// Nodes intentionally out of order to prove SelectControlPlaneVersion sorts descending and
	// returns the tip (offset 0) -> 4.20.5.
	graphJSON := `{"nodes":[` +
		`{"version":"4.20.1","payload":"quay.io/openshift-release-dev/ocp-release:4.20.1-multi"},` +
		`{"version":"4.20.5","payload":"quay.io/openshift-release-dev/ocp-release:4.20.5-multi"},` +
		`{"version":"4.20.3","payload":"quay.io/openshift-release-dev/ocp-release:4.20.3-multi"}` +
		`]}`
	var graphCalled bool

	syncer := &controlPlaneVersionSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		roundTripper:                 graphResponseRoundTripper(graphJSON, &graphCalled),
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion, "expected desired version to be seeded from the channel tip")
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.20.5")),
		"expected desired version 4.20.5 (tip of candidate-4.20), got %s", spc.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, graphCalled, "expected the graph API round tripper to be invoked")

	assertIntentFailedFalse(t, ctx, mockDB, controlPlaneDesiredVersionControllerName)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceSeedRejectsNightly verifies that the controller returns
// an error for the nightly channel group, which is not served by the Cincinnati graph API used for
// version selection.
func TestControlPlaneDesiredVersionSyncer_SyncOnceSeedRejectsNightly(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.19", nightlyChannelGroup)
	createServiceProviderClusterNoActiveVersions(t, ctx, mockDB)

	syncer := &controlPlaneVersionSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
	}

	err := syncer.SyncOnce(ctx, clusterKey)
	require.Error(t, err, "expected the nightly channel group to be rejected")
	assert.ErrorContains(t, err, "not supported for control plane version selection")

	spc, getErr := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, getErr)
	assert.Nil(t, spc.Spec.ControlPlaneVersion.DesiredVersion, "expected no desired version to be seeded for nightly")
}
