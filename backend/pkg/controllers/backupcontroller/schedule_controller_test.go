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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	backendlistertesting "github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	dblistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestBuildApplyDesiresFromSchedules(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	hcNamespace := controllers.HostedClusterNamespace("testenv", clusterID)
	hcpNamespace := hcNamespace + "-test-domprefix"
	keyVersion := "abc123"
	mcResourceID := api.Must(fleet.ToManagementClusterResourceID("mc1"))

	hourlySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "hourly", "0 */1 * * *", keyVersion, 48*time.Hour, false)
	dailySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "daily", "0 2 * * *", keyVersion, 336*time.Hour, false)
	weeklySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "weekly", "0 3 * * 0", keyVersion, 2160*time.Hour, false)

	schedules := []*velerov1api.Schedule{hourlySchedule, dailySchedule, weeklySchedule}
	desires, err := buildApplyDesiresFromSchedules("test-sub", "test-rg", "test-cluster", mcResourceID, schedules)
	require.NoError(t, err)
	require.Len(t, desires, 3)

	for i, s := range schedules {
		ad := desires[i]
		assert.Equal(t, backupApplyDesireName(s.Name), ad.ResourceID.Name)
		assert.Equal(t, veleroGroup, ad.Spec.TargetItem.Group)
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
	keyVersion := "abc123"

	hourlySchedule := NewScheduledBackup(clusterID, "test-domprefix", hcNamespace, hcpNamespace, "hourly", "0 */1 * * *", keyVersion, 48*time.Hour, false)

	desires, err := buildReadDesiresFromSchedules("test-sub", "test-rg", "test-cluster", mcResourceID, []*velerov1api.Schedule{hourlySchedule})
	require.NoError(t, err)
	require.Len(t, desires, 1)

	rd := desires[0]
	assert.Equal(t, backupApplyDesireName(hourlySchedule.Name), rd.ResourceID.Name)
	assert.Equal(t, veleroGroup, rd.Spec.TargetItem.Group)
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
					Group: veleroGroup, Version: veleroVersion,
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
					Group: veleroGroup, Version: veleroVersion,
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
			desiredADs:     []*kubeapplier.ApplyDesire{makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *","keyVersion":"abc123"}`)},
			desiredRDs:     []*kubeapplier.ReadDesire{makeDesiredRD(backup.BackupScheduleDesireNamePrefix + "hourly")},
			expectADExists: true,
			expectRDExists: true,
		},
		{
			name: "skips existing AD",
			seedKA: func(ka *databasetesting.MockKubeApplierDBClient) {
				crud, _ := ka.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
				_, _ = crud.Create(context.Background(), makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *","keyVersion":"abc123"}`), nil)
			},
			desiredADs:     []*kubeapplier.ApplyDesire{makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *","keyVersion":"abc123"}`)},
			desiredRDs:     []*kubeapplier.ReadDesire{makeDesiredRD(backup.BackupScheduleDesireNamePrefix + "hourly")},
			expectADExists: true,
		},
		{
			name:           "DB error on Get returns error",
			adCrudOverride: &erroringADCrud{err: fmt.Errorf("cosmos unavailable")},
			desiredADs:     []*kubeapplier.ApplyDesire{makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *","keyVersion":"abc123"}`)},
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
			// First call: creates the first missing AD.
			_, err := syncer.ensureDesireCreated(context.Background(), adCrud, rdCrud, tt.desiredADs, tt.desiredRDs)

			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.expectADExists {
				_, err := adCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
				assert.NoError(t, err, "expected AD to exist after first call")
			}
			if tt.expectRDExists {
				// RD is created on the second call (all ADs must exist first).
				_, err = syncer.ensureDesireCreated(context.Background(), adCrud, rdCrud, tt.desiredADs, tt.desiredRDs)
				require.NoError(t, err)
				_, err = rdCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
				assert.NoError(t, err, "expected RD to exist after second call")
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
					Group: veleroGroup, Version: veleroVersion,
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
		_, _ = adCrud.Create(context.Background(), makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *","keyVersion":"abc123"}`), nil)

		syncer := &backupScheduleSyncer{}
		_, err := syncer.ensureDesireUpdated(context.Background(), adCrud,
			[]*kubeapplier.ApplyDesire{makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *","keyVersion":"abc123"}`)})
		require.NoError(t, err)
	})

	t.Run("drifted content replaces", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		_, _ = adCrud.Create(context.Background(), makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */6 * * *","keyVersion":"abc123"}`), nil)

		syncer := &backupScheduleSyncer{}
		_, err := syncer.ensureDesireUpdated(context.Background(), adCrud,
			[]*kubeapplier.ApplyDesire{makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"*/5 * * * *","keyVersion":"abc123"}`)})
		require.NoError(t, err)

		ad, err := adCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
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
		_, err := syncer.deleteStaleDesires(context.Background(),
			&erroringADCrud{err: fmt.Errorf("cosmos unavailable")}, nil, nil, nil)
		require.Error(t, err)
	})

	t.Run("replaces stale ApplyDesire with Delete type and leaves RD intact", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		rdCrud, _ := mockKA.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		staleAD := makeDesiredAD(backup.BackupScheduleDesireNamePrefix + "old")
		staleAD.Spec.TargetItem = kubeapplier.ResourceReference{
			Group: veleroGroup, Version: veleroVersion,
			Resource: veleroScheduleResource, Namespace: veleroNamespace, Name: "old-schedule",
		}
		_, _ = adCrud.Create(context.Background(), staleAD, nil)
		_, _ = adCrud.Create(context.Background(), makeDesiredAD(backup.BackupScheduleDesireNamePrefix+"current"), nil)

		// Seed a RD for the stale desire — it must NOT be deleted by Case B.
		staleRDResourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", backup.BackupScheduleDesireNamePrefix+"old")
		staleRDResourceID := api.Must(azcorearm.ParseResourceID(staleRDResourceIDStr))
		staleRD := &kubeapplier.ReadDesire{CosmosMetadata: api.CosmosMetadata{ResourceID: staleRDResourceID, PartitionKey: strings.ToLower(mcResourceID.String())}}
		_, _ = rdCrud.Create(context.Background(), staleRD, nil)

		syncer := &backupScheduleSyncer{}
		_, err := syncer.deleteStaleDesires(context.Background(), adCrud, rdCrud, mcResourceID,
			[]*kubeapplier.ApplyDesire{makeDesiredAD(backup.BackupScheduleDesireNamePrefix + "current")})
		require.NoError(t, err)

		ad, err := adCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		require.NoError(t, err, "stale AD should still exist but with Delete type")
		assert.Equal(t, kubeapplier.ApplyDesireTypeDelete, ad.Spec.Type)
		assert.Equal(t, "old-schedule", ad.Spec.TargetItem.Name)
		assert.Nil(t, ad.Spec.ServerSideApply)

		_, err = adCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"current")
		assert.NoError(t, err, "desired AD should still exist")

		_, err = rdCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		assert.NoError(t, err, "stale RD must remain until Case A after kube-applier confirms deletion")
	})

	t.Run("removes Delete-type ApplyDesire and its ReadDesire when kube-applier reports Successful", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		rdCrud, _ := mockKA.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		staleAD := makeDesiredAD(backup.BackupScheduleDesireNamePrefix + "old")
		staleAD.Spec.Type = kubeapplier.ApplyDesireTypeDelete
		staleAD.Status.Conditions = []metav1.Condition{
			{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
		}
		_, _ = adCrud.Create(context.Background(), staleAD, nil)

		// Seed a RD to verify it is cleaned up in Case A alongside the AD.
		staleRDResourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", backup.BackupScheduleDesireNamePrefix+"old")
		staleRDResourceID := api.Must(azcorearm.ParseResourceID(staleRDResourceIDStr))
		staleRD := &kubeapplier.ReadDesire{CosmosMetadata: api.CosmosMetadata{ResourceID: staleRDResourceID, PartitionKey: strings.ToLower(mcResourceID.String())}}
		_, _ = rdCrud.Create(context.Background(), staleRD, nil)

		syncer := &backupScheduleSyncer{}
		_, err := syncer.deleteStaleDesires(context.Background(), adCrud, rdCrud, mcResourceID, nil)
		require.NoError(t, err)

		_, err = adCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		assert.True(t, database.IsNotFoundError(err), "successful Delete-type AD should be removed from Cosmos")

		_, err = rdCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		assert.True(t, database.IsNotFoundError(err), "RD should also be removed when AD is Successful Delete-type")
	})

	t.Run("leaves Delete-type ApplyDesire when kube-applier has not yet reported Successful", func(t *testing.T) {
		mockKA := databasetesting.NewMockKubeApplierDBClient()
		adCrud, _ := mockKA.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		rdCrud, _ := mockKA.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		staleAD := makeDesiredAD(backup.BackupScheduleDesireNamePrefix + "old")
		staleAD.Spec.Type = kubeapplier.ApplyDesireTypeDelete
		_, _ = adCrud.Create(context.Background(), staleAD, nil)

		syncer := &backupScheduleSyncer{}
		_, err := syncer.deleteStaleDesires(context.Background(), adCrud, rdCrud, mcResourceID, nil)
		require.NoError(t, err)

		ad, err := adCrud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		require.NoError(t, err, "pending Delete-type AD should remain in Cosmos")
		assert.Equal(t, kubeapplier.ApplyDesireTypeDelete, ad.Spec.Type)
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
				ProvisioningState:       arm.ProvisioningStateSucceeded,
				ClusterServiceID:        &csID,
				BillingDocumentCosmosID: "test-billing-doc-id",
			},
		}
		for _, opt := range opts {
			opt(cluster)
		}
		return cluster
	}

	newTestSPC := func() *api.ServiceProviderCluster {
		clusterResourceID := api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		spcResourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			clusterResourceID.String(), api.ServiceProviderClusterResourceTypeName, api.ServiceProviderClusterResourceName)))
		return &api.ServiceProviderCluster{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   spcResourceID,
				PartitionKey: strings.ToLower(testKey.SubscriptionID),
			},
			Status: api.ServiceProviderClusterStatus{
				ManagementClusterResourceID: testMgmtClusterResourceID(),
			},
		}
	}

	seedAllDesiresForConfig := func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient, config *BackupConfig, keyVersion string) {
		t.Helper()
		hcNamespace := controllers.HostedClusterNamespace(testEnvID, testClusterID)
		hcpNamespace := fmt.Sprintf("%s-%s", hcNamespace, testDomainPrefix)
		mcResourceID := testMgmtClusterResourceID()
		configSchedules := config.Schedules()
		schedules := make([]*velerov1api.Schedule, 0, len(configSchedules))
		for _, sc := range configSchedules {
			schedules = append(schedules, NewScheduledBackup(testClusterID, testDomainPrefix, hcNamespace, hcpNamespace, sc.Name, sc.Schedule, keyVersion, sc.TTL, false))
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

	newHostedClusterReadDesireLister := func(key controllerutils.HCPClusterKey, keyVersion string) *dblistertesting.SliceReadDesireLister {
		hc := &v1beta1.HostedCluster{
			Status: v1beta1.HostedClusterStatus{
				SecretEncryption: v1beta1.SecretEncryptionStatus{
					ActiveKey: v1beta1.SecretEncryptionKeyStatus{
						Azure: v1beta1.AzureKMSKey{KeyVersion: keyVersion},
					},
				},
			},
		}
		raw, _ := json.Marshal(hc)
		resourceID := api.Must(azcorearm.ParseResourceID(
			kubeapplier.ToClusterScopedReadDesireResourceIDString(
				key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
				maestrohelpers.ReadDesireNameReadonlyHostedCluster)))
		return &dblistertesting.SliceReadDesireLister{
			Desires: []*kubeapplier.ReadDesire{{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID:   resourceID,
					PartitionKey: strings.ToLower(resourceID.SubscriptionID),
				},
				Status: kubeapplier.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: raw},
				},
			}},
		}
	}

	tests := []struct {
		name             string
		seedDB           func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient)
		seedKA           func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient)
		readDesireLister dblisters.ReadDesireLister
		hasPlacement     bool
		backupConfig     *BackupConfig
		syncCount        int
		clusterOpts      []func(*api.HCPOpenShiftCluster)
		expectError      bool
		errorContains    string
		verify           func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient)
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
			name: "cluster marked for deletion with failed state is skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				now := metav1.Now()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = arm.ProvisioningStateFailed
					c.ServiceProviderProperties.DeletionTimestamp = &now
				}), nil)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, sc := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, sc.Name))
					_, err := adCrud.Get(ctx, desireName)
					assert.True(t, database.IsNotFoundError(err), "desire %s should not exist for cluster targeted for deletion", desireName)
				}
			},
		},
		{
			name: "creates ApplyDesires when not found",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement:     true,
			readDesireLister: newHostedClusterReadDesireLister(testKey, ""),
			// One sync per missing AD, then one sync per missing RD.
			// Production cadence has 3 schedules: 3 AD syncs + 3 RD syncs = 6.
			syncCount: 6,
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
			},
			hasPlacement:     true,
			readDesireLister: newHostedClusterReadDesireLister(testKey, ""),
			seedKA: func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedAllDesiresForConfig(t, ctx, mockKA, testBackupConfig, "")
			},
		},
		{
			name: "propagates keyVersion from HostedCluster into Schedule labels",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement:     true,
			syncCount:        6,
			readDesireLister: newHostedClusterReadDesireLister(testKey, "test-key-v1"),
			verify: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, sc := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, sc.Name))
					ad, err := adCrud.Get(ctx, desireName)
					require.NoError(t, err, "ApplyDesire %s should exist", desireName)
					require.NotNil(t, ad.Spec.ServerSideApply)
					require.NotNil(t, ad.Spec.ServerSideApply.KubeContent)

					var schedule velerov1api.Schedule
					err = json.Unmarshal(ad.Spec.ServerSideApply.KubeContent.Raw, &schedule)
					require.NoError(t, err, "failed to unmarshal Schedule from ApplyDesire %s", desireName)
					assert.Equal(t, "test-key-v1", schedule.Labels[backup.KmsKeyVersionLabel],
						"Schedule %s should have keyVersion label", desireName)
				}
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
			},
			hasPlacement:     true,
			readDesireLister: newHostedClusterReadDesireLister(testKey, ""),
			syncCount:        6,
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
			},
			hasPlacement:     true,
			readDesireLister: newHostedClusterReadDesireLister(testKey, ""),
			seedKA: func(t *testing.T, ctx context.Context, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				seedAllDesiresForConfig(t, ctx, mockKA, testBackupConfig, "")
			},
			backupConfig: &BackupConfig{
				Cadence: BackupCadenceTesting,
			},
			// Testing cadence has 1 schedule (5min); production has 3 (hourly, daily, weekly).
			// Syncs needed: 1 (create 5min AD) + 1 (create 5min RD) + 3 (mark hourly/daily/weekly Delete) = 5.
			syncCount: 5,
			verify: func(t *testing.T, ctx context.Context, _ *databasetesting.MockResourcesDBClient, mockKA *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				adCrud, err := mockKA.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				rdCrud, err := mockKA.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				fiveMinDesireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, "5min"))
				fiveMinAD, err := adCrud.Get(ctx, fiveMinDesireName)
				assert.NoError(t, err, "ApplyDesire %s should still exist", fiveMinDesireName)
				assert.Equal(t, kubeapplier.ApplyDesireTypeServerSideApply, fiveMinAD.Spec.Type)

				for _, name := range []string{"hourly", "daily", "weekly"} {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testClusterID, name))
					ad, err := adCrud.Get(ctx, desireName)
					require.NoError(t, err, "stale %s ApplyDesire should still exist with Delete type", name)
					assert.Equal(t, kubeapplier.ApplyDesireTypeDelete, ad.Spec.Type, "stale %s ApplyDesire should be Delete type", name)
					assert.Nil(t, ad.Spec.ServerSideApply, "stale %s ApplyDesire should not have ServerSideApply", name)
					// RD cleanup is deferred to Case A after kube-applier confirms deletion.
					_, err = rdCrud.Get(ctx, desireName)
					assert.NoError(t, err, "stale %s ReadDesire must still exist until kube-applier confirms deletion", name)
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

			if tt.seedDB != nil {
				tt.seedDB(t, ctx, mockDB)
			}
			if tt.seedKA != nil {
				tt.seedKA(t, ctx, mockKA)
			}

			cfg := testBackupConfig
			if tt.backupConfig != nil {
				cfg = tt.backupConfig
			}

			clusterLister := &backendlistertesting.SliceClusterLister{
				Clusters: []*api.HCPOpenShiftCluster{newTestCluster(tt.clusterOpts...)},
			}

			var spcList []*api.ServiceProviderCluster
			if tt.hasPlacement {
				spcList = []*api.ServiceProviderCluster{newTestSPC()}
			}

			var rdLister dblisters.ReadDesireLister = &dblistertesting.SliceReadDesireLister{}
			if tt.readDesireLister != nil {
				rdLister = tt.readDesireLister
			}

			syncer := &backupScheduleSyncer{
				cosmosClient:                        mockDB,
				clusterLister:                       clusterLister,
				serviceProviderClusterLister:        &backendlistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: spcList},
				applyDesireLister:                   &dblistertesting.SliceApplyDesireLister{},
				kubeApplierDBClients:                mockKAClients,
				hostedClusterNamespaceEnvIdentifier: testEnvID,
				backupConfig:                        cfg,
				readDesireLister:                    rdLister,
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
