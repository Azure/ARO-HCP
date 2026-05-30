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
package backupcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

func TestNewScheduledBackup(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	clusterName := "test-domprefix"
	hcNamespace := "ocm-testenv-" + clusterID
	hcpNamespace := hcNamespace + "-" + clusterName
	cronSchedule := "0 */1 * * *"
	ttl := 7 * 24 * time.Hour

	schedule := NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace, "hourly", cronSchedule, ttl, false)

	assert.Equal(t, clusterID+"-hourly", schedule.Name)
	assert.Equal(t, "velero", schedule.Namespace)

	assert.Equal(t, clusterID, schedule.Labels["api.openshift.com/id"])
	assert.Equal(t, "default", schedule.Labels["velero.io/storage-location"])
	assert.Equal(t, clusterName, schedule.Labels["hypershift.openshift.io/hosted-cluster"])
	assert.Equal(t, hcNamespace, schedule.Labels["hypershift.openshift.io/hosted-cluster-namespace"])

	assert.Equal(t, cronSchedule, schedule.Spec.Schedule)
	assert.Equal(t, false, schedule.Spec.Paused)
	assert.Equal(t, metav1.Duration{Duration: ttl}, schedule.Spec.Template.TTL)
	assert.Contains(t, schedule.Spec.Template.IncludedNamespaces, hcNamespace)
	assert.Contains(t, schedule.Spec.Template.IncludedNamespaces, hcpNamespace)

	// Verify paused flag is respected
	pausedSchedule := NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace, "hourly", cronSchedule, ttl, true)
	assert.Equal(t, true, pausedSchedule.Spec.Paused)

	// Verify different schedule names produce different Velero Schedule names
	dailySchedule := NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace, "daily", "0 2 * * *", 48*time.Hour, false)
	assert.Equal(t, clusterID+"-daily", dailySchedule.Name)
}

func TestBuildScheduleManifestWork(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	hcNamespace := "ocm-testenv-" + clusterID
	hcpNamespace := hcNamespace + "-test-domprefix"

	hourlySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "hourly", "0 */1 * * *", 48*time.Hour, false)
	dailySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "daily", "0 2 * * *", 336*time.Hour, false)
	weeklySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "weekly", "0 3 * * 0", 2160*time.Hour, false)

	schedules := []*velerov1api.Schedule{hourlySchedule, dailySchedule, weeklySchedule}
	mw, err := buildScheduleManifestWork(
		types.NamespacedName{Name: clusterID + "-dr", Namespace: "test-consumer"},
		schedules,
	)
	require.NoError(t, err)

	assert.Equal(t, clusterID+"-dr", mw.Name)
	assert.Equal(t, "test-consumer", mw.Namespace)
	assert.Equal(t, backupScheduleManagedByK8sLabelValue, mw.Labels[backupScheduleManagedByK8sLabelKey])

	require.Len(t, mw.Spec.Workload.Manifests, 3)
	for i, s := range schedules {
		assert.Nil(t, mw.Spec.Workload.Manifests[i].Object)
		require.NotEmpty(t, mw.Spec.Workload.Manifests[i].Raw)

		var got velerov1api.Schedule
		require.NoError(t, json.Unmarshal(mw.Spec.Workload.Manifests[i].Raw, &got))
		assert.Equal(t, s.Name, got.Name)
		assert.Equal(t, s.Namespace, got.Namespace)
		assert.Equal(t, s.Spec, got.Spec)
	}

	require.Len(t, mw.Spec.ManifestConfigs, 3)
	for i, s := range schedules {
		mc := mw.Spec.ManifestConfigs[i]
		assert.Equal(t, "velero.io", mc.ResourceIdentifier.Group)
		assert.Equal(t, "schedules", mc.ResourceIdentifier.Resource)
		assert.Equal(t, s.Name, mc.ResourceIdentifier.Name)
		assert.Equal(t, veleroNamespace, mc.ResourceIdentifier.Namespace)
		assert.Equal(t, workv1.UpdateStrategyTypeServerSideApply, mc.UpdateStrategy.Type)

		require.Len(t, mc.FeedbackRules, 1)
		assert.Equal(t, workv1.JSONPathsType, mc.FeedbackRules[0].Type)
		require.Len(t, mc.FeedbackRules[0].JsonPaths, 1)
		assert.Equal(t, "status", mc.FeedbackRules[0].JsonPaths[0].Name)
		assert.Equal(t, ".status", mc.FeedbackRules[0].JsonPaths[0].Path)
	}
}

