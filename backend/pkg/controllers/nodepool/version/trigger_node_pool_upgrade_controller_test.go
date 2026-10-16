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
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestTriggerNodePoolUpgradeSyncer_SyncOnce(t *testing.T) {
	// Both the NodePool and its ServiceProviderNodePool live ONLY in slice-backed
	// cache listers, never in a mock ResourcesDBClient. This controller performs no
	// Cosmos writes, so no DB is needed. Because the objects live only in the cache,
	// reverting SyncOnce to a live c.resourcesDBClient...Get read would resolve
	// NotFound and return early — so these tests genuinely guard the cached reads.
	tests := []struct {
		name        string
		nodePools   []*coreapi.NodePool
		spNodePools []*coreapi.ServiceProviderNodePool
	}{
		{
			name: "node pool absent from cache returns nil",
		},
		{
			name: "node pool with deletion timestamp returns nil",
			nodePools: []*coreapi.NodePool{nodePoolInCache(func(np *coreapi.NodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
			})},
		},
		{
			name: "missing NodePool ClusterServiceID returns nil",
			nodePools: []*coreapi.NodePool{nodePoolInCache(func(np *coreapi.NodePool) {
				np.ServiceProviderProperties.ClusterServiceID = nil
			})},
		},
		{
			name:        "no desired version on ServiceProviderNodePool returns nil",
			nodePools:   []*coreapi.NodePool{nodePoolInCache(nil)},
			spNodePools: []*coreapi.ServiceProviderNodePool{spNodePoolInCache(nil, "4.21.0")},
		},
		{
			name:        "no active versions during installation returns nil",
			nodePools:   []*coreapi.NodePool{nodePoolInCache(nil)},
			spNodePools: []*coreapi.ServiceProviderNodePool{spNodePoolInCache(ptr.To(semver.MustParse("4.21.0")))},
		},
		{
			name:        "desired version matches latest actual version returns nil",
			nodePools:   []*coreapi.NodePool{nodePoolInCache(nil)},
			spNodePools: []*coreapi.ServiceProviderNodePool{spNodePoolInCache(ptr.To(semver.MustParse("4.21.0")), "4.21.0", "4.20.15")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())

			syncer := &triggerNodePoolUpgradeSyncer{
				nodePoolLister:                &corelistertesting.SliceNodePoolLister{NodePools: tt.nodePools},
				serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: tt.spNodePools},
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

// TestTriggerNodePoolUpgradeSyncer_SyncOnce_TriggersUpgradeFromCache drives the
// full SyncOnce path where the node pool is read from the informer-backed
// NodePoolLister (a slice-backed cache lister here, with nothing in any mock DB)
// and its desired version differs from the active one, so an upgrade policy is
// posted to Cluster Service. Because the objects live only in the cache, reverting
// either read to a live Cosmos Get would resolve NotFound and skip the post.
func TestTriggerNodePoolUpgradeSyncer_SyncOnce_TriggersUpgradeFromCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())

	// Node pool + ServiceProviderNodePool live ONLY in the slice-backed cache
	// listers (never a mock DB). Desired (4.21.5) differs from active (4.21.0), so
	// an upgrade policy is posted. Reverting either read to a live Cosmos Get would
	// resolve NotFound and skip the post, failing this test.
	nodePools := []*coreapi.NodePool{nodePoolInCache(nil)}
	spNodePools := []*coreapi.ServiceProviderNodePool{spNodePoolInCache(ptr.To(semver.MustParse("4.21.5")), "4.21.0")}

	nodePoolServiceID := metadataapi.Must(metadataapi.NewInternalID(testCSNodePoolIDStr))
	mockClusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockClusterServiceClient.EXPECT().
		ListNodePoolUpgradePolicies(nodePoolServiceID, "creation_timestamp desc").
		Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{}, nil))
	expectedBuilder := arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.21.5")
	mockClusterServiceClient.EXPECT().
		PostNodePoolUpgradePolicy(gomock.Any(), nodePoolServiceID, expectedBuilder).
		Return(metadataapi.Must(expectedBuilder.Build()), nil)

	syncer := &triggerNodePoolUpgradeSyncer{
		nodePoolLister:                &corelistertesting.SliceNodePoolLister{NodePools: nodePools},
		clusterServiceClient:          mockClusterServiceClient,
		serviceProviderNodePoolLister: &corelistertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: spNodePools},
	}

	err := syncer.SyncOnce(runCtx, controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	})
	assertSyncResult(t, err, false, "")
}

func TestTriggerNodePoolUpgradeSyncer_CreateUpgradePolicyIfNeeded(t *testing.T) {
	testNodePoolServiceID, _ := metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster-id/node_pools/test-nodepool-id")

	tests := []struct {
		name                         string
		desiredVersion               *semver.Version
		nodePoolServiceID            metadataapi.InternalID
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
				latestPolicy := metadataapi.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20").Build())
				olderPolicy := metadataapi.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.15").Build())

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
				latestPolicy := metadataapi.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.18").Build())
				olderPolicy := metadataapi.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.15").Build())

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
					Return(metadataapi.Must(expectedBuilder.Build()), nil)
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
					Return(metadataapi.Must(expectedBuilder.Build()), nil)
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

// nodePoolInCache builds a NodePool (with a cluster-service ID and no SystemData)
// intended to live only in a slice-backed cache lister — never written to a mock
// ResourcesDBClient — so tests can prove SyncOnce reads it from the informer cache
// rather than a live Cosmos read. Pass a mutate func to tweak the result.
func nodePoolInCache(mutate func(*coreapi.NodePool)) *coreapi.NodePool {
	nodePoolResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName))
	np := &coreapi.NodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: nodePoolResourceID},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: nodePoolResourceID, Name: testNodePoolName, Type: coreapi.NodePoolResourceType.String()},
		},
		ServiceProviderProperties: coreapi.NodePoolServiceProviderProperties{
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID(testCSNodePoolIDStr))),
		},
	}
	if mutate != nil {
		mutate(np)
	}
	return np
}

// spNodePoolInCache builds a ServiceProviderNodePool for a slice-backed cache
// lister with the given desired version and zero or more active versions (newest first).
func spNodePoolInCache(desiredVersion *semver.Version, activeVersions ...string) *coreapi.ServiceProviderNodePool {
	spNodePoolResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName +
		"/" + coreapi.ServiceProviderNodePoolResourceTypeName + "/" + coreapi.ServiceProviderNodePoolResourceName))
	var activeVersionEntries []coreapi.ServiceProviderNodePoolActiveVersion
	for _, activeVersion := range activeVersions {
		version := semver.MustParse(activeVersion)
		activeVersionEntries = append(activeVersionEntries, coreapi.ServiceProviderNodePoolActiveVersion{Version: &version})
	}
	return &coreapi.ServiceProviderNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spNodePoolResourceID},
		Spec:           coreapi.ServiceProviderNodePoolSpec{NodePoolVersion: coreapi.ServiceProviderNodePoolSpecVersion{DesiredVersion: desiredVersion}},
		Status:         coreapi.ServiceProviderNodePoolStatus{NodePoolVersion: coreapi.ServiceProviderNodePoolStatusVersion{ActiveVersions: activeVersionEntries}},
	}
}
