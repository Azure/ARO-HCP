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
	"errors"
	"net/http"
	"strings"
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

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// testVersionClusterKey is the key for the cluster seeded by the testCosmos* helpers.
var testVersionClusterKey = controllerutils.HCPClusterKey{
	SubscriptionID:    "6b690bec-0c16-4ecb-8f67-781caf40bba7",
	ResourceGroupName: "test-rg",
	HCPClusterName:    "test-cluster",
}

// TestValidateRequestedMinorVersionChange verifies the minor-version-change validation that gates the
// upgrade controller: it rejects downgrades, skip-minor jumps, unsupported cross-major landings, and
// node-pool minor skew, while allowing supported changes. Version selection is independent and not
// exercised here.
func TestValidateRequestedMinorVersionChange(t *testing.T) {
	tests := []struct {
		name                  string
		activeVersions        []coreapi.HCPClusterActiveVersion
		customerDesiredMinor  string
		cosmosResources       []any
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name:                 "no active versions short-circuits without error",
			activeVersions:       nil,
			customerDesiredMinor: "4.19",
			expectedError:        false,
		},
		{
			name:                 "same minor (z-stream) is allowed",
			activeVersions:       []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "4.19",
			expectedError:        false,
		},
		{
			name:                  "downgrade not allowed (4.20 -> 4.19)",
			activeVersions:        []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.15")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.19",
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                  "major downgrade not allowed (5.1 -> 4.20)",
			activeVersions:        []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("5.1.5")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.20",
			expectedError:         true,
			expectedErrorContains: "only upgrades to the next minor version are allowed, no downgrades",
		},
		{
			name:                  "skip minor version not allowed (4.19 -> 4.21)",
			activeVersions:        []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.22")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "4.21",
			expectedError:         true,
			expectedErrorContains: "only upgrade to the next minor is allowed",
		},
		{
			name:                  "unsupported cross-major landing (4.21 -> 5.0)",
			activeVersions:        []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.21.10")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "5.0",
			expectedError:         true,
			expectedErrorContains: "cross-major upgrade from 4.21 is only allowed to",
		},
		{
			name:                  "node pool minor skew blocks supported cross-major (4.22 -> 5.0, node pool at 4.20)",
			activeVersions:        []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor:  "5.0",
			cosmosResources:       testCosmosClusterWithWorkersNodePoolAtVersion("4.20.0"),
			expectedError:         true,
			expectedErrorContains: "incompatible with node pool",
		},
		{
			name:                 "supported cross-major with compatible node pools (4.22 -> 5.0)",
			activeVersions:       []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "5.0",
			cosmosResources:      testCosmosClusterWithWorkersNodePoolAtVersion("4.22.0"),
			expectedError:        false,
		},
		{
			name:                 "incompatible node pool being deleted is ignored (4.22 -> 5.0)",
			activeVersions:       []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.22.0")), State: configv1.CompletedUpdate}},
			customerDesiredMinor: "5.0",
			cosmosResources:      testCosmosClusterWithActiveAndDeletingNodePools("infra", "4.22.0", "workers", "4.20.0"),
			expectedError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tt.cosmosResources)
			require.NoError(t, err)
			syncer := &controlPlaneVersionSyncer{
				resourcesDBClient:             mockResourcesDBClient,
				nodePoolLister:                &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
			}

			err = syncer.validateRequestedMinorVersionChange(ctx, testVersionClusterKey, tt.customerDesiredMinor, tt.activeVersions)
			if tt.expectedError {
				require.Error(t, err)
				require.NotEmpty(t, tt.expectedErrorContains, "expectedErrorContains must be set when expectedError is true")
				assert.ErrorContains(t, err, tt.expectedErrorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func testCosmosClusterWithWorkersNodePoolAtVersion(nodePoolVersionId string) []any {
	clusterResourceId, cluster := testCosmosClusterResource()
	workersNodePool := testCosmosNodePool(clusterResourceId, "workers", nodePoolVersionId, false)
	return []any{
		cluster,
		workersNodePool,
		testCosmosServiceProviderNodePool(workersNodePool.ResourceID),
	}
}

func testCosmosClusterResource() (*azcorearm.ResourceID, *coreapi.HCPOpenShiftCluster) {
	clusterResourceId := metadataapi.Must(coreapi.ToClusterResourceID("6b690bec-0c16-4ecb-8f67-781caf40bba7", "test-rg", "test-cluster"))
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceId,
			PartitionKey: strings.ToLower(clusterResourceId.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceId,
				Name: clusterResourceId.Name,
				Type: clusterResourceId.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster"))),
		},
	}
	return clusterResourceId, cluster
}

// testCosmosServiceProviderNodePool returns an empty ServiceProviderNodePool nested under
// the given node pool, the way CreateServiceProviderNodePool would have populated it in production.
func testCosmosServiceProviderNodePool(nodePoolResourceId *azcorearm.ResourceID) *coreapi.ServiceProviderNodePool {
	spnpResourceId := metadataapi.Must(azcorearm.ParseResourceID(
		nodePoolResourceId.String() + "/serviceProviderNodePools/" + coreapi.ServiceProviderNodePoolResourceName,
	))
	return &coreapi.ServiceProviderNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spnpResourceId,
			PartitionKey: strings.ToLower(spnpResourceId.SubscriptionID),
		},
	}
}

