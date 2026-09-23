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
	"fmt"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestTriggerControlPlaneUpgradeSyncer_CreateUpgradePolicyIfNeeded(t *testing.T) {
	testClusterServiceID, _ := metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster-id")

	tests := []struct {
		name                         string
		desiredVersion               *semver.Version
		clusterServiceID             metadataapi.InternalID
		mockSetup                    func(*ocm.MockClusterServiceClientSpec)
		expectError                  bool
		expectedErrorContains        string
		expectPolicyCreation         bool
		expectedCreatedPolicyVersion string
	}{
		{
			name:             "latest existing policy matches desired version - returns nil",
			desiredVersion:   ptr.To(semver.MustParse("4.19.20")),
			clusterServiceID: testClusterServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				latestPolicy := metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.20").Build())
				olderPolicy := metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.15").Build())

				mc.EXPECT().
					ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{latestPolicy, olderPolicy}, nil))
			},
			expectError:          false,
			expectPolicyCreation: false,
		},
		{
			name:             "latest existing policy differs from desired version - creates upgrade policy",
			desiredVersion:   ptr.To(semver.MustParse("4.19.20")),
			clusterServiceID: testClusterServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				latestPolicy := metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.18").Build())
				olderPolicy := metadataapi.Must(arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.15").Build())

				mc.EXPECT().
					ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{latestPolicy, olderPolicy}, nil))

				expectedBuilder := arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.20")
				mc.EXPECT().
					PostControlPlaneUpgradePolicy(
						context.Background(),
						testClusterServiceID,
						expectedBuilder,
					).
					Return(metadataapi.Must(expectedBuilder.Build()), nil)
			},
			expectError:                  false,
			expectPolicyCreation:         true,
			expectedCreatedPolicyVersion: "4.19.20",
		},
		{
			name:             "no existing policies - creates upgrade policy",
			desiredVersion:   ptr.To(semver.MustParse("4.19.20")),
			clusterServiceID: testClusterServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{}, nil))

				expectedBuilder := arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.20")
				mc.EXPECT().
					PostControlPlaneUpgradePolicy(
						context.Background(),
						testClusterServiceID,
						expectedBuilder,
					).
					Return(metadataapi.Must(expectedBuilder.Build()), nil)
			},
			expectError:                  false,
			expectPolicyCreation:         true,
			expectedCreatedPolicyVersion: "4.19.20",
		},
		{
			name:             "list control plane upgrade policies fails - returns error",
			desiredVersion:   ptr.To(semver.MustParse("4.19.20")),
			clusterServiceID: testClusterServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator(nil, fmt.Errorf("cluster service unavailable")))
			},
			expectError:           true,
			expectedErrorContains: "failed to list control plane upgrade policies",
			expectPolicyCreation:  false,
		},
		{
			name:             "post control plane upgrade policy fails - returns error",
			desiredVersion:   ptr.To(semver.MustParse("4.19.20")),
			clusterServiceID: testClusterServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{}, nil))

				// Policy creation fails
				mc.EXPECT().
					PostControlPlaneUpgradePolicy(
						context.Background(),
						testClusterServiceID,
						arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.20"),
					).
					Return(nil, fmt.Errorf("cluster service API error"))
			},
			expectError:           true,
			expectedErrorContains: "failed to create control plane upgrade policy",
			expectPolicyCreation:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.mockSetup(mockClusterServiceClient)

			syncer := &triggerControlPlaneUpgradeSyncer{
				clusterServiceClient: mockClusterServiceClient,
			}

			ctx := context.Background()
			err := syncer.createUpgradePolicyIfNeeded(ctx, tt.desiredVersion, tt.clusterServiceID)

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

