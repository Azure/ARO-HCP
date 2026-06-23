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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

func TestBuildApplyDesiresFromSchedules(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	hcNamespace := controllers.HostedClusterNamespace("testenv", clusterID)
	hcpNamespace := hcNamespace + "-test-domprefix"
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))

	hourlySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "hourly", "0 */1 * * *", 48*time.Hour, false)
	dailySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "daily", "0 2 * * *", 336*time.Hour, false)
	weeklySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "weekly", "0 3 * * 0", 2160*time.Hour, false)

	schedules := []*velerov1api.Schedule{hourlySchedule, dailySchedule, weeklySchedule}
	desires, err := buildApplyDesiresFromSchedules("test-sub", "test-rg", "test-cluster", mcResourceID, schedules)
	require.NoError(t, err)
	require.Len(t, desires, 3)

	for i, s := range schedules {
		ad := desires[i]
		assert.Equal(t, backupApplyDesireName(s.Name), ad.ResourceID.Name)
		assert.Equal(t, veleroScheduleGroup, ad.Spec.TargetItem.Group)
		assert.Equal(t, veleroScheduleResource, ad.Spec.TargetItem.Resource)
		assert.Equal(t, veleroNamespace, ad.Spec.TargetItem.Namespace)
		assert.Equal(t, s.Name, ad.Spec.TargetItem.Name)
		assert.Equal(t, kubeapplier.ApplyDesireTypeServerSideApply, ad.Spec.Type)
		require.NotNil(t, ad.Spec.ServerSideApply)
		assert.NotNil(t, ad.Spec.ServerSideApply.KubeContent)

		var got velerov1api.Schedule
		require.NoError(t, json.Unmarshal(ad.Spec.ServerSideApply.KubeContent.Raw, &got))
		assert.Equal(t, s.Name, got.Name)
		assert.Equal(t, s.Namespace, got.Namespace)
		assert.Equal(t, s.Spec, got.Spec)
	}
}

func TestBuildReadDesiresFromSchedules(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	hcNamespace := controllers.HostedClusterNamespace("testenv", clusterID)
	hcpNamespace := hcNamespace + "-test-domprefix"
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))

	hourlySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "hourly", "0 */1 * * *", 48*time.Hour, false)

	desires, err := buildReadDesiresFromSchedules("test-sub", "test-rg", "test-cluster", mcResourceID, []*velerov1api.Schedule{hourlySchedule})
	require.NoError(t, err)
	require.Len(t, desires, 1)

	rd := desires[0]
	assert.Equal(t, backupApplyDesireName(hourlySchedule.Name), rd.ResourceID.Name)
	assert.Equal(t, veleroScheduleGroup, rd.Spec.TargetItem.Group)
	assert.Equal(t, veleroScheduleResource, rd.Spec.TargetItem.Resource)
	assert.Equal(t, veleroNamespace, rd.Spec.TargetItem.Namespace)
	assert.Equal(t, hourlySchedule.Name, rd.Spec.TargetItem.Name)
	assert.Nil(t, rd.Status.KubeContent)
}