func TestBackupActionValidate(t *testing.T) {
	tests := []struct {
		name        string
		action      backupAction
		expectError bool
	}{
		{
			name:   "no fields set is valid",
			action: backupAction{},
		},
		{
			name:   "only createManifestWork set is valid",
			action: backupAction{createManifestWork: &workv1.ManifestWork{}},
		},
		{
			name:   "only patchManifestWork set is valid",
			action: backupAction{patchManifestWork: &workv1.ManifestWork{}},
		},
		{
			name:   "only updateSPC set is valid",
			action: backupAction{updateSPC: &api.ServiceProviderCluster{}},
		},
		{
			name: "two fields set is invalid",
			action: backupAction{
				createManifestWork: &workv1.ManifestWork{},
				updateSPC:          &api.ServiceProviderCluster{},
			},
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.action.validate()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "programmer error")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBackupSteps(t *testing.T) {
	matchingSpec := workv1.ManifestWorkSpec{
		Workload: workv1.ManifestsTemplate{
			Manifests: []workv1.Manifest{{RawExtension: runtime.RawExtension{Raw: []byte(`{"schedule":"0 */1 * * *"}`)}}},
		},
	}
	desiredMW := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{Name: "test-mw"},
		Spec: workv1.ManifestWorkSpec{
			Workload: workv1.ManifestsTemplate{
				Manifests: []workv1.Manifest{{RawExtension: runtime.RawExtension{Raw: []byte(`{"schedule":"*/5 * * * *"}`)}}},
			},
		},
	}

	tests := []struct {
		name         string
		step         func(*backupScheduleSyncer) backupStep
		setupMock    func(*maestro.MockClient)
		desiredMW    *workv1.ManifestWork
		expectDone   bool
		expectAction bool
		expectError  bool
	}{
		{
			name:      "create: MW not found returns create action",
			step:      func(s *backupScheduleSyncer) backupStep { return s.ensureManifestWorkCreated },
			desiredMW: desiredMW,
			setupMock: func(mc *maestro.MockClient) {
				mc.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
					Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
			},
			expectDone:   true,
			expectAction: true,
		},
		{
			name:      "create: MW exists passes through",
			step:      func(s *backupScheduleSyncer) backupStep { return s.ensureManifestWorkCreated },
			desiredMW: desiredMW,
			setupMock: func(mc *maestro.MockClient) {
				mc.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
					Return(&workv1.ManifestWork{ObjectMeta: metav1.ObjectMeta{Name: "test-mw"}}, nil)
			},
			expectDone: false,
		},
		{
			name: "create: Get error returns error",
			step: func(s *backupScheduleSyncer) backupStep { return s.ensureManifestWorkCreated },
			setupMock: func(mc *maestro.MockClient) {
				mc.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
					Return(nil, fmt.Errorf("maestro API error"))
			},
			expectDone:  true,
			expectError: true,
		},
		{
			name:      "patch: matching spec passes through",
			step:      func(s *backupScheduleSyncer) backupStep { return s.ensureManifestWorkPatched },
			desiredMW: &workv1.ManifestWork{Spec: matchingSpec},
			setupMock: func(mc *maestro.MockClient) {
				mc.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
					Return(&workv1.ManifestWork{ObjectMeta: metav1.ObjectMeta{Name: "test-mw"}, Spec: matchingSpec}, nil)
			},
			expectDone: false,
		},
		{
			name:      "patch: drifted spec returns patch action",
			step:      func(s *backupScheduleSyncer) backupStep { return s.ensureManifestWorkPatched },
			desiredMW: desiredMW,
			setupMock: func(mc *maestro.MockClient) {
				mc.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
					Return(&workv1.ManifestWork{
						ObjectMeta: metav1.ObjectMeta{Name: "test-mw"},
						Spec: workv1.ManifestWorkSpec{
							Workload: workv1.ManifestsTemplate{
								Manifests: []workv1.Manifest{{RawExtension: runtime.RawExtension{Raw: []byte(`{"schedule":"0 */6 * * *"}`)}}},
							},
						},
					}, nil)
			},
			expectDone:   true,
			expectAction: true,
		},
		{
			name: "patch: Get error returns error",
			step: func(s *backupScheduleSyncer) backupStep { return s.ensureManifestWorkPatched },
			setupMock: func(mc *maestro.MockClient) {
				mc.EXPECT().Get(gomock.Any(), "test-mw", gomock.Any()).
					Return(nil, fmt.Errorf("maestro API error"))
			},
			expectDone:  true,
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestroClient := maestro.NewMockClient(ctrl)
			tt.setupMock(mockMaestroClient)

			state := &backupSyncState{
				maestroClient:       mockMaestroClient,
				manifestWorkName:    "test-mw",
				desiredManifestWork: tt.desiredMW,
			}

			syncer := &backupScheduleSyncer{}
			step := tt.step(syncer)
			done, action, err := step(context.Background(), state)

			assert.Equal(t, tt.expectDone, done)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.expectAction {
				require.NotNil(t, action)
			} else {
				assert.Nil(t, action)
			}
		})
	}
}

