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

package hostedcluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/cpoutofband"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	fleetlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var hostedClusterReleaseImagesManagementTestKey = controllerutils.HCPClusterKey{
	SubscriptionID:    testSubscriptionID,
	ResourceGroupName: testResourceGroupName,
	HCPClusterName:    testClusterName,
}

func newHostedClusterReleaseImagesManagementTestCluster(channelGroup, versionID string) *coreapi.Cluster {
	c := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
					testSubscriptionID, testResourceGroupName, testClusterName))),
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		CustomerProperties: coreapi.ClusterCustomerProperties{
			Version: coreapi.VersionProfile{
				ID:           versionID,
				ChannelGroup: channelGroup,
			},
		},
	}
	return c
}

func newHostedClusterReleaseImagesManagementTestSPC(desiredVersion *semver.Version) *coreapi.ServiceProviderCluster {
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/serviceProviderClusters/default",
					testSubscriptionID, testResourceGroupName, testClusterName))),
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
	}
	if desiredVersion != nil {
		spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
	}
	return spc
}

func newHostedClusterReleaseImagesManagementTestRollout(yStreamChannel string, bestExactVersion *semver.Version) *fleetapi.ControlPlaneVersionRollout {
	r := &fleetapi.ControlPlaneVersionRollout{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				fmt.Sprintf("/providers/Microsoft.RedHatOpenShift/controlPlaneVersionRollouts/%s", yStreamChannel))),
			PartitionKey: "fleet",
		},
		Spec: fleetapi.ControlPlaneVersionRolloutSpec{
			BestExactVersion: bestExactVersion,
		},
	}
	return r
}

func hostedClusterReleaseImagesManagementTestHostedClusterReadDesire(clusterID string) *kubeapplierapi.ReadDesire {
	hc := &hsv1beta1.HostedCluster{}
	hc.APIVersion = "hypershift.openshift.io/v1beta1"
	hc.Kind = "HostedCluster"
	hc.SetName(testClusterName)
	hc.SetNamespace("ocm-test")
	hc.Spec.ClusterID = clusterID
	raw, _ := json.Marshal(hc)
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
					testSubscriptionID, testResourceGroupName, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster))),
			PartitionKey: strings.ToLower("management-cluster-resource-id"),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions:  []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue}},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func hostedClusterReleaseImagesManagementCincinnatiExpectation(mockClient *cincinnati.MockClient, releaseImage string) {
	mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", gomock.Any(), gomock.Any()).Return(
		configv1.Release{Version: "ignored", Image: releaseImage}, nil, nil, nil,
	).AnyTimes()
}

func TestHostedClusterReleaseImagesManagementSyncer_SetsReleaseImageInBand(t *testing.T) {
	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(nil)

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	hostedClusterReleaseImagesManagementCincinnatiExpectation(mockClient, "quay.io/openshift-release-dev/ocp-release@sha256:inband1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab")

	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient).AnyTimes()

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse("4.19.15")
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	// Seed Cosmos with Cluster and SPC.
	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, stored.Spec.DesiredHostedCluster, "DesiredHostedCluster must be populated after in-band sync")
	assert.Equal(t, "quay.io/openshift-release-dev/ocp-release@sha256:inband1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
		stored.Spec.DesiredHostedCluster.Spec.Release.Image)
	assert.Nil(t, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease, "in-band must clear controlPlaneRelease")
	assert.Equal(t, stored.Spec.DesiredHostedCluster.Spec.Release.Image,
		stored.Spec.DesiredHostedCluster.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation],
		"in-band must force-upgrade to releaseImage")
}