func TestEnsureDesireCreated(t *testing.T) {
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))

	makeDesiredAD := func(name string, content string) *kubeapplier.ApplyDesire {
		resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: mcResourceID,
				Type:              kubeapplier.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplier.ResourceReference{
					Group: veleroScheduleGroup, Version: veleroScheduleVersion,
					Resource: veleroScheduleResource, Namespace: veleroNamespace, Name: name,
				},
				ServerSideApply: &kubeapplier.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(content)},
				},
			},
		}
	}

	makeDesiredRD := func(name string) *kubeapplier.ReadDesire {
		resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplier.ReadDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: mcResourceID,
				TargetItem: kubeapplier.ResourceReference{
					Group: veleroScheduleGroup, Version: veleroScheduleVersion,
					Resource: veleroScheduleResource, Namespace: veleroNamespace, Name: name,
				},
			},
		}
	}

	tests := []struct {
		name           string
		adCrudOverride database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]
		seedKA         func(*databasetesting.MockKubeApplierDBClient)
		desiredADs     []*kubeapplier.ApplyDesire
		desiredRDs     []*kubeapplier.ReadDesire
		expectError    bool
		expectADExists bool
		expectRDExists bool
	}{
		{
			name:           "creates missing AD and RD",
			seedKA:         func(ka *databasetesting.MockKubeApplierDBClient) {},
			desiredADs:     []*kubeapplier.ApplyDesire{makeDesiredAD("backup-hourly", `{"schedule":"0 */1 * * *"}`)},
			desiredRDs:     []*kubeapplier.ReadDesire{makeDesiredRD("backup-hourly")},
			expectADExists: true,
			expectRDExists: true,
		},
		{
			name: "skips existing AD",
			seedKA: func(ka *databasetesting.MockKubeApplierDBClient) {
				crud, _ := ka.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
				_, _ = crud.Create(context.Background(), makeDesiredAD("backup-hourly", `{"schedule":"0 */1 * * *"}`), nil)
			},
			desiredADs:     []*kubeapplier.ApplyDesire{makeDesiredAD("backup-hourly", `{"schedule":"0 */1 * * *"}`)},
			desiredRDs:     []*kubeapplier.ReadDesire{makeDesiredRD("backup-hourly")},
			expectADExists: true,
		},
		{
			name:           "DB error on Get returns error",
			adCrudOverride: &erroringADCrud{err: fmt.Errorf("cosmos unavailable")},
			desiredADs:     []*kubeapplier.ApplyDesire{makeDesiredAD("backup-hourly", `{"schedule":"0 */1 * * *"}`)},
			expectError:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var adCrud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]
			var rdCrud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire]
			if tt.adCrudOverride != nil {
				adCrud = tt.adCrudOverride
			} else {
				mockKA := databasetesting.NewMockKubeApplierDBClient()
				tt.seedKA(mockKA)
				adCrud, _ = mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
				rdCrud, _ = mockKA.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")
			}

			syncer := &backupScheduleSyncer{}
			err := syncer.ensureDesireCreated(context.Background(), adCrud, rdCrud, tt.desiredADs, tt.desiredRDs)

			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.expectADExists {
				_, err := adCrud.Get(context.Background(), "backup-hourly")
				assert.NoError(t, err, "expected AD to exist")
			}
			if tt.expectRDExists {
				_, err := rdCrud.Get(context.Background(), "backup-hourly")
				assert.NoError(t, err, "expected RD to exist")
			}
		})
	}
}

func TestEnsureDesireUpdated(t *testing.T) {
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))

	makeDesiredAD := func(name string, content string) *kubeapplier.ApplyDesire {
		resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: mcResourceID,
				Type:              kubeapplier.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplier.ResourceReference{
					Group: veleroScheduleGroup, Version: veleroScheduleVersion,
					Resource: veleroScheduleResource, Namespace: veleroNamespace, Name: name,
				},
				ServerSideApply: &kubeapplier.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(content)},
				},
			},
		}
	}

	t.Run("matching content is no-op", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		_, _ = adCrud.Create(context.Background(), makeDesiredAD("backup-hourly", `{"schedule":"0 */1 * * *"}`), nil)

		syncer := &backupScheduleSyncer{}
		err := syncer.ensureDesireUpdated(context.Background(), adCrud,
			[]*kubeapplier.ApplyDesire{makeDesiredAD("backup-hourly", `{"schedule":"0 */1 * * *"}`)})
		require.NoError(t, err)
	})

	t.Run("drifted content replaces", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		_, _ = adCrud.Create(context.Background(), makeDesiredAD("backup-hourly", `{"schedule":"0 */6 * * *"}`), nil)

		syncer := &backupScheduleSyncer{}
		err := syncer.ensureDesireUpdated(context.Background(), adCrud,
			[]*kubeapplier.ApplyDesire{makeDesiredAD("backup-hourly", `{"schedule":"*/5 * * * *"}`)})
		require.NoError(t, err)

		ad, err := adCrud.Get(context.Background(), "backup-hourly")
		require.NoError(t, err)
		assert.Contains(t, string(ad.Spec.ServerSideApply.KubeContent.Raw), `*/5 * * * *`)
	})
}