func testCosmosNodePool(clusterResourceId *azcorearm.ResourceID, name, nodePoolVersionId string, deleting bool) *coreapi.HCPOpenShiftClusterNodePool {
	nodePoolResourceId := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceId.String() + "/nodePools/" + name))
	nodePool := &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   nodePoolResourceId,
			PartitionKey: strings.ToLower(nodePoolResourceId.SubscriptionID),
		},
		TrackedResource: coreapi.NewTrackedResource(nodePoolResourceId, "eastus"),
		Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
			Version: coreapi.NodePoolVersionProfile{ID: nodePoolVersionId},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster/node_pools/" + name))),
		},
	}
	if deleting {
		nodePool.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
	}
	return nodePool
}

// testCosmosClusterWithActiveAndDeletingNodePools returns a cluster with one active node pool and one
// node pool marked for deletion. Skew validation should consider only the active pool.
// The active pool is also seeded with an empty ServiceProviderNodePool so that
// listClusterAdmissionNodePools (which Gets the SPNP for every non-deleting pool)
// returns a complete result.
func testCosmosClusterWithActiveAndDeletingNodePools(activeNodePoolName, activeVersion, deletingNodePoolName, deletingVersion string) []any {
	clusterResourceId, cluster := testCosmosClusterResource()
	activeNodePool := testCosmosNodePool(clusterResourceId, activeNodePoolName, activeVersion, false)
	deletingNodePool := testCosmosNodePool(clusterResourceId, deletingNodePoolName, deletingVersion, true)
	return []any{
		cluster,
		activeNodePool,
		deletingNodePool,
		testCosmosServiceProviderNodePool(activeNodePool.ResourceID),
	}
}

func createTestHCPClusterWithCustomerVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, customerVersionID, channelGroup string) {
	t.Helper()
	createTestSubscription(t, ctx, mockResourcesDBClient)
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := metadataapi.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			Version: coreapi.VersionProfile{
				ID:           customerVersionID,
				ChannelGroup: channelGroup,
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnce verifies the end-to-end persistence orchestration:
// validation errors persist IntentFailed, a graph-resolved version greater than the stored desired is
// persisted (and IntentFailed cleared), and lower/equal resolutions leave the stored desired
// unchanged. Version selection is via selectControlPlaneVersion (the OpenShift update service graph),
// driven here by an injected roundTripper.
func TestControlPlaneDesiredVersionSyncer_SyncOnce(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	// candidate-4.19 graph tips used by the resolution cases (offset 0 for candidate).
	graphTip := func(versions ...string) func(*http.Request) (*http.Response, error) {
		nodes := make([]string, 0, len(versions))
		for _, v := range versions {
			nodes = append(nodes, `{"version":"`+v+`","payload":"quay.io/openshift-release-dev/ocp-release:`+v+`-multi"}`)
		}
		return graphResponseRoundTripper(`{"nodes":[`+strings.Join(nodes, ",")+`]}`, nil)
	}

	tests := []struct {
		name                   string
		channelGroup           string
		customerVersion        string
		controlPlaneVersion    string
		previousDesiredVersion *semver.Version
		roundTripper           func(*http.Request) (*http.Response, error)
		wantDesiredVersion     *semver.Version
		wantIntentFailed       *metav1.Condition
	}{
		{
			name:                   "successful resolution persists a higher desired version and clears IntentFailed",
			channelGroup:           "candidate",
			customerVersion:        "4.19",
			controlPlaneVersion:    "4.19.15",
			previousDesiredVersion: ptr.To(semver.MustParse("4.19.15")),
			roundTripper:           graphTip("4.19.15", "4.19.22", "4.19.18"),
			wantDesiredVersion:     ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   coreapi.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: coreapi.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                   "lower resolved desired does not replace higher stored desired",
			channelGroup:           "candidate",
			customerVersion:        "4.19",
			controlPlaneVersion:    "4.19.15",
			previousDesiredVersion: ptr.To(semver.MustParse("4.19.22")),
			roundTripper:           graphTip("4.19.15", "4.19.18"),
			wantDesiredVersion:     ptr.To(semver.MustParse("4.19.22")),
			wantIntentFailed: &metav1.Condition{
				Type:   coreapi.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionFalse,
				Reason: coreapi.ControllerConditionReasonAsExpected,
			},
		},
		{
			name:                   "validation error persists IntentFailed and leaves desired version unchanged",
			channelGroup:           "candidate",
			customerVersion:        "4.19",
			controlPlaneVersion:    "4.20.15",
			previousDesiredVersion: ptr.To(semver.MustParse("4.20.15")),
			wantDesiredVersion:     ptr.To(semver.MustParse("4.20.15")),
			wantIntentFailed: &metav1.Condition{
				Type:    coreapi.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionTrue,
				Reason:  coreapi.VersionUpgradeNotAcceptedReason,
				Message: "invalid next y-stream upgrade path from 4.20.0 to 4.19.0: only upgrades to the next minor version are allowed, no downgrades",
			},
		},
		{
			name:                   "resolution error persists IntentFailed and leaves desired version unchanged",
			channelGroup:           "candidate",
			customerVersion:        "4.19",
			controlPlaneVersion:    "4.19.15",
			previousDesiredVersion: ptr.To(semver.MustParse("4.19.15")),
			roundTripper:           graphResponseRoundTripper(`{"nodes":[]}`, nil),
			wantDesiredVersion:     ptr.To(semver.MustParse("4.19.15")),
			wantIntentFailed: &metav1.Condition{
				Type:   coreapi.ControllerConditionTypeIntentFailed,
				Status: metav1.ConditionTrue,
				Reason: coreapi.VersionUpgradeNotAcceptedReason,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			createTestHCPClusterWithCustomerVersion(t, ctx, mockResourcesDBClient, tt.customerVersion, tt.channelGroup)
			createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockResourcesDBClient, semver.MustParse(tt.controlPlaneVersion), tt.previousDesiredVersion)

			syncer := &controlPlaneVersionSyncer{
				clock:                         clocktesting.NewFakePassiveClock(now),
				resourcesDBClient:             mockResourcesDBClient,
				clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
				activeOperationLister:         &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
				nodePoolLister:                &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				serviceProviderNodePoolLister: &corelistertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				roundTripper:                  tt.roundTripper,
			}

			require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

			serviceProviderCluster, getErr := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, getErr)
			gotDesired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
			require.NotNil(t, gotDesired)
			assert.True(t, gotDesired.EQ(*tt.wantDesiredVersion), "wanted desired version %s, got %s", tt.wantDesiredVersion.String(), gotDesired.String())

			controllerDoc, getControllerDocErr := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
			require.NoError(t, getControllerDocErr)
			intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)
			require.NotNil(t, intentFailedCondition)
			assert.Equal(t, tt.wantIntentFailed.Status, intentFailedCondition.Status)
			assert.Equal(t, tt.wantIntentFailed.Reason, intentFailedCondition.Reason)
			if tt.wantIntentFailed.Message != "" {
				assert.Equal(t, tt.wantIntentFailed.Message, intentFailedCondition.Message)
			}
		})
	}
}