func TestHostedClusterReleaseImagesManagementSyncer_SetsControlPlaneReleaseOutOfBand(t *testing.T) {
	const outOfBandVersion = "4.19.99"
	const outOfBandImage = "quay.io/openshift-release-dev/ocp-release-priv@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const inBandSubstitute = "4.19.15"
	const inBandImage = "quay.io/openshift-release-dev/ocp-release@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(map[string]string{outOfBandVersion: outOfBandImage})

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "stable-4.19", semver.MustParse(inBandSubstitute)).Return(
		configv1.Release{Version: inBandSubstitute, Image: inBandImage}, nil, nil, nil,
	)

	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse(outOfBandVersion)
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	best := semver.MustParse(inBandSubstitute)
	rollout := newHostedClusterReleaseImagesManagementTestRollout("stable-4.19", &best)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{rollout}},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, stored.Spec.DesiredHostedCluster)
	assert.Equal(t, inBandImage, stored.Spec.DesiredHostedCluster.Spec.Release.Image,
		"out-of-band must keep data-plane releaseImage at channel best")
	require.NotNil(t, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease)
	assert.Equal(t, outOfBandImage, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease.Image,
		"out-of-band controlPlaneRelease must be the pin image")
	assert.Equal(t, outOfBandImage, stored.Spec.DesiredHostedCluster.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation],
		"out-of-band must force-upgrade to controlPlaneRelease image")
}

func TestHostedClusterReleaseImagesManagementSyncer_PreservesUnrelatedAnnotations(t *testing.T) {
	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(nil)

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	const image = "quay.io/openshift-release-dev/ocp-release@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	hostedClusterReleaseImagesManagementCincinnatiExpectation(mockClient, image)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient).AnyTimes()

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse("4.19.15")
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)
	spc.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{}
	spc.Spec.DesiredHostedCluster.SetAnnotations(map[string]string{"example.com/keep": "yes"})

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, "yes", stored.Spec.DesiredHostedCluster.GetAnnotations()["example.com/keep"],
		"unrelated annotations must survive")
}

func TestHostedClusterReleaseImagesManagementSyncer_NoDesiredVersionNoOp(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(nil)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	// No Cincinnati calls expected.

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
		cincinnatiClientCache:        mockCache,
		cincinnatiUpdatesCache:       lru.New(10),
		ocpImagesACRHostname:         "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	assert.Nil(t, stored.Spec.DesiredHostedCluster, "no DesiredVersion means no DesiredHostedCluster write")
}

func TestHostedClusterReleaseImagesManagementSyncer_ClearsOutOfBandOnRecovery(t *testing.T) {
	const outOfBandVersion = "4.19.99"
	const outOfBandImage = "quay.io/openshift-release-dev/ocp-release-priv@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const inBandVersion = "4.19.16"
	const inBandImage = "quay.io/openshift-release-dev/ocp-release@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(map[string]string{outOfBandVersion: outOfBandImage})

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "stable-4.19", semver.MustParse(inBandVersion)).Return(
		configv1.Release{Version: inBandVersion, Image: inBandImage}, nil, nil, nil,
	)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse(inBandVersion)
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)
	// Simulate previous out-of-band state still on SPC.
	spc.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{}
	spc.Spec.DesiredHostedCluster.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release@sha256:old"
	spc.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease = &hsv1beta1.Release{Image: outOfBandImage}
	spc.Spec.DesiredHostedCluster.SetAnnotations(map[string]string{
		hsv1beta1.ForceUpgradeToAnnotation: outOfBandImage,
		"example.com/keep":                 "yes",
	})

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, stored.Spec.DesiredHostedCluster)
	assert.Nil(t, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease, "in-band recovery must clear controlPlaneRelease")
	assert.Equal(t, inBandImage, stored.Spec.DesiredHostedCluster.Spec.Release.Image)
	assert.Equal(t, inBandImage, stored.Spec.DesiredHostedCluster.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation],
		"in-band recovery must force-upgrade to releaseImage")
	assert.Equal(t, "yes", stored.Spec.DesiredHostedCluster.GetAnnotations()["example.com/keep"])
}

