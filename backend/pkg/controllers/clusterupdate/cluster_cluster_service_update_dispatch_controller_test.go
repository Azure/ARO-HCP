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

package clusterupdate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"
)

func TestClusterShouldProceed(t *testing.T) {
	csID := api.Must(api.NewInternalID(testClusterServiceIDStr))
	now := metav1.Now()

	tests := []struct {
		name    string
		cluster *api.HCPOpenShiftCluster
		want    bool
	}{
		{
			name: "proceed when no deletion and old approach",
			cluster: &api.HCPOpenShiftCluster{
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ClusterServiceID: &csID,
				},
			},
			want: true,
		},
		{
			name: "skip when deletion timestamp is set",
			cluster: &api.HCPOpenShiftCluster{
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					DeletionTimestamp: &now,
					ClusterServiceID: &csID,
				},
			},
			want: false,
		},
		{
			name: "skip when new approach and no CSID",
			cluster: &api.HCPOpenShiftCluster{
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					UsesNewClusterUpdateApproach: true,
				},
			},
			want: false,
		},
		{
			name: "proceed when new approach and CSID set",
			cluster: &api.HCPOpenShiftCluster{
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					UsesNewClusterUpdateApproach: true,
					ClusterServiceID:             &csID,
				},
			},
			want: true,
		},
		{
			name: "proceed when old approach and no CSID",
			cluster: &api.HCPOpenShiftCluster{
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					UsesNewClusterUpdateApproach: false,
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clusterShouldProceed(tt.cluster))
		})
	}
}

func TestClusterUpdateDispatchSyncer_SyncOnce(t *testing.T) {
	csID := api.Must(api.NewInternalID(testClusterServiceIDStr))

	testCases := []struct {
		name                     string
		cluster                  *api.HCPOpenShiftCluster
		serviceProviderCluster   *api.ServiceProviderCluster
		existingCSCluster        *arohcpv1alpha1.Cluster
		expectCSGet              bool
		expectCSUpdateAutoscaler bool
		expectCSUpdateCluster    bool
		expectHashStamped        bool
		expectError              bool
	}{
		{
			name: "old approach stamps hash without CS call",
			cluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.UsesNewClusterUpdateApproach = false
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
			}),
			serviceProviderCluster: newTestServiceProviderCluster(""),
			expectCSGet:            false,
			expectHashStamped:      true,
		},
		{
			name: "old approach no-op when hash matches",
			cluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.UsesNewClusterUpdateApproach = false
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
			}),
			serviceProviderCluster:   nil, // will be computed
			expectCSGet:              false,
			expectCSUpdateAutoscaler: false,
			expectCSUpdateCluster:    false,
			expectHashStamped:        false,
		},
		{
			name: "new approach dispatches CS calls when hash differs",
			cluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.UsesNewClusterUpdateApproach = true
				c.CustomerProperties.NodeDrainTimeoutMinutes = 60
				c.CustomerProperties.Autoscaling.MaxNodesTotal = 10
			}),
			serviceProviderCluster: newTestServiceProviderCluster("old-hash"),
			existingCSCluster: func() *arohcpv1alpha1.Cluster {
				c, _ := arohcpv1alpha1.NewCluster().Build()
				return c
			}(),
			expectCSGet:              true,
			expectCSUpdateAutoscaler: true,
			expectCSUpdateCluster:    true,
			expectHashStamped:        true,
		},
		{
			name: "new approach no-op when hash matches",
			cluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.UsesNewClusterUpdateApproach = true
				c.CustomerProperties.NodeDrainTimeoutMinutes = 30
			}),
			serviceProviderCluster:   nil, // will be computed
			expectCSGet:              false,
			expectCSUpdateAutoscaler: false,
			expectCSUpdateCluster:    false,
			expectHashStamped:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			resources := []any{tc.cluster}

			// If serviceProviderCluster is nil for "hash matches" tests, pre-compute the hash
			if tc.serviceProviderCluster == nil {
				hash, err := ocm.ClusterUpdatableConfigHash(tc.cluster)
				require.NoError(t, err)
				tc.serviceProviderCluster = newTestServiceProviderCluster(hash)
			}
			resources = append(resources, tc.serviceProviderCluster)

			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tc.expectCSGet {
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), csID).
					Return(tc.existingCSCluster, nil)
			}
			if tc.expectCSUpdateAutoscaler {
				mockCSClient.EXPECT().
					UpdateClusterAutoscaler(gomock.Any(), csID, gomock.Any()).
					Return(nil, nil)
			}
			if tc.expectCSUpdateCluster {
				mockCSClient.EXPECT().
					UpdateCluster(gomock.Any(), csID, gomock.Any()).
					Return(nil, nil)
			}

			syncer := &clusterClusterServiceUpdateDispatchSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				clusterLister:        newFakeClusterLister(tc.cluster),
				resourcesDBClient:    mockResourcesDB,
				clusterServiceClient: mockCSClient,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			err = syncer.SyncOnce(ctx, key)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.expectHashStamped {
				spc, err := mockResourcesDB.ServiceProviderClusters(
					testSubscriptionID,
					testResourceGroupName,
					testClusterName,
				).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)

				expectedHash, err := ocm.ClusterUpdatableConfigHash(tc.cluster)
				require.NoError(t, err)
				assert.Equal(t, expectedHash, spc.Status.ClusterServiceUpdatableConfigHashForUpdateDispatch)
			}
		})
	}
}

func newTestCluster(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	csID := api.Must(api.NewInternalID(testClusterServiceIDStr))
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &csID,
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

func newTestServiceProviderCluster(hash string) *api.ServiceProviderCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName,
	))

	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		Status: api.ServiceProviderClusterStatus{
			ClusterServiceUpdatableConfigHashForUpdateDispatch: hash,
		},
	}
}

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

type fakeClusterLister struct {
	cluster *api.HCPOpenShiftCluster
}

func newFakeClusterLister(cluster *api.HCPOpenShiftCluster) *fakeClusterLister {
	return &fakeClusterLister{cluster: cluster}
}

func (f *fakeClusterLister) List(_ context.Context) ([]*api.HCPOpenShiftCluster, error) {
	return []*api.HCPOpenShiftCluster{f.cluster}, nil
}

func (f *fakeClusterLister) Get(_ context.Context, _, _, _ string) (*api.HCPOpenShiftCluster, error) {
	return f.cluster, nil
}

func (f *fakeClusterLister) ListForResourceGroup(_ context.Context, _, _ string) ([]*api.HCPOpenShiftCluster, error) {
	return []*api.HCPOpenShiftCluster{f.cluster}, nil
}