func createServiceProviderClusterWithActiveAndDesiredVersion(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, activeVersion semver.Version, desiredVersion *semver.Version) {
	t.Helper()

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				coreapi.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName),
			)),
		},
		Spec: coreapi.ServiceProviderClusterSpec{
			ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{
				DesiredVersion: desiredVersion,
			},
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{
				ActiveVersions: []coreapi.HCPClusterActiveVersion{
					{Version: ptr.To(activeVersion), State: configv1.CompletedUpdate},
				},
			},
		},
	}
	serviceProviderCluster.SetPartitionKey(testSubscriptionID)
	_, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, serviceProviderCluster, nil)
	require.NoError(t, err)
}

// TestControlPlaneDesiredVersionSyncer_SyncOnceSkipsWhenGated verifies the
// end-to-end skip behaviour: when shouldDetermineDesiredVersion returns false
// SyncOnce returns nil without touching the SPC DesiredVersion or writing a
// controller doc, so the cluster create can finish without an upgrade
// recomputation racing it.
func TestControlPlaneDesiredVersionSyncer_SyncOnceSkipsWhenGated(t *testing.T) {
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Cluster is 5 minutes old.
	createTestHCPClusterWithCustomerVersion(t, ctx, mockDB, "4.19", "stable")
	clusterCRUD := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName)
	existing, err := clusterCRUD.Get(ctx, testClusterName)
	require.NoError(t, err)
	updated := existing.DeepCopy()
	createdAt := now.Add(-5 * time.Minute)
	updated.SystemData = &coreapi.SystemData{CreatedAt: &createdAt}
	updated.ServiceProviderProperties.ActiveOperationID = "op-create-1"
	_, err = clusterCRUD.Replace(ctx, updated, nil)
	require.NoError(t, err)

	// SPC already has a desired version — gate 1 will not fire.
	createServiceProviderClusterWithActiveAndDesiredVersion(t, ctx, mockDB, semver.MustParse("4.19.15"), ptr.To(semver.MustParse("4.19.22")))

	// Active Create operation pinned to the cluster itself.
	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")

	ctrl := gomock.NewController(t)

	// roundTripper is intentionally left nil: the OpenShift update service must not be consulted on
	// the skip path.
	syncer := &controlPlaneVersionSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		resourcesDBClient:            mockDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		activeOperationLister:        &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockDB},
		clusterServiceClient:         ocm.NewMockClusterServiceClientSpec(ctrl),
	}

	require.NoError(t, syncer.SyncOnce(ctx, clusterKey))

	// DesiredVersion is untouched.
	spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotNil(t, spc.Spec.ControlPlaneVersion.DesiredVersion)
	assert.True(t, spc.Spec.ControlPlaneVersion.DesiredVersion.EQ(semver.MustParse("4.19.22")), "DesiredVersion must not change on the skip path")

	// Controller doc was never written, since we returned before WriteController.
	_, getControllerDocErr := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
		Controllers(testClusterName).Get(ctx, controlPlaneDesiredVersionControllerName)
	assert.True(t, cosmosstorageutils.IsNotFoundError(getControllerDocErr), "controller doc must not be written on the skip path, got err=%v", getControllerDocErr)
}

// boomActiveOperationLister is a test double that returns the configured
// error from ListActiveOperationsForCluster. It exists so the gating helper
// can exercise its error-propagation branch without a misbehaving mock DB.
type boomActiveOperationLister struct {
	corelisters.ActiveOperationLister
	err error
}

func (b *boomActiveOperationLister) Get(_ context.Context, _, _ string) (*coreapi.Operation, error) {
	return nil, b.err
}

func (b *boomActiveOperationLister) ListActiveOperationsForCluster(_ context.Context, _, _, _ string) ([]*coreapi.Operation, error) {
	return nil, b.err
}