func TestHostedClusterReleaseImagesManagementSyncer_NeedsWork(t *testing.T) {
	syncer := &hostedClusterReleaseImagesManagementSyncer{cincinnatiUpdatesCache: lru.New(10)}
	cluster := &coreapi.Cluster{}
	assert.True(t, syncer.needsWork(cluster), "active cluster must need work")

	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	assert.False(t, syncer.needsWork(cluster), "deleting cluster must not need work")
}

func TestHostedClusterReleaseImagesManagementSyncer_EmptyChannelGroupErrors(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse("4.19.15")
	cluster := newHostedClusterReleaseImagesManagementTestCluster("", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
		cincinnatiClientCache:        cincinnati.NewMockClientCache(ctrl),
		cincinnatiUpdatesCache:       lru.New(10),
		ocpImagesACRHostname:         "exampleacrhostname.com",
	}

	err = syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel group")
}

func TestHostedClusterReleaseImagesManagementSyncer_CincinnatiVersionNotFoundErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", gomock.Any(), gomock.Any()).Return(
		configv1.Release{}, nil, nil, &cvocincinnati.Error{Reason: "VersionNotFound", Message: "not found"},
	)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse("4.19.15")
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	err = syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestHostedClusterReleaseImagesManagementSyncer_IdempotentNoReplace(t *testing.T) {
	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(nil)

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	const image = "quay.io/openshift-release-dev/ocp-release@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	hostedClusterReleaseImagesManagementCincinnatiExpectation(mockClient, image)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient).AnyTimes()

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse("4.19.15")
	cluster := newHostedClusterReleaseImagesManagementTestCluster("stable", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))
	first, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))
	second, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)

	assert.Equal(t, first.CosmosETag, second.CosmosETag, "identical DesiredHostedCluster must not trigger a Replace")
}

func TestHostedClusterReleaseImagesManagementSyncer_NightlyRemapsDigestToACR(t *testing.T) {
	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(nil)

	const nightlyDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const nightlyImage = "registry.ci.openshift.org/ocp-multi/4.19-art-latest-multi@sha256:" + nightlyDigest
	const expectedMirroredImage = "exampleacrhostname.com/openshift-release-dev/ocp-release-nightly@sha256:" + nightlyDigest

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	nightlyVersion := semver.MustParse("4.19.0-0.nightly-2026-11-20-123456")
	mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "nightly-4.19", nightlyVersion).Return(
		configv1.Release{Version: nightlyVersion.String(), Image: nightlyImage}, nil, nil, nil,
	)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient).AnyTimes()

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := nightlyVersion
	cluster := newHostedClusterReleaseImagesManagementTestCluster("nightly", "4.19")
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, stored.Spec.DesiredHostedCluster, "nightly DesiredHostedCluster must be populated")
	assert.Equal(t, expectedMirroredImage, stored.Spec.DesiredHostedCluster.Spec.Release.Image,
		"nightly release image must retain digest under configured ACR repository")
	assert.Nil(t, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease, "nightly in-band must clear controlPlaneRelease")
	assert.Equal(t, expectedMirroredImage, stored.Spec.DesiredHostedCluster.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation])
}