func TestRecordManifestWorkInStatus(t *testing.T) {
	tests := []struct {
		name           string
		existingMWName string
		expectAction   bool
		expectDone     bool
	}{
		{
			name:           "already recorded passes through to next step",
			existingMWName: "test-mw",
			expectAction:   false,
			expectDone:     false,
		},
		{
			name:           "not recorded returns update action",
			existingMWName: "",
			expectAction:   true,
			expectDone:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spc := &api.ServiceProviderCluster{}
			spc.Status.BackupScheduleManifestWorkName = tt.existingMWName

			state := &backupSyncState{
				spc:              spc,
				manifestWorkName: "test-mw",
			}

			syncer := &backupScheduleSyncer{}
			done, action, err := syncer.recordManifestWorkInStatus(context.Background(), state)

			assert.Equal(t, tt.expectDone, done)
			require.NoError(t, err)
			if tt.expectAction {
				require.NotNil(t, action)
				assert.Equal(t, spc, action.updateSPC)
				assert.Equal(t, "test-mw", spc.Status.BackupScheduleManifestWorkName)
			} else {
				assert.Nil(t, action)
			}
		})
	}
}

func TestBackupScheduleSyncer_SyncOnce(t *testing.T) {
	const (
		testClusterID    = "11111111111111111111111111111111"
		testClusterIDStr = "/api/aro_hcp/v1alpha1/clusters/" + testClusterID
		manifestWorkName = testClusterID + "-dr"
		testEnvID        = "test-env"
		testDomainPrefix = "test-domprefix"
		testConsumer     = "test-consumer"
		testShardID      = "test-shard-id"
		testStampID      = "mc1"
	)

	testBackupConfig := &BackupConfig{
		Schedules: []BackupScheduleConfig{
			{Name: "hourly", Schedule: "0 */1 * * *", TTL: "48h"},
			{Name: "daily", Schedule: "0 2 * * *", TTL: "336h"},
			{Name: "weekly", Schedule: "0 3 * * 0", TTL: "2160h"},
		},
	}

	testMgmtClusterResourceID := func() *azcorearm.ResourceID {
		return api.Must(fleet.ToManagementClusterResourceID(testStampID))
	}

	buildExpectedMW := func() *workv1.ManifestWork {
		hcNamespace := fmt.Sprintf("ocm-%s-%s", testEnvID, testClusterID)
		hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, testDomainPrefix)
		schedules := make([]*velerov1api.Schedule, 0, len(testBackupConfig.Schedules))
		for _, sc := range testBackupConfig.Schedules {
			schedules = append(schedules, NewScheduledBackup(testClusterID, testDomainPrefix, hcNamespace, hcpNamespace, sc.Name, sc.Schedule, sc.TTLDuration(), false))
		}
		mw, err := buildScheduleManifestWork(
			types.NamespacedName{Name: manifestWorkName, Namespace: testConsumer},
			schedules,
		)
		require.NoError(t, err)
		return simulateMaestroRoundTrip(t, mw)
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	newTestCluster := func(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
		resourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		csID := api.Must(api.NewInternalID(testClusterIDStr))
		cluster := &api.HCPOpenShiftCluster{
			CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID},
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{ID: resourceID},
			},
			CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
				DNS: api.CustomerDNSProfile{
					BaseDomainPrefix: testDomainPrefix,
				},
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState: arm.ProvisioningStateSucceeded,
				ClusterServiceID:  &csID,
			},
		}
		for _, opt := range opts {
			opt(cluster)
		}
		return cluster
	}

	seedFleetDB := func(t *testing.T, ctx context.Context, fleetDB *databasetesting.MockFleetDBClient) {
		t.Helper()
		mc := newTestManagementCluster(testStampID, testShardID, testConsumer, "https://maestro-rest", "maestro-grpc:8090")
		_, err := fleetDB.Stamps().ManagementClusters(testStampID).Create(ctx, mc, nil)
		require.NoError(t, err)
	}

	seedSPCWithPlacement := func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
		t.Helper()
		spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
		require.NoError(t, err)
		spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
		_, err = mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(ctx, spc, nil)
		require.NoError(t, err)
	}

	setupMaestroMock := func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
		t.Helper()
		mb.EXPECT().NewClient(gomock.Any(), "https://maestro-rest", "maestro-grpc:8090", testConsumer, gomock.Any()).Return(mc, nil)
	}

	tests := []struct {
		name          string
		seedDB        func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient)
		setupMocks    func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient)
		clusterOpts   []func(*api.HCPOpenShiftCluster)
		expectError   bool
		errorContains string
		verify        func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "cluster not found in DB is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
			},
		},
		{
			name: "installing cluster is skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateProvisioning
				}), nil)
				require.NoError(t, err)
			},
		},
		{
			name: "creates ManifestWork when not found",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
				seedFleetDB(t, ctx, fleetDB)
			},
			setupMocks: func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				t.Helper()
				setupMaestroMock(t, mb, mc)
				mc.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
					Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				mc.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&workv1.ManifestWork{ObjectMeta: metav1.ObjectMeta{Name: manifestWorkName}}, nil)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
				require.NoError(t, err)
				assert.Empty(t, spc.Status.BackupScheduleManifestWorkName, "SPC should not be updated on the same sync that creates the MW")
			},
		},
		{
			name: "updates SPC when MW already exists",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
				seedFleetDB(t, ctx, fleetDB)
			},
			setupMocks: func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				t.Helper()
				setupMaestroMock(t, mb, mc)
				mc.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
					Return(buildExpectedMW(), nil).Times(2)
			},
			verify: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
				require.NoError(t, err)
				assert.Equal(t, manifestWorkName, spc.Status.BackupScheduleManifestWorkName)
			},
		},
		{
			name: "no-op when fully reconciled",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
				spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
				require.NoError(t, err)
				spc.Status.BackupScheduleManifestWorkName = manifestWorkName
				_, err = mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(ctx, spc, nil)
				require.NoError(t, err)
				seedFleetDB(t, ctx, fleetDB)
			},
			setupMocks: func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				t.Helper()
				setupMaestroMock(t, mb, mc)
				mc.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
					Return(buildExpectedMW(), nil).Times(3)
			},
		},
		{
			name: "maestro create error is propagated",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
				seedFleetDB(t, ctx, fleetDB)
			},
			setupMocks: func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				t.Helper()
				setupMaestroMock(t, mb, mc)
				mc.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
					Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				mc.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("maestro API error"))
			},
			expectError:   true,
			errorContains: "failed to create ManifestWork",
		},
		{
			name: "patches MW when spec has drifted",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
				seedFleetDB(t, ctx, fleetDB)
			},
			setupMocks: func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				t.Helper()
				setupMaestroMock(t, mb, mc)
				staleMW := &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: manifestWorkName},
					Spec: workv1.ManifestWorkSpec{
						Workload: workv1.ManifestsTemplate{
							Manifests: []workv1.Manifest{{RawExtension: runtime.RawExtension{Raw: []byte(`{"old":"spec"}`)}}},
						},
					},
				}
				mc.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
					Return(staleMW, nil).Times(2)
				mc.EXPECT().Patch(gomock.Any(), manifestWorkName, types.MergePatchType, gomock.Any(), gomock.Any()).
					Return(buildExpectedMW(), nil)
			},
		},
		{
			name: "failed state cluster still gets backup",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, fleetDB *databasetesting.MockFleetDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateFailed
				}), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
				seedFleetDB(t, ctx, fleetDB)
			},
			setupMocks: func(t *testing.T, mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				t.Helper()
				setupMaestroMock(t, mb, mc)
				mc.EXPECT().Get(gomock.Any(), manifestWorkName, gomock.Any()).
					Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				mc.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&workv1.ManifestWork{ObjectMeta: metav1.ObjectMeta{Name: manifestWorkName}}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			mockDB := databasetesting.NewMockResourcesDBClient()
			mockFleetDB := databasetesting.NewMockFleetDBClient()
			mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
			mockMaestroClient := maestro.NewMockClient(ctrl)

			tt.seedDB(t, ctx, mockDB, mockFleetDB)
			if tt.setupMocks != nil {
				tt.setupMocks(t, mockMaestroBuilder, mockMaestroClient)
			}

			syncer := &backupScheduleSyncer{
				cooldownChecker:                    &alwaysSyncCooldownChecker{},
				cosmosClient:                       mockDB,
				fleetDBClient:                      mockFleetDB,
				maestroSourceEnvironmentIdentifier: testEnvID,
				maestroClientBuilder:               mockMaestroBuilder,
				backupConfig:                       testBackupConfig,
			}

			err := syncer.SyncOnce(ctx, testKey)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB)
			}
		})
	}
}

