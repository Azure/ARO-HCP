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
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestControlPlaneActiveVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	tests := []struct {
		name          string
		seedDB        func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient)
		readDesires   func(t *testing.T) []*kubeapplierapi.ReadDesire
		expectedError bool
		validateAfter func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name: "cluster not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				// No cluster seeded - Get will return not found.
			},
			expectedError: false,
		},
		{
			name: "no management cluster content returns nil (no error)",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
				// No ReadDesire - GetForCluster returns NotFound, syncer returns nil without writes.
			},
			expectedError: false,
		},
		{
			name: "active versions unchanged when management cluster version matches current",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
				createServiceProviderClusterWithVersion(t, ctx, mockResourcesDBClient, "4.19.15")
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil,
					hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
						{Version: "4.19.15", State: configv1.CompletedUpdate},
					}},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				expectedVersions := []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, expectedVersions, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "all versions from newest until last completed when multiple history entries",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil,
					hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
						{Version: "4.19.17", State: configv1.PartialUpdate}, {Version: "4.19.16", State: configv1.PartialUpdate}, {Version: "4.19.15", State: configv1.CompletedUpdate},
						{Version: "4.19.14", State: configv1.PartialUpdate}, {Version: "4.19.13", State: configv1.CompletedUpdate},
					}},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				expectedSPCVersions := []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.17")), State: configv1.PartialUpdate}, {Version: ptr.To(semver.MustParse("4.19.16")), State: configv1.PartialUpdate}, {Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, expectedSPCVersions, spc.Status.ControlPlaneVersion.ActiveVersions)

				expectedHCPVersions := []coreapi.HCPClusterActiveVersion{
					{Version: ptr.To(semver.Version{Major: 4, Minor: 19})},
				}
				cluster, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, expectedHCPVersions, cluster.Status.ActiveVersions)
			},
		},
		{
			name: "one active version when control plane history has one element",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil,
					hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
						{Version: "4.19.16", State: configv1.PartialUpdate},
					}},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				expectedSPCVersions := []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.16")), State: configv1.PartialUpdate},
				}
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, expectedSPCVersions, spc.Status.ControlPlaneVersion.ActiveVersions)

				expectedHCPVersions := []coreapi.HCPClusterActiveVersion{
					{Version: ptr.To(semver.Version{Major: 4, Minor: 19})},
				}
				cluster, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, expectedHCPVersions, cluster.Status.ActiveVersions)
			},
		},
		{
			name: "no active versions when control plane history is empty",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil, hsv1beta1.ControlPlaneVersionStatus{})}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Empty(t, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "no active versions when control plane history empty and version status nil",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil, hsv1beta1.ControlPlaneVersionStatus{})}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Empty(t, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "history entries with empty or invalid version are skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil,
					hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
						{Version: "", State: configv1.PartialUpdate},
						{Version: "not-a-version", State: configv1.PartialUpdate},
						{Version: "4.19.15", State: configv1.CompletedUpdate},
					}},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "prefers controlPlaneVersion history over version history when both set",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t,
					&hsv1beta1.ClusterVersionStatus{History: []configv1.UpdateHistory{
						{Version: "4.20.1", State: configv1.PartialUpdate},
					}},
					hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
						{Version: "4.20.1", State: configv1.CompletedUpdate},
					}},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.20.1")), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "nightly version in control plane history is parsed and included",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil,
					hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
						{Version: "4.19.0-0.nightly-multi-2026-01-10-204154", State: configv1.CompletedUpdate},
					}},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(metadataapi.Must(semver.ParseTolerant("4.19.0-0.nightly-multi-2026-01-10-204154"))), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
		{
			name: "falls back to version history when control plane history empty",
			seedDB: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockResourcesDBClient)
			},
			readDesires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t,
					&hsv1beta1.ClusterVersionStatus{History: []configv1.UpdateHistory{
						{Version: "4.19.17", State: configv1.PartialUpdate},
						{Version: "4.19.16", State: configv1.PartialUpdate},
						{Version: "4.19.15", State: configv1.CompletedUpdate},
					}},
					hsv1beta1.ControlPlaneVersionStatus{},
				)}
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, []coreapi.ServiceProviderClusterActiveVersion{
					{Version: ptr.To(semver.MustParse("4.19.17")), State: configv1.PartialUpdate},
					{Version: ptr.To(semver.MustParse("4.19.16")), State: configv1.PartialUpdate},
					{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
				}, spc.Status.ControlPlaneVersion.ActiveVersions)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			tt.seedDB(t, runCtx, mockResourcesDBClient)

			var desires []*kubeapplierapi.ReadDesire
			if tt.readDesires != nil {
				desires = tt.readDesires(t)
			}

			syncer := &controlPlaneActiveVersionSyncer{
				resourcesDBClient:            mockResourcesDBClient,
				readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{Desires: desires},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
			}

			err := syncer.SyncOnce(runCtx, testKey)

			assertSyncResult(t, err, tt.expectedError, "")

			if tt.validateAfter != nil && !tt.expectedError {
				tt.validateAfter(t, runCtx, mockResourcesDBClient)
			}
		})
	}
}