func TestHostedClusterReleaseImagesManagementSyncer_NightlyWithOutOfBandPinRetainsDigestUnderACR(t *testing.T) {
	const outOfBandVersion = "4.19.99"
	const outOfBandImage = "quay.io/openshift-release-dev/ocp-release-priv@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const nightlyDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	const nightlyImage = "registry.ci.openshift.org/ocp/4.19@sha256:" + nightlyDigest
	const expectedMirroredImage = "exampleacrhostname.com/openshift-release-dev/ocp-release-nightly@sha256:" + nightlyDigest
	const nightlyExact = "4.19.0-0.nightly-2026-11-20-123456"

	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(map[string]string{outOfBandVersion: outOfBandImage})

	ctrl := gomock.NewController(t)
	mockClient := cincinnati.NewMockClient(ctrl)
	exactVersion := semver.MustParse(nightlyExact)
	mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "nightly-4.19", exactVersion).Return(
		configv1.Release{Version: nightlyExact, Image: nightlyImage}, nil, nil, nil,
	)
	mockCache := cincinnati.NewMockClientCache(ctrl)
	mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	desiredVersion := semver.MustParse(outOfBandVersion)
	cluster := newHostedClusterReleaseImagesManagementTestCluster("nightly", "4.19")
	cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = &exactVersion
	spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

	_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
	_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
	require.NoError(t, err)

	syncer := &hostedClusterReleaseImagesManagementSyncer{
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
		},
		cincinnatiClientCache:  mockCache,
		cincinnatiUpdatesCache: lru.New(10),
		ocpImagesACRHostname:   "exampleacrhostname.com",
	}

	require.NoError(t, syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey))

	stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, stored.Spec.DesiredHostedCluster)
	assert.Equal(t, expectedMirroredImage, stored.Spec.DesiredHostedCluster.Spec.Release.Image,
		"nightly out-of-band must keep data-plane release as mirrored nightly digest")
	require.NotNil(t, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease)
	assert.Equal(t, outOfBandImage, stored.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease.Image,
		"out-of-band nightly must pin controlPlaneRelease to out-of-band image")
	assert.Equal(t, outOfBandImage, stored.Spec.DesiredHostedCluster.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation])
}

func TestHostedClusterReleaseImagesManagementSyncer_NightlyMalformedImageErrorsWithoutReplacing(t *testing.T) {
	t.Cleanup(func() { cpoutofband.ConfigureControlPlaneVersions(nil) })
	cpoutofband.ConfigureControlPlaneVersions(nil)

	tests := []struct {
		name  string
		image string
	}{
		{name: "missing registry.ci prefix", image: "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "missing @ digest separator", image: "registry.ci.openshift.org/ocp-multi/4.19-art-latest-multi"},
		{name: "missing sha256 prefix", image: "registry.ci.openshift.org/ocp-multi/4.19-art-latest-multi@sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "empty sha256 digest", image: "registry.ci.openshift.org/ocp-multi/4.19-art-latest-multi@sha256:"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := cincinnati.NewMockClient(ctrl)
			nightlyVersion := semver.MustParse("4.19.0-0.nightly-2026-11-20-123456")
			mockClient.EXPECT().GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "nightly-4.19", nightlyVersion).Return(
				configv1.Release{Version: nightlyVersion.String(), Image: tc.image}, nil, nil, nil,
			)
			mockCache := cincinnati.NewMockClientCache(ctrl)
			mockCache.EXPECT().GetOrCreateClient(gomock.Any()).Return(mockClient).AnyTimes()

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			desiredVersion := nightlyVersion
			cluster := newHostedClusterReleaseImagesManagementTestCluster("nightly", "4.19")
			spc := newHostedClusterReleaseImagesManagementTestSPC(&desiredVersion)

			_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
			require.NoError(t, err)
			_, err = mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
			require.NoError(t, err)

			syncer := &hostedClusterReleaseImagesManagementSyncer{
				resourcesDBClient:            mockDB,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
				rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{},
				readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
					Desires: []*kubeapplierapi.ReadDesire{hostedClusterReleaseImagesManagementTestHostedClusterReadDesire("11111111-1111-1111-1111-111111111111")},
				},
				cincinnatiClientCache:  mockCache,
				cincinnatiUpdatesCache: lru.New(10),
				ocpImagesACRHostname:   "exampleacrhostname.com",
			}

			err = syncer.SyncOnce(ctx, hostedClusterReleaseImagesManagementTestKey)
			require.Error(t, err, "malformed nightly image must error")

			stored, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, "default")
			require.NoError(t, err)
			assert.Nil(t, stored.Spec.DesiredHostedCluster, "malformed nightly image must not replace stored desired images")
		})
	}
}

// Silence unused import if helpers change signature.
var _ = fleetlistertesting.SliceControlPlaneVersionRolloutLister{}