// simulateMaestroRoundTrip converts a ManifestWork to the form returned by
// the Maestro API: RawExtension.Object fields are serialized to Raw JSON bytes.
func simulateMaestroRoundTrip(t *testing.T, mw *workv1.ManifestWork) *workv1.ManifestWork {
	t.Helper()
	raw, err := json.Marshal(mw)
	require.NoError(t, err)
	var roundTripped workv1.ManifestWork
	require.NoError(t, json.Unmarshal(raw, &roundTripped))
	return &roundTripped
}

func TestEnsureManifestWorkPatched_IdempotentAfterRoundTrip(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	hcNamespace := "ocm-testenv-" + clusterID
	hcpNamespace := hcNamespace + "-test-domprefix"
	schedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "hourly", "0 */1 * * *", 48*time.Hour, false)
	desiredMW, err := buildScheduleManifestWork(
		types.NamespacedName{Name: clusterID + "-dr", Namespace: "test-consumer"},
		[]*velerov1api.Schedule{schedule},
	)
	require.NoError(t, err)

	actualMW := simulateMaestroRoundTrip(t, desiredMW)

	ctrl := gomock.NewController(t)
	mockClient := maestro.NewMockClient(ctrl)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(actualMW, nil)

	state := &backupSyncState{
		maestroClient:       mockClient,
		manifestWorkName:    clusterID + "-dr",
		desiredManifestWork: desiredMW,
	}

	syncer := &backupScheduleSyncer{}
	done, action, err := syncer.ensureManifestWorkPatched(context.Background(), state)

	require.NoError(t, err)
	assert.False(t, done, "should pass through (no patch needed)")
	assert.Nil(t, action, "should not return a patch action for identical content")
}