func TestTriggerControlPlaneUpgradeSyncer_ShouldTriggerUpgrade(t *testing.T) {
	clusterResourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	listerBoom := errors.New("active operation lister exploded")

	newCluster := func(createdAt *time.Time, activeOperationID string) *coreapi.Cluster {
		c := &coreapi.Cluster{
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
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
				ActiveOperationID: activeOperationID,
			},
		}
		if createdAt != nil {
			c.SystemData = &coreapi.SystemData{CreatedAt: createdAt}
		}
		return c
	}

	tests := []struct {
		name           string
		cluster        *coreapi.Cluster
		seedOperation  bool
		opLister       func(mockDB *corecosmosstoragetesting.MockResourcesDBClient) corelisters.ActiveOperationLister
		wantShouldRun  bool
		wantErrContain string
	}{
		{
			name:          "cluster older than grace period runs even with active create (gate 1)",
			cluster:       newCluster(ptr.To(now.Add(-3*time.Hour)), "op-create-1"),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster with no SystemData.CreatedAt runs (treated as old enough)",
			cluster:       newCluster(nil, "op-create-1"),
			seedOperation: true,
			wantShouldRun: true,
		},
		{
			name:          "cluster younger than grace period with no active create runs (gate 2)",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), ""),
			seedOperation: false,
			wantShouldRun: true,
		},
		{
			name:          "young cluster + active create skips",
			cluster:       newCluster(ptr.To(now.Add(-5*time.Minute)), "op-create-1"),
			seedOperation: true,
			wantShouldRun: false,
		},
		{
			name:          "cluster exactly at grace period boundary still skips (boundary is strict >)",
			cluster:       newCluster(ptr.To(now.Add(-clusterCreateGracePeriod)), "op-create-1"),
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
			syncer := &triggerControlPlaneUpgradeSyncer{
				clock:                 clocktesting.NewFakePassiveClock(now),
				activeOperationLister: opLister,
			}

			gotShouldRun, err := syncer.shouldTriggerUpgrade(ctx, tt.cluster)
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

// TestTriggerControlPlaneUpgradeSyncer_SyncOnce exercises the full SyncOnce path,
// including the cluster read that now comes from the informer-backed ClusterLister.
//
// The cluster and ServiceProviderCluster are placed ONLY in slice-backed cache
// listers and are deliberately NOT written to any mock ResourcesDBClient. This
// controller performs no Cosmos writes, so no DB is needed at all. Because the
// objects live only in the cache, reverting SyncOnce to a live
// c.resourcesDBClient.HCPClusters(...).Get(...) read would resolve NotFound and
// return early, so the "triggers upgrade" case would fail — the test genuinely
// guards the cached read rather than silently accepting a DB read.
func TestTriggerControlPlaneUpgradeSyncer_SyncOnce(t *testing.T) {
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	testClusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testCSClusterIDStr))

	// clusterInCache builds a cluster with a ClusterServiceID and no SystemData
	// (so shouldTriggerUpgrade passes the grace-period gate without consulting the
	// active-operation lister). It is only ever stored in the slice cache lister.
	clusterInCache := func() *coreapi.Cluster {
		clusterResourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID(testSubscriptionID, testResourceGroupName, testClusterName))
		return &coreapi.Cluster{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: clusterResourceID},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: clusterResourceID, Name: testClusterName, Type: coreapi.ClusterResourceType.String()},
			},
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
				ClusterServiceID: ptr.To(testClusterServiceID),
			},
		}
	}
	spcInCache := func(activeVersion, desiredVersion string) *coreapi.ServiceProviderCluster {
		spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(coreapihelpers.ToServiceProviderClusterResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName)))
		active := semver.MustParse(activeVersion)
		desired := semver.MustParse(desiredVersion)
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID},
			Spec: coreapi.ServiceProviderClusterSpec{
				ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{DesiredVersion: &desired},
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{
					ActiveVersions: []coreapi.ServiceProviderClusterActiveVersion{{Version: &active}},
				},
			},
		}
	}

	tests := []struct {
		name      string
		clusters  []*coreapi.Cluster
		spcs      []*coreapi.ServiceProviderCluster
		mockSetup func(*ocm.MockClusterServiceClientSpec)
	}{
		{
			name:      "cluster absent from cache returns nil",
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:      "desired version matches latest active version returns nil",
			clusters:  []*coreapi.Cluster{clusterInCache()},
			spcs:      []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.15")},
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:     "desired version differs from active version triggers upgrade policy from cached cluster read",
			clusters: []*coreapi.Cluster{clusterInCache()},
			spcs:     []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator([]*arohcpv1alpha1.ControlPlaneUpgradePolicy{}, nil))
				expectedBuilder := arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.22")
				mc.EXPECT().
					PostControlPlaneUpgradePolicy(gomock.Any(), testClusterServiceID, expectedBuilder).
					Return(metadataapi.Must(expectedBuilder.Build()), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

			mockClusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.mockSetup(mockClusterServiceClient)

			syncer := &triggerControlPlaneUpgradeSyncer{
				clock:                        clocktesting.NewFakePassiveClock(now),
				clusterLister:                &corelistertesting.SliceClusterLister{Clusters: tt.clusters},
				clusterServiceClient:         mockClusterServiceClient,
				activeOperationLister:        &corelistertesting.SliceActiveOperationLister{},
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: tt.spcs},
			}

			err := syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			})
			assertSyncResult(t, err, false, "")
		})
	}
}