func TestDeleteStaleDesires(t *testing.T) {
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))

	makeDesiredAD := func(name string) *kubeapplier.ApplyDesire {
		resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplier.ApplyDesire{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(mcResourceID.String())},
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: mcResourceID,
			},
		}
	}

	t.Run("DB error on List returns error", func(t *testing.T) {
		syncer := &backupScheduleSyncer{}
		err := syncer.deleteStaleDesires(context.Background(),
			&erroringADCrud{err: fmt.Errorf("cosmos unavailable")}, nil, nil, nil)
		require.Error(t, err)
	})

	t.Run("replaces stale ApplyDesire with Delete type", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		rdCrud, _ := mockKA.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		staleAD := makeDesiredAD("backup-old")
		staleAD.Spec.TargetItem = kubeapplier.ResourceReference{
			Group: veleroScheduleGroup, Version: veleroScheduleVersion,
			Resource: veleroScheduleResource, Namespace: veleroNamespace, Name: "old-schedule",
		}
		_, _ = adCrud.Create(context.Background(), staleAD, nil)
		_, _ = adCrud.Create(context.Background(), makeDesiredAD("backup-current"), nil)

		syncer := &backupScheduleSyncer{}
		err := syncer.deleteStaleDesires(context.Background(), adCrud, rdCrud, mcResourceID,
			[]*kubeapplier.ApplyDesire{makeDesiredAD("backup-current")})
		require.NoError(t, err)

		ad, err := adCrud.Get(context.Background(), "backup-old")
		require.NoError(t, err, "stale AD should still exist but with Delete type")
		assert.Equal(t, kubeapplier.ApplyDesireTypeDelete, ad.Spec.Type)
		assert.Equal(t, "old-schedule", ad.Spec.TargetItem.Name)
		assert.Nil(t, ad.Spec.ServerSideApply)

		_, err = adCrud.Get(context.Background(), "backup-current")
		assert.NoError(t, err, "desired AD should still exist")
	})
}

