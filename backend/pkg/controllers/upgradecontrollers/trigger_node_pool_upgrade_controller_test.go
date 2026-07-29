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

package upgradecontrollers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestTriggerNodePoolUpgradeSyncer_SyncOnce(t *testing.T) {
	tests := []struct {
		name   string
		seedDB func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "node pool not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
			},
		},
		{
			name: "node pool with deletion timestamp returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.21.0")

				nodePool, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				nodePool.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())

				_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Replace(ctx, nodePool, nil)
				require.NoError(t, err)
			},
		},
		{
			name: "missing NodePool ClusterServiceID returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()

				nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
					"/resourceGroups/" + testResourceGroupName +
					"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
					"/nodePools/" + testNodePoolName))
				nodePool := &api.HCPOpenShiftClusterNodePool{
					CosmosMetadata: arm.CosmosMetadata{
						ResourceID:   nodePoolResourceID,
						PartitionKey: strings.ToLower(nodePoolResourceID.SubscriptionID),
					},
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID:   nodePoolResourceID,
							Name: testNodePoolName,
							Type: api.NodePoolResourceType.String(),
						},
						Location: "eastus",
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{},
				}

				_, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Create(ctx, nodePool, nil)
				require.NoError(t, err)
			},
		},
		{
			name: "no desired version on ServiceProviderNodePool returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.21.0")
				createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.21.0")
			},
		},
		{
			name: "no active versions during installation returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.21.0")
				createServiceProviderNodePoolWithActiveAndDesiredVersion(
					t, ctx, mockDB, ptr.To(semver.MustParse("4.21.0")),
				)
			},
		},
		{
			name: "desired version matches latest actual version returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.21.0")
				createServiceProviderNodePoolWithActiveAndDesiredVersion(
					t, ctx, mockDB, ptr.To(semver.MustParse("4.21.0")),
					"4.21.0", "4.20.15",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
			mockDB := databasetesting.NewMockResourcesDBClient()
			tt.seedDB(t, runCtx, mockDB)

			syncer := &triggerNodePoolUpgradeSyncer{
				resourcesDBClient:             mockDB,
				serviceProviderNodePoolLister: &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockDB},
			}

			err := syncer.SyncOnce(runCtx, controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			})
			assertSyncResult(t, err, false, "")
		})
	}
}

func TestTriggerNodePoolUpgradeSyncer_CreateUpgradePolicyIfNeeded(t *testing.T) {
	testNodePoolServiceID, _ := api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster-id/node_pools/test-nodepool-id")

	tests := []struct {
		name                         string
		desiredVersion               *semver.Version
		nodePoolServiceID            api.InternalID
		mockSetup                    func(*ocm.MockClusterServiceClientSpec)
		expectError                  bool
		expectedErrorContains        string
		expectPolicyCreation         bool
		expectedCreatedPolicyVersion string
	}{
		{
			name:              "latest existing policy matches desired version - returns nil",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				latestPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20").Build())
				olderPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.15").Build())

				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{latestPolicy, olderPolicy}, nil))
			},
			expectError:          false,
			expectPolicyCreation: false,
		},
		{
			name:              "latest existing policy differs from desired version - creates upgrade policy",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				latestPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.18").Build())
				olderPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.15").Build())

				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{latestPolicy, olderPolicy}, nil))

				expectedBuilder := arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20")
				mc.EXPECT().
					PostNodePoolUpgradePolicy(
						context.Background(),
						testNodePoolServiceID,
						expectedBuilder,
					).
					Return(api.Must(expectedBuilder.Build()), nil)
			},
			expectError:                  false,
			expectPolicyCreation:         true,
			expectedCreatedPolicyVersion: "4.19.20",
		},
		{
			name:              "no existing policies - creates upgrade policy",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{}, nil))

				expectedBuilder := arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20")
				mc.EXPECT().
					PostNodePoolUpgradePolicy(
						context.Background(),
						testNodePoolServiceID,
						expectedBuilder,
					).
					Return(api.Must(expectedBuilder.Build()), nil)
			},
			expectError:                  false,
			expectPolicyCreation:         true,
			expectedCreatedPolicyVersion: "4.19.20",
		},
		{
			name:              "list node pool upgrade policies fails - returns error",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator(nil, fmt.Errorf("cluster service unavailable")))
			},
			expectError:           true,
			expectedErrorContains: "failed to list node pool upgrade policies",
			expectPolicyCreation:  false,
		},
		{
			name:              "post node pool upgrade policy fails - returns error",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{}, nil))

				// Policy creation fails
				mc.EXPECT().
					PostNodePoolUpgradePolicy(
						context.Background(),
						testNodePoolServiceID,
						arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20"),
					).
					Return(nil, fmt.Errorf("cluster service API error"))
			},
			expectError:           true,
			expectedErrorContains: "failed to create node pool upgrade policy",
			expectPolicyCreation:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.mockSetup(mockClusterServiceClient)

			syncer := &triggerNodePoolUpgradeSyncer{
				clusterServiceClient: mockClusterServiceClient,
			}

			ctx := context.Background()
			err := syncer.createUpgradePolicyIfNeeded(ctx, tt.desiredVersion, tt.nodePoolServiceID)

			if tt.expectError {
				assert.Error(t, err)
				assert.NotEmpty(t, tt.expectedErrorContains, "expectedErrorContains should be set when expectError is true")
				assert.ErrorContains(t, err, tt.expectedErrorContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// createServiceProviderNodePoolWithActiveAndDesiredVersion seeds a
// ServiceProviderNodePool with the given desired version and zero or more
// active versions (newest first).
func createServiceProviderNodePoolWithActiveAndDesiredVersion(
	t *testing.T,
	ctx context.Context,
	mockResourcesDBClient *databasetesting.MockResourcesDBClient,
	desiredVersion *semver.Version,
	activeVersions ...string,
) {
	t.Helper()

	nodePoolResourceID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName
	spNodePoolResourceID := nodePoolResourceID + "/" + api.ServiceProviderNodePoolResourceTypeName + "/" + api.ServiceProviderNodePoolResourceName

	var activeVersionEntries []api.HCPNodePoolActiveVersion
	for _, activeVersion := range activeVersions {
		version := semver.MustParse(activeVersion)
		activeVersionEntries = append(activeVersionEntries, api.HCPNodePoolActiveVersion{Version: &version})
	}

	spNodePool := &api.ServiceProviderNodePool{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   api.Must(azcorearm.ParseResourceID(spNodePoolResourceID)),
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: api.ServiceProviderNodePoolSpec{
			NodePoolVersion: api.ServiceProviderNodePoolSpecVersion{
				DesiredVersion: desiredVersion,
			},
		},
		Status: api.ServiceProviderNodePoolStatus{
			NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
				ActiveVersions: activeVersionEntries,
			},
		},
	}
	_, err := mockResourcesDBClient.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).Create(ctx, spNodePool, nil)
	require.NoError(t, err)
}