// TestControlPlaneActiveVersionSyncer_NoReplaceWhenVersionsUnchanged is a regression test for
// unnecessary writes against ServiceProviderClusters/default. The previous comparison used
// slices.Equal on []HCPClusterActiveVersion, where each element holds a *semver.Version. Two
// independently-parsed semver pointers compare unequal under Go's `==` even when the represented
// versions are identical, so every reconciliation produced a Replace whose only effect was a new
// _etag / _ts / properties.cosmosMetadata.etag.
func TestControlPlaneActiveVersionSyncer_NoReplaceWhenVersionsUnchanged(t *testing.T) {
	runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

	createTestHCPCluster(t, runCtx, mockResourcesDBClient)
	createServiceProviderClusterWithVersion(t, runCtx, mockResourcesDBClient, "4.19.15")
	desires := []*kubeapplierapi.ReadDesire{newHostedClusterReadDesireWithVersions(t, nil,
		hsv1beta1.ControlPlaneVersionStatus{History: []hsv1beta1.ControlPlaneUpdateHistory{
			{Version: "4.19.15", State: configv1.CompletedUpdate},
		}},
	)}

	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
	before, err := spcCRUD.Get(runCtx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	beforeETag := before.CosmosETag

	syncer := &controlPlaneActiveVersionSyncer{
		resourcesDBClient:            mockResourcesDBClient,
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{Desires: desires},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
	}
	require.NoError(t, syncer.SyncOnce(runCtx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}))

	after, err := spcCRUD.Get(runCtx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Equal(t, beforeETag, after.CosmosETag, "ServiceProviderCluster.CosmosETag changed despite identical ActiveVersions; the syncer wrote unnecessarily")
}

// createTestHCPCluster creates an HCP cluster in the mock database (no node pools).
// Used as the parent resource for control plane active version sync.
//
// An empty ServiceProviderCluster is also seeded; this mirrors what the
// CreateServiceProviderCluster controller would have populated by the time
// any consumer syncer runs in production.
func createTestHCPCluster(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()

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
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, clusterResourceID)
	require.NoError(t, err)
}

// newHostedClusterReadDesireWithVersions builds a ReadDesire whose
// Status.KubeContent.Raw carries a marshaled HostedCluster with the given
// status.version and status.controlPlaneVersion. Pass nil version to omit
// status.version; history entries are newest first.
func newHostedClusterReadDesireWithVersions(
	t *testing.T,
	version *hsv1beta1.ClusterVersionStatus,
	controlPlaneVersion hsv1beta1.ControlPlaneVersionStatus,
) *kubeapplierapi.ReadDesire {
	t.Helper()

	hc := &hsv1beta1.HostedCluster{}
	hc.APIVersion = "hypershift.openshift.io/v1beta1"
	hc.Kind = "HostedCluster"
	hc.SetName(testClusterName)
	hc.Status.ControlPlaneVersion = controlPlaneVersion
	hc.Status.Version = version
	raw, err := json.Marshal(hc)
	require.NoError(t, err)
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   hostedClusterReadDesireResourceID(t),
			PartitionKey: strings.ToLower("management-cluster-resource-id"),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}