func TestGetHostedClusterNamespace(t *testing.T) {
	syncer := &backupScheduleSyncer{}
	result := syncer.getHostedClusterNamespace("testenv", "11111111111111111111111111111111")
	assert.Equal(t, "ocm-testenv-11111111111111111111111111111111", result)
}

func newTestManagementCluster(stampID, shardID, consumerName, restURL, grpcTarget string) *fleet.ManagementCluster {
	resourceID := api.Must(fleet.ToManagementClusterResourceID(stampID))
	return &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ResourceID:     resourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks")),
			PublicDNSZoneResourceID:                              api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://kv.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "00000000-0000-0000-0000-000000000000",
			MaestroConsumerName:                                  consumerName,
			MaestroRESTAPIURL:                                    restURL,
			MaestroGRPCTarget:                                    grpcTarget,
			ClusterServiceProvisionShardID:                       ptr.To(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/" + shardID))),
			KubeApplierCosmosContainerName:                       "kube-applier-test",
		},
	}
}

func TestClusterNeedsBackup(t *testing.T) {
	tests := []struct {
		state arm.ProvisioningState
		want  bool
	}{
		{arm.ProvisioningStateSucceeded, true},
		{arm.ProvisioningStateFailed, true},
		{arm.ProvisioningStateUpdating, true},
		{arm.ProvisioningStateProvisioning, false},
		{arm.ProvisioningStateDeleting, false},
		{arm.ProvisioningStateAccepted, false},
		{arm.ProvisioningStateCanceled, false},
		{arm.ProvisioningStateAwaitingSecret, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.want, clusterNeedsBackup(tt.state))
		})
	}
}
