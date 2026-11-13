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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	writeContext := gomock.Cond[context.Context](func(ctx context.Context) bool {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		return ok && remaining > controllerutils.CSWriteTimeout-time.Second && remaining <= controllerutils.CSWriteTimeout
	})

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
						writeContext,
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
						writeContext,
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
						writeContext,
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
	deletingCluster := clusterInCache()
	deletingCluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.NewTime(now))
	csDeletingCluster := clusterInCache()
	csDeletingCluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = ptr.To(metav1.NewTime(now))
	uninstallingCluster := clusterInCache()
	uninstallingCluster.ServiceProviderProperties.ClusterServiceUninstallingObserved = true
	previouslyObservedSPC := spcInCache("4.19.15", "4.19.22")
	previouslyObservedSPC.Status.Conditions = []metav1.Condition{{
		Type: coreapi.ControlPlaneUpgradeMonitoringEnabled, Status: metav1.ConditionTrue,
		Reason: "Active", Message: "Cluster is not deleting or uninstalling.",
		LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
	}}
	recoveringSPC := spcInCache("4.19.15", "4.19.22")
	recoveringSPC.Status.Conditions = []metav1.Condition{{
		Type: coreapi.ControlPlaneUpgradeMonitoringEnabled, Status: metav1.ConditionFalse,
		Reason: "Uninstalling", LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
	}}
	initializingSPC := spcInCache("4.19.15", "4.19.22")
	initializingSPC.Status.ControlPlaneVersion.ActiveVersions = nil
	unselectedSPC := spcInCache("4.19.15", "4.19.22")
	unselectedSPC.Spec.ControlPlaneVersion.DesiredVersion = nil
	invalidVersionSPC := spcInCache("4.19.15", "4.19.22")
	invalidVersionSPC.Status.ControlPlaneVersion.ActiveVersions[0].Version = nil

	tests := []struct {
		name         string
		clusters     []*coreapi.Cluster
		spcs         []*coreapi.ServiceProviderCluster
		mockSetup    func(*ocm.MockClusterServiceClientSpec)
		csState      arohcpv1alpha1.ClusterState
		csError      error
		wantError    string
		disabled     bool
		missingState bool
		nilStatus    bool
		unobserved   bool
		repeat       bool
		probe        bool
	}{
		{
			name:      "cluster absent from cache returns nil",
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:       "initializing cluster does not probe CS",
			clusters:   []*coreapi.Cluster{clusterInCache()},
			spcs:       []*coreapi.ServiceProviderCluster{initializingSPC},
			unobserved: true,
			mockSetup:  func(*ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:       "cluster with no desired target does not probe CS",
			clusters:   []*coreapi.Cluster{clusterInCache()},
			spcs:       []*coreapi.ServiceProviderCluster{unselectedSPC},
			unobserved: true,
			mockSetup:  func(*ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:       "missing active version fails before any CS request",
			clusters:   []*coreapi.Cluster{clusterInCache()},
			spcs:       []*coreapi.ServiceProviderCluster{invalidVersionSPC},
			unobserved: true,
			wantError:  "latest active control plane version is missing",
			mockSetup:  func(*ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:       "desired version matches latest active version returns nil",
			clusters:   []*coreapi.Cluster{clusterInCache()},
			spcs:       []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.15")},
			mockSetup:  func(mc *ocm.MockClusterServiceClientSpec) {},
			repeat:     true,
			unobserved: true,
		},
		{
			name:  "desired version differs from active version triggers upgrade policy from cached cluster read",
			probe: true, unobserved: true,
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
		{
			name:      "RP deletion disables monitoring without calling CS",
			clusters:  []*coreapi.Cluster{deletingCluster},
			spcs:      []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			mockSetup: func(*ocm.MockClusterServiceClientSpec) {},
			disabled:  true,
		},
		{
			name:      "CS deletion timestamp disables without a CS probe",
			clusters:  []*coreapi.Cluster{csDeletingCluster},
			spcs:      []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			mockSetup: func(*ocm.MockClusterServiceClientSpec) {},
			disabled:  true,
		},
		{
			name:      "CS uninstalling blocks dispatch without an RP deletion timestamp",
			probe:     true,
			clusters:  []*coreapi.Cluster{clusterInCache()},
			spcs:      []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			csState:   arohcpv1alpha1.ClusterStateUninstalling,
			mockSetup: func(*ocm.MockClusterServiceClientSpec) {},
			disabled:  true,
		},
		{
			name:       "converged cluster with no observation neither probes nor writes",
			clusters:   []*coreapi.Cluster{clusterInCache()},
			spcs:       []*coreapi.ServiceProviderCluster{spcInCache("4.19.22", "4.19.22")},
			mockSetup:  func(*ocm.MockClusterServiceClientSpec) {},
			unobserved: true,
			repeat:     true,
		},
		{
			name:      "durable uninstallation observation is rechecked before dispatch",
			clusters:  []*coreapi.Cluster{uninstallingCluster},
			spcs:      []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			mockSetup: func(*ocm.MockClusterServiceClientSpec) {},
			disabled:  true,
			csState:   arohcpv1alpha1.ClusterStateUninstalling,
			probe:     true,
		},
		{
			name:     "a later healthy dispatch candidate recovers a spurious uninstallation observation",
			clusters: []*coreapi.Cluster{uninstallingCluster.DeepCopy()},
			spcs:     []*coreapi.ServiceProviderCluster{recoveringSPC},
			probe:    true,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().ListControlPlaneUpgradePolicies(testClusterServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleControlPlaneUpgradePolicyListIterator(nil, nil))
				builder := arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version("4.19.22")
				mc.EXPECT().PostControlPlaneUpgradePolicy(gomock.Any(), testClusterServiceID, builder).
					Return(metadataapi.Must(builder.Build()), nil)
			},
		},
		{
			name:      "CS lookup failure blocks dispatch and preserves the previous observation",
			probe:     true,
			clusters:  []*coreapi.Cluster{clusterInCache()},
			spcs:      []*coreapi.ServiceProviderCluster{previouslyObservedSPC},
			csError:   errors.New("CS unavailable"),
			wantError: "failed to observe cluster lifecycle before upgrade",
			mockSetup: func(*ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:       "CS lookup failure before observation does not publish recovery",
			probe:      true,
			clusters:   []*coreapi.Cluster{clusterInCache()},
			spcs:       []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			csError:    errors.New("CS unavailable"),
			wantError:  "failed to observe cluster lifecycle before upgrade",
			unobserved: true,
			mockSetup:  func(*ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:     "nil CS status blocks a dispatch candidate",
			clusters: []*coreapi.Cluster{clusterInCache()},
			spcs:     []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			probe:    true, nilStatus: true, unobserved: true,
			wantError: "cluster service returned no cluster lifecycle state",
			mockSetup: func(*ocm.MockClusterServiceClientSpec) {},
		},
		{
			name:         "missing CS lifecycle state blocks dispatch",
			probe:        true,
			clusters:     []*coreapi.Cluster{clusterInCache()},
			spcs:         []*coreapi.ServiceProviderCluster{spcInCache("4.19.15", "4.19.22")},
			missingState: true,
			wantError:    "cluster service returned no cluster lifecycle state",
			unobserved:   true,
			mockSetup:    func(*ocm.MockClusterServiceClientSpec) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

			mockClusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.mockSetup(mockClusterServiceClient)
			if tt.probe {
				if tt.csError != nil {
					mockClusterServiceClient.EXPECT().GetClusterStatus(gomock.Any(), testClusterServiceID).Return(nil, tt.csError)
				} else {
					state := tt.csState
					if state == "" {
						state = arohcpv1alpha1.ClusterStateReady
					}
					builder := arohcpv1alpha1.NewClusterStatus()
					if !tt.missingState {
						builder.State(state)
					}
					csStatus := metadataapi.Must(builder.Build())
					if tt.nilStatus {
						csStatus = nil
					}
					calls := 1
					if tt.repeat {
						calls = 2
					}
					readContext := gomock.Cond[context.Context](func(ctx context.Context) bool {
						deadline, ok := ctx.Deadline()
						remaining := time.Until(deadline)
						return ok && remaining > controllerutils.CSReadTimeout-time.Second && remaining <= controllerutils.CSReadTimeout
					})
					mockClusterServiceClient.EXPECT().GetClusterStatus(readContext, testClusterServiceID).Return(csStatus, nil).Times(calls)
				}
			}
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			if tt.csState == arohcpv1alpha1.ClusterStateUninstalling || (len(tt.clusters) > 0 && tt.clusters[0].ServiceProviderProperties.ClusterServiceUninstallingObserved) {
				tt.clusters[0].PartitionKey = testSubscriptionID
				created, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, tt.clusters[0], nil)
				require.NoError(t, err)
				tt.clusters[0] = created
			}
			for i, spc := range tt.spcs {
				spc.PartitionKey = testSubscriptionID
				created, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
				require.NoError(t, err)
				tt.spcs[i] = created
			}

			spcLister := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: tt.spcs}
			clock := clocktesting.NewFakeClock(now)
			syncer := &triggerControlPlaneUpgradeSyncer{
				clock:                        clock,
				resourcesDBClient:            db,
				clusterLister:                &corelistertesting.SliceClusterLister{Clusters: tt.clusters},
				clusterServiceClient:         mockClusterServiceClient,
				activeOperationLister:        &corelistertesting.SliceActiveOperationLister{},
				serviceProviderClusterLister: spcLister,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			err := syncer.SyncOnce(ctx, key)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
			if tt.csState == arohcpv1alpha1.ClusterStateUninstalling || (len(tt.clusters) > 0 && tt.clusters[0].ServiceProviderProperties.ClusterServiceUninstallingObserved) {
				parent, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.Equal(t, tt.csState == arohcpv1alpha1.ClusterStateUninstalling, parent.ServiceProviderProperties.ClusterServiceUninstallingObserved, "persist the latest dispatch-path observation")
			}
			if len(tt.spcs) > 0 {
				stored, getErr := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, getErr)
				condition := apimeta.FindStatusCondition(stored.Status.Conditions, coreapi.ControlPlaneUpgradeMonitoringEnabled)
				if tt.unobserved {
					require.Nil(t, condition, "an unsuccessful first observation must not enable or disable monitoring")
				} else {
					require.NotNil(t, condition)
					wantStatus := metav1.ConditionTrue
					if tt.disabled {
						wantStatus = metav1.ConditionFalse
					}
					require.Equal(t, wantStatus, condition.Status)
				}
				if tt.csError != nil && !tt.unobserved {
					previous := previouslyObservedSPC.Status.Conditions[0]
					require.Equal(t, previous.Reason, condition.Reason)
					require.Equal(t, previous.Message, condition.Message)
					require.True(t, previous.LastTransitionTime.Equal(&condition.LastTransitionTime), "failed lifecycle reads must preserve the observation timestamp")
				}
				if tt.repeat {
					firstETag := stored.CosmosETag
					spcLister.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{stored}
					clock.Step(time.Minute)
					require.NoError(t, syncer.SyncOnce(ctx, key))
					again, getErr := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
					require.NoError(t, getErr)
					require.Equal(t, firstETag, again.CosmosETag, "unchanged lifecycle observations must not rewrite Cosmos")
				}
			}
		})
	}
}
