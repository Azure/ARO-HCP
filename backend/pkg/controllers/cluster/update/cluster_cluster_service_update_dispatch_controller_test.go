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

package update

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
)

func TestClusterUpdateDispatchSyncer_SyncOnce(t *testing.T) {
	csID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))

	defaultExistingCSCluster := metadataapi.Must(arohcpv1alpha1.NewCluster().Build())

	newClusterWithConfigDiff := func() *coreapi.HCPOpenShiftCluster {
		return newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
			c.CustomerProperties.NodeDrainTimeoutMinutes = 60
			c.CustomerProperties.Autoscaling.MaxNodesTotal = 10
		})
	}

	newFakeOCMClusterNotUpdatableError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("Cluster 'abc123' is in state 'installing', can't update").
			Build()
		return e
	}

	newFakeOCMUnrelatedBadRequestError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("some other validation error").
			Build()
		return e
	}

	testCases := []struct {
		name                           string
		existingCluster                *coreapi.HCPOpenShiftCluster
		existingSubscription           *coreapi.Subscription
		existingServiceProviderCluster *coreapi.ServiceProviderCluster
		existingCSCluster              *arohcpv1alpha1.Cluster
		// When not set, the syncer uses a cluster lister backed by the seeded Cosmos resources.
		clusterLister                       corelisters.ClusterLister
		setupMockCSClient                   func(mock *ocm.MockClusterServiceClientSpec)
		minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
		wantErr                             bool
		wantErrContain                      string
	}{
		{
			name: "skip without CS call when no CSID",
			existingCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
			}),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                           "dispatches CS calls when config differs",
			existingCluster:                newClusterWithConfigDiff(),
			existingSubscription:           newTestSubscription(),
			existingServiceProviderCluster: newTestServiceProviderCluster(testClusterName),
			existingCSCluster:              defaultExistingCSCluster,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(defaultExistingCSCluster, nil)
				mock.EXPECT().
					UpdateCluster(gomock.Any(), csID, gomock.Any()).
					Return(nil, nil)
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name: "no-op when config matches",
			existingCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
			}),
			existingSubscription:           newTestSubscription(),
			existingServiceProviderCluster: newTestServiceProviderCluster(testClusterName),
			existingCSCluster: mustBuildCSClusterFromRP(t, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
			})),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(mustBuildCSClusterFromRP(t, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
						c.CustomerProperties.NodeDrainTimeoutMinutes = 30
					})), nil)
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                           "when CS cluster update returns cluster not updatable no error is returned",
			existingCluster:                newClusterWithConfigDiff(),
			existingSubscription:           newTestSubscription(),
			existingServiceProviderCluster: newTestServiceProviderCluster(testClusterName),
			existingCSCluster:              defaultExistingCSCluster,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(defaultExistingCSCluster, nil)
				mock.EXPECT().
					UpdateCluster(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMClusterNotUpdatableError())
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                           "when CS cluster update returns unhandled error error is propagated",
			existingCluster:                newClusterWithConfigDiff(),
			existingSubscription:           newTestSubscription(),
			existingServiceProviderCluster: newTestServiceProviderCluster(testClusterName),
			existingCSCluster:              defaultExistingCSCluster,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(defaultExistingCSCluster, nil)
				mock.EXPECT().
					UpdateCluster(gomock.Any(), csID, gomock.Any()).
					Return(nil, errors.New("boom"))
			},
			wantErr:                             true,
			wantErrContain:                      "failed to update cluster-service Cluster",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                           "when CS cluster update returns unrelated bad request error error is propagated",
			existingCluster:                newClusterWithConfigDiff(),
			existingSubscription:           newTestSubscription(),
			existingServiceProviderCluster: newTestServiceProviderCluster(testClusterName),
			existingCSCluster:              defaultExistingCSCluster,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(defaultExistingCSCluster, nil)
				mock.EXPECT().
					UpdateCluster(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMUnrelatedBadRequestError())
			},
			wantErr:                             true,
			wantErrContain:                      "failed to update cluster-service Cluster",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                           "when CS GetCluster fails error is propagated",
			existingCluster:                newClusterWithConfigDiff(),
			existingSubscription:           newTestSubscription(),
			existingServiceProviderCluster: newTestServiceProviderCluster(testClusterName),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(nil, errors.New("boom"))
			},
			wantErr:                             true,
			wantErrContain:                      "failed to get cluster from Cluster Service",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                                "when subscription properties are nil error is propagated",
			existingCluster:                     newClusterWithConfigDiff(),
			existingSubscription:                newTestSubscription(func(s *coreapi.Subscription) { s.Properties = nil }),
			existingServiceProviderCluster:      newTestServiceProviderCluster(testClusterName),
			wantErr:                             true,
			wantErrContain:                      "subscription properties or subscription tenant ID is nil",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                                "when subscription tenant ID is nil error is propagated",
			existingCluster:                     newClusterWithConfigDiff(),
			existingSubscription:                newTestSubscription(func(s *coreapi.Subscription) { s.Properties.TenantId = nil }),
			existingServiceProviderCluster:      newTestServiceProviderCluster(testClusterName),
			wantErr:                             true,
			wantErrContain:                      "subscription properties or subscription tenant ID is nil",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                                "minimum reconcile cooldown prevents sync",
			existingCluster:                     newTestCluster(),
			minimumReconcileTimeCooldownChecker: &neverSyncCooldownChecker{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			seedResources := []any{}
			if tc.existingCluster != nil {
				seedResources = append(seedResources, tc.existingCluster)
			}
			if tc.existingServiceProviderCluster != nil {
				seedResources = append(seedResources, tc.existingServiceProviderCluster)
			}

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seedResources)
			require.NoError(t, err)

			clusterLister := tc.clusterLister
			if clusterLister == nil {
				clusterLister = &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB}
			}

			var subscriptionLister corelisters.SubscriptionLister
			if tc.existingSubscription != nil {
				subscriptionLister = &corelistertesting.SliceSubscriptionLister{
					Subscriptions: []*coreapi.Subscription{tc.existingSubscription},
				}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			syncer := &clusterClusterServiceUpdateDispatchSyncer{
				minimumReconcileTimeCooldownChecker: tc.minimumReconcileTimeCooldownChecker,
				clusterLister:                       clusterLister,
				subscriptionLister:                  subscriptionLister,
				resourcesDBClient:                   mockResourcesDB,
				clusterServiceClient:                mockCSClient,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			err = syncer.SyncOnce(ctx, key)
			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrContain != "" {
					assert.Contains(t, err.Error(), tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNeedsWork(t *testing.T) {
	csID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
	now := metav1.Now()

	tests := []struct {
		name    string
		cluster *coreapi.HCPOpenShiftCluster
		want    bool
	}{
		{
			name: "proceed when CSID set",
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					ClusterServiceID: &csID,
				},
			},
			want: true,
		},
		{
			name: "skip when deletion timestamp is set",
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &now,
					ClusterServiceID:  &csID,
				},
			},
			want: false,
		},
		{
			name: "skip when no CSID",
			cluster: &coreapi.HCPOpenShiftCluster{
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, needsWork(tt.cluster))
		})
	}
}

func mustBuildCSClusterFromRP(t *testing.T, hcpCluster *coreapi.HCPOpenShiftCluster) *arohcpv1alpha1.Cluster {
	t.Helper()

	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

	clusterBuilder, err := ocm.BuildCSCluster(hcpCluster.ID, "", hcpCluster, nil, oldClusterServiceCluster, &coreapi.ServiceProviderCluster{})
	require.NoError(t, err)

	csCluster, err := clusterBuilder.Build()
	require.NoError(t, err)
	return csCluster
}

func newTestSubscription(opts ...func(*coreapi.Subscription)) *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	subscription := &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: subResourceID},
		Properties: &coreapi.SubscriptionProperties{
			TenantId: metadataapi.Ptr("11111111-1111-1111-1111-111111111111"),
		},
	}
	for _, opt := range opts {
		opt(subscription)
	}
	return subscription
}

func newTestCluster(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	csID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &csID,
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

func newTestServiceProviderCluster(clusterName string) *coreapi.ServiceProviderCluster {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/" +
			coreapi.ServiceProviderClusterResourceTypeName + "/" +
			coreapi.ServiceProviderClusterResourceName,
	))

	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

type neverSyncCooldownChecker struct{}

func (c *neverSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return false
}