// seedClusterCreateOperation seeds an active Create operation rooted at the
// given ExternalID into the mock DB so the DB-backed active operation lister
// can find it.
func seedClusterCreateOperation(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, externalID *azcorearm.ResourceID, opName string) {
	t.Helper()
	opResourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapi.ToOperationResourceIDString(externalID.SubscriptionID, opName)))
	operationID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + externalID.SubscriptionID +
			"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/" + opName,
	))
	op := &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   opResourceID,
			PartitionKey: strings.ToLower(externalID.SubscriptionID),
		},
		Status:      coreapi.ProvisioningStateAccepted,
		Request:     cosmosstorageutils.OperationRequestCreate,
		ExternalID:  externalID,
		OperationID: operationID,
	}
	_, err := mockDB.Operations(externalID.SubscriptionID).Create(ctx, op, nil)
	require.NoError(t, err)
}

func TestControlPlaneDesiredVersionSyncer_ShouldDetermineDesiredVersion(t *testing.T) {
	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	listerBoom := errors.New("active operation lister exploded")

	newCluster := func(createdAt *time.Time, activeOperationID string) *coreapi.HCPOpenShiftCluster {
		c := &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID: clusterResourceID,
			},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{
					ID:   clusterResourceID,
					Name: testClusterName,
					Type: coreapi.ClusterResourceType.String(),
				},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ActiveOperationID: activeOperationID,
			},
		}
		if createdAt != nil {
			c.SystemData = &coreapi.SystemData{CreatedAt: createdAt}
		}
		return c
	}
	newSPC := func(desired *semver.Version) *coreapi.ServiceProviderCluster {
		return &coreapi.ServiceProviderCluster{
			Spec: coreapi.ServiceProviderClusterSpec{
				ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{DesiredVersion: desired},
			},
		}
	}

	tests := []struct {
		name           string
		cluster        *coreapi.HCPOpenShiftCluster
		spc            *coreapi.ServiceProviderCluster
		seedOperation  bool
		opLister       func(mockDB *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister
		wantShouldRun  bool
		wantErrContain string
	}{
		{
			name:          "empty DesiredVersion runs even when create is in flight (gate 1)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(nil),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster older than grace period runs even with active create (gate 2)",
			cluster:       newCluster(ptr.To(now.Add(-3*time.Hour)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster with no SystemData.CreatedAt runs (treated as old enough)",
			cluster:       newCluster(nil, "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster younger than grace period with no active create runs (gate 3)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), ""),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			wantShouldRun: true,
		},
		{
			name:          "young cluster + DesiredVersion set + active create skips",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			name:    "cluster exactly at grace period boundary still skips (boundary is strict >)",
			cluster: newCluster(ptr.To(now.Add(-clusterCreateGracePeriod)), "op-create-1"),
			spc:     newSPC(ptr.To(semver.MustParse("4.19.15"))),
			// active create present so without the boundary-is-strict gate, the
			// cluster's age would have to push us through.
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			// Fail open: if we can't tell whether a Create is in flight we
			// surface the error to the caller but still report shouldRun=true
			// so a flaky lister doesn't pin the controller in skip-forever
			// mode for the rest of the grace window.
			name:          "active operation lister error is propagated and fails open to shouldRun=true",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-broken"),
			spc:           newSPC(ptr.To(semver.MustParse("4.19.15"))),
			seedOperation: false,
			opLister: func(_ *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister {
				return &boomActiveOperationLister{err: listerBoom}
			},
			wantShouldRun:  true,
			wantErrContain: "failed to get operations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tt.seedOperation {
				seedClusterCreateOperation(t, ctx, mockDB, clusterResourceID, "op-create-1")
			}
			var opLister corelisters.ActiveOperationLister
			if tt.opLister != nil {
				opLister = tt.opLister(mockDB)
			} else {
				opLister = &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockDB}
			}
			syncer := &controlPlaneVersionSyncer{
				clock:                 clocktesting.NewFakePassiveClock(now),
				resourcesDBClient:     mockDB,
				activeOperationLister: opLister,
			}

			gotShouldRun, err := syncer.shouldDetermineDesiredVersion(ctx, tt.cluster, tt.spc)
			if tt.wantErrContain != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErrContain)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantShouldRun, gotShouldRun)
		})
	}
}
