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

package nodepoolcreationcontrollers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testNodePoolName        = "test-nodepool"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testNodePoolCSIDStr     = testClusterServiceIDStr + "/node_pools/" + testNodePoolName
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

func TestNodePoolClusterServiceCreateSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	clusterCSInternalID := api.Must(api.NewInternalID(testClusterServiceIDStr))
	nodePoolCSInternalID := api.Must(api.NewInternalID(testNodePoolCSIDStr))

	verifyClusterServiceIDIsNil := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		require.NoError(t, err)
		assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceID)
	}

	verifyClusterServiceIDIsSet := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		require.NoError(t, err)
		require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceID, "expected ClusterServiceID to be set")
		assert.Equal(t, testNodePoolCSIDStr, stored.ServiceProviderProperties.ClusterServiceID.String())
	}

	testCases := []struct {
		name              string
		listerCluster     *api.HCPOpenShiftCluster
		existingNodePool  *api.HCPOpenShiftClusterNodePool
		listerNodePool    *api.HCPOpenShiftClusterNodePool // Optional. If not provided, existingNodePool is used as the listerNodePool
		setupMockCSClient func(mock *ocm.MockClusterServiceClientSpec)
		wantErr           bool
		wantErrContain    string
		verifyDB          func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:          "when ClusterServiceID is already set no-op is performed",
			listerCluster: newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.ClusterServiceID = api.Ptr(api.Must(api.NewInternalID(testNodePoolCSIDStr)))
			}),
			verifyDB: verifyClusterServiceIDIsSet,
		},
		{
			name:          "when DeletionTimestamp is set no-op is performed",
			listerCluster: newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			}),
			verifyDB: verifyClusterServiceIDIsNil,
		},
		{
			name: "node pool not found in lister no-op is performed",
		},
		{
			name:          "when lister is stale but DB already has ClusterServiceID no-op is performed",
			listerCluster: newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.ClusterServiceID = api.Ptr(api.Must(api.NewInternalID(testNodePoolCSIDStr)))
			}),
			listerNodePool: newTestNodePoolForCreate(t, nil),
			verifyDB:       verifyClusterServiceIDIsSet,
		},
		{
			name:             "when cluster has no ClusterServiceID error is returned",
			listerCluster:    newTestCluster(t, func(c *api.HCPOpenShiftCluster) { c.ServiceProviderProperties.ClusterServiceID = nil }),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			wantErr:          true,
			wantErrContain:   "cluster test-cluster has no ClusterServiceID",
		},
		{
			name:             "when CS node pool does not exist POST is issued and ClusterServiceID is set",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(nil, fakeOCMNotFoundError())
				csNodePool, err := arohcpv1alpha1.NewNodePool().
					ID(testNodePoolName).
					HREF(testNodePoolCSIDStr).
					Build()
				require.NoError(t, err)
				mock.EXPECT().
					PostNodePool(gomock.Any(), clusterCSInternalID, gomock.Any()).
					Return(csNodePool, nil)
			},
			verifyDB: verifyClusterServiceIDIsSet,
		},
		{
			name:             "when CS node pool already exists POST is skipped and ClusterServiceID is set",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				existing, err := arohcpv1alpha1.NewNodePool().
					ID(testNodePoolName).
					HREF(testNodePoolCSIDStr).
					Build()
				require.NoError(t, err)
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(existing, nil)
			},
			verifyDB: verifyClusterServiceIDIsSet,
		},
		{
			name:             "when CS node pool lookup returns a non-404 error it is propagated",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "boom",
		},
		{
			name:             "when CS node pool POST fails error is propagated",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(nil, fakeOCMNotFoundError())
				mock.EXPECT().
					PostNodePool(gomock.Any(), clusterCSInternalID, gomock.Any()).
					Return(nil, errors.New("post failed"))
			},
			wantErr:        true,
			wantErrContain: "post failed",
		},
		{
			name:             "when PostNodePool returns 4XX OCM error reconciler returns error",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(nil, fakeOCMNotFoundError())
				mock.EXPECT().
					PostNodePool(gomock.Any(), clusterCSInternalID, gomock.Any()).
					Return(nil, fakeOCMError(http.StatusBadRequest, "Machine type not supported"))
			},
			wantErr:        true,
			wantErrContain: "Machine type not supported",
			verifyDB:       verifyClusterServiceIDIsNil,
		},
		{
			name:             "when PostNodePool returns 4XX OCM error reconciler returns error",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(nil, fakeOCMNotFoundError())
				mock.EXPECT().
					PostNodePool(gomock.Any(), clusterCSInternalID, gomock.Any()).
					Return(nil, fakeOCMError(http.StatusForbidden, "forbidden"))
			},
			wantErr:        true,
			wantErrContain: "forbidden",
			verifyDB:       verifyClusterServiceIDIsNil,
		},
		{
			name:             "when PostNodePool returns 500 OCM error it is propagated for retry",
			listerCluster:    newTestCluster(t, nil),
			existingNodePool: newTestNodePoolForCreate(t, nil),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), nodePoolCSInternalID).
					Return(nil, fakeOCMNotFoundError())
				mock.EXPECT().
					PostNodePool(gomock.Any(), clusterCSInternalID, gomock.Any()).
					Return(nil, fakeOCMInternalServerError("internal error"))
			},
			wantErr:        true,
			wantErrContain: "internal error",
			verifyDB:       verifyClusterServiceIDIsNil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			clustersForLister := []*api.HCPOpenShiftCluster{}
			if tc.listerCluster != nil {
				clustersForLister = append(clustersForLister, tc.listerCluster)
			}

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			listerNodePool := tc.listerNodePool
			if listerNodePool == nil {
				listerNodePool = tc.existingNodePool
			}
			if listerNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, listerNodePool)
			}

			syncer := &nodePoolClusterServiceCreateSyncer{
				cooldownChecker:       &alwaysSyncCooldownChecker{},
				nodePoolLister:        &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				clusterLister:         &listertesting.SliceClusterLister{Clusters: clustersForLister},
				controllerLister:      &listertesting.DBControllerLister{ResourcesDBClient: mockResourcesDBClient},
				resourcesDBClient:     mockResourcesDBClient,
				clustersServiceClient: mockCSClient,
			}

			err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				if len(tc.wantErrContain) > 0 {
					require.ErrorContains(t, err, tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

func fakeOCMNotFoundError() error {
	e, _ := ocmerrors.NewError().Status(http.StatusNotFound).Reason("not found").Build()
	return e
}

func fakeOCMError(status int, msg string) error {
	e, _ := ocmerrors.NewError().Status(status).Reason(msg).Build()
	return e
}

func fakeOCMInternalServerError(msg string) error {
	e, _ := ocmerrors.NewError().Status(http.StatusInternalServerError).Reason(msg).Build()
	return e
}

func newTestCluster(t *testing.T, opts func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID := api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr)))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: clusterInternalID,
		},
	}
	if opts != nil {
		opts(cluster)
	}
	return cluster
}

func newTestNodePoolForCreate(t *testing.T, opts func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	np := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID:           "4.20.8",
				ChannelGroup: "stable",
			},
			Replicas: 2,
			Platform: api.NodePoolPlatformProfile{
				VMSize: "Standard_D4s_v3",
				OSDisk: api.OSDiskProfile{
					SizeGiB:                ptr.To(int32(128)),
					DiskType:               api.OsDiskTypeManaged,
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: nil,
		},
	}
	if opts != nil {
		opts(np)
	}
	return np
}