func TestBackupScheduleSyncer_SyncOnce(t *testing.T) {
	const (
		testClusterID    = "11111111111111111111111111111111"
		testClusterIDStr = "/api/aro_hcp/v1alpha1/clusters/" + testClusterID
		testEnvID        = "test-env"
		testDomainPrefix = "test-domprefix"
		testStampID      = "mc1"
	)

	testBackupConfig := &BackupConfig{
		Cadence: BackupCadenceProduction,
	}

	testMgmtClusterResourceID := func() *azcorearm.ResourceID {
		return api.Must(fleet.ToManagementClusterResourceID(testStampID))
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
			CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
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

	seedSPCWithPlacement := func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
		t.Helper()
		spc, err := database.GetOrCreateServiceProviderCluster(ctx, mockDB, testKey.GetResourceID())
		require.NoError(t, err)
		spc.Status.ManagementClusterResourceID = testMgmtClusterResourceID()
		_, err = mockDB.ServiceProviderClusters(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName).Replace(ctx, spc, nil)
		require.NoError(t, err)
	}

	seedAllDesiresForConfig := func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient, config *BackupConfig) {
		t.Helper()
		hcNamespace := controllers.HostedClusterNamespace(testEnvID, testClusterID)
		hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, testDomainPrefix)
		mcResourceID := testMgmtClusterResourceID()
		configSchedules := config.Schedules()
		schedules := make([]*velerov1api.Schedule, 0, len(configSchedules))
		for _, sc := range configSchedules {
			schedules = append(schedules, NewScheduledBackup(testClusterID, testDomainPrefix, hcNamespace, hcpNamespace, sc.Name, sc.Schedule, sc.TTL, false))
		}
		ads, err := buildApplyDesiresFromSchedules(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, mcResourceID, schedules)
		require.NoError(t, err)
		adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		for _, ad := range ads {
			_, err := adCrud.Create(ctx, ad, nil)
			require.NoError(t, err)
		}
		rds, err := buildReadDesiresFromSchedules(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, mcResourceID, schedules)
		require.NoError(t, err)
		rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		for _, rd := range rds {
			_, err := rdCrud.Create(ctx, rd, nil)
			require.NoError(t, err)
		}
	}

	tests := []struct {
		name          string
		seedDB        func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		seedKA        func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient)
		backupConfig  *BackupConfig
		syncCount     int
		clusterOpts   []func(*api.HCPOpenShiftCluster)
		expectError   bool
		errorContains string
		verify        func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient)
	}{
		{
			name: "cluster not found in DB is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
			},
		},
		{
			name: "installing cluster is skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateProvisioning
				}), nil)
				require.NoError(t, err)
			},
		},
		{
			name: "creates ApplyDesires when not found",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
			},
			verify: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, sc := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, sc.Name))
					ad, err := adCrud.Get(ctx, desireName)
					require.NoError(t, err, "ApplyDesire %s should exist", desireName)
					assert.Equal(t, kubeapplier.ApplyDesireTypeServerSideApply, ad.Spec.Type)
					require.NotNil(t, ad.Spec.ServerSideApply)
					assert.NotNil(t, ad.Spec.ServerSideApply.KubeContent)
				}
			},
		},
		{
			name: "no-op when desires already exist",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
			},
			seedKA: func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedAllDesiresForConfig(t, ctx, mockKA, testBackupConfig)
			},
		},
		{
			name: "failed state cluster still gets backup",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateFailed
				}), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
			},
			verify: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, "hourly"))
				ad, err := adCrud.Get(ctx, desireName)
				require.NoError(t, err)
				assert.NotNil(t, ad)
			},
		},
		{
			name: "deletes stale desires when schedule is removed from config",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
				seedSPCWithPlacement(t, ctx, mockDB)
			},
			seedKA: func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedAllDesiresForConfig(t, ctx, mockKA, testBackupConfig)
			},
			backupConfig: &BackupConfig{
				Cadence: BackupCadenceTesting,
			},
			syncCount: 1,
			verify: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				hourlyDesireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, "hourly"))
				hourlyAD, err := adCrud.Get(ctx, hourlyDesireName)
				assert.NoError(t, err, "ApplyDesire %s should still exist", hourlyDesireName)
				assert.Equal(t, kubeapplier.ApplyDesireTypeServerSideApply, hourlyAD.Spec.Type)

				for _, name := range []string{"daily", "weekly"} {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, name))
					ad, err := adCrud.Get(ctx, desireName)
					require.NoError(t, err, "stale %s ApplyDesire should still exist with Delete type", name)
					assert.Equal(t, kubeapplier.ApplyDesireTypeDelete, ad.Spec.Type, "stale %s ApplyDesire should be Delete type", name)
					assert.Nil(t, ad.Spec.ServerSideApply, "stale %s ApplyDesire should not have ServerSideApply", name)
					_, err = rdCrud.Get(ctx, desireName)
					assert.Error(t, err, "stale %s ReadDesire should be deleted", name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockDB := databasetesting.NewMockResourcesDBClient()
			mockKA := databasetesting.NewMockKubeApplierDBClient()
			mockKAClients := databasetesting.NewMockKubeApplierDBClients()
			mockKAClients.Register(testMgmtClusterResourceID(), mockKA)

			tt.seedDB(t, ctx, mockDB)
			if tt.seedKA != nil {
				tt.seedKA(t, ctx, mockKA)
			}

			cfg := testBackupConfig
			if tt.backupConfig != nil {
				cfg = tt.backupConfig
			}

			syncer := &backupScheduleSyncer{
				cooldownChecker:                     &alwaysSyncCooldownChecker{},
				cosmosClient:                        mockDB,
				kubeApplierDBClients:                mockKAClients,
				hostedClusterNamespaceEnvIdentifier: testEnvID,
				backupConfig:                        cfg,
			}

			syncCount := max(tt.syncCount, 1)
			var err error
			for range syncCount {
				err = syncer.SyncOnce(ctx, testKey)
				if err != nil {
					break
				}
			}

			if tt.expectError {
				require.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, mockKA)
			}
		})
	}
}

func TestApplyDesireNeedsUpdate(t *testing.T) {
	makeAD := func(content string) *kubeapplier.ApplyDesire {
		return &kubeapplier.ApplyDesire{
			Spec: kubeapplier.ApplyDesireSpec{
				Type: kubeapplier.ApplyDesireTypeServerSideApply,
				ServerSideApply: &kubeapplier.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(content)},
				},
			},
		}
	}

	assert.True(t, applyDesireNeedsUpdate(nil, makeAD(`{"a":1}`)))
	assert.False(t, applyDesireNeedsUpdate(makeAD(`{"a":1}`), makeAD(`{"a":1}`)))
	assert.True(t, applyDesireNeedsUpdate(makeAD(`{"a":1}`), makeAD(`{"a":2}`)))
	assert.False(t, applyDesireNeedsUpdate(makeAD(`{"a":1,"b":2}`), makeAD(`{"b":2,"a":1}`)))
}

type erroringADCrud struct {
	database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]
	err error
}

func (e *erroringADCrud) Get(_ context.Context, _ string) (*kubeapplier.ApplyDesire, error) {
	return nil, e.err
}

func (e *erroringADCrud) List(_ context.Context, _ *database.DBClientListResourceDocsOptions) (database.DBClientIterator[kubeapplier.ApplyDesire], error) {
	return nil, e.err
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
