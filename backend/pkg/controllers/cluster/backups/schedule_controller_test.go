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
package backups

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func TestDeleteStaleApplyDesires(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	makeDesiredApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}
	}

	makeReadDesire := func(name string) *kubeapplierapi.ReadDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}
	}

	newSyncer := func(clients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) *backupScheduleSyncer {
		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementClusterResourceID}},
			},
		}
		return &backupScheduleSyncer{
			applyDesireLister: &kubeapplierlistertesting.DBApplyDesireLister{Clients: clients, Lister: mcLister},
			readDesireLister:  &kubeapplierlistertesting.DBReadDesireLister{Clients: clients, Lister: mcLister},
		}
	}

	t.Run("replaces stale ApplyDesire with Delete type", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		staleApplyDesire := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix + "old")
		staleApplyDesire.Spec.TargetItem = kubeapplierapi.ResourceReference{
			Group: backup.VeleroGroup, Version: backup.VeleroVersion,
			Resource: backup.VeleroScheduleResource, Namespace: backup.VeleroNamespace, Name: "old-schedule",
		}
		_, _ = applyDesireCRUD.Create(context.Background(), staleApplyDesire, nil)
		_, _ = applyDesireCRUD.Create(context.Background(), makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"current"), nil)
		_, _ = readDesireCRUD.Create(context.Background(), makeReadDesire(backup.BackupScheduleDesireNamePrefix+"old"), nil)

		syncer := newSyncer(mockClients)
		_, err := syncer.deleteStaleApplyDesires(context.Background(), testKey, applyDesireCRUD,
			map[string]bool{backup.BackupScheduleDesireNamePrefix + "current": true})
		require.NoError(t, err)

		applyDesire, err := applyDesireCRUD.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		require.NoError(t, err, "stale ApplyDesire should still exist but with Delete type")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, applyDesire.Spec.Type)
		assert.Equal(t, "old-schedule", applyDesire.Spec.TargetItem.Name)
		assert.Nil(t, applyDesire.Spec.ServerSideApply)

		_, err = applyDesireCRUD.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"current")
		assert.NoError(t, err, "desired ApplyDesire should still exist")
	})

	t.Run("removes Delete-type ApplyDesire once its delete has succeeded", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")

		// EnsureApplyDesireRemoved purges a Delete-type ApplyDesire once the
		// ApplyDesire's own delete condition reports success.
		staleApplyDesire := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix + "old")
		staleApplyDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		staleApplyDesire.Status.Conditions = []metav1.Condition{
			{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue},
		}
		_, _ = applyDesireCRUD.Create(context.Background(), staleApplyDesire, nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleApplyDesires(context.Background(), testKey, applyDesireCRUD, nil)
		require.NoError(t, err)
		assert.True(t, requeue)
		_, err = applyDesireCRUD.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "Delete-type ApplyDesire should be purged once its delete has succeeded")
	})

	t.Run("leaves Delete-type ApplyDesire when ReadDesire has not yet synced", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		staleApplyDesire := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix + "old")
		staleApplyDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		_, _ = applyDesireCRUD.Create(context.Background(), staleApplyDesire, nil)

		rd := makeReadDesire(backup.BackupScheduleDesireNamePrefix + "old")
		_, _ = readDesireCRUD.Create(context.Background(), rd, nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleApplyDesires(context.Background(), testKey, applyDesireCRUD, nil)
		require.NoError(t, err)
		assert.False(t, requeue)

		applyDesire, err := applyDesireCRUD.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		require.NoError(t, err, "pending Delete-type ApplyDesire should remain in Cosmos")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, applyDesire.Spec.Type)
	})

	t.Run("skips ApplyDesire with no tags", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")

		noTagDesire := makeDesiredApplyDesire("unrelated-notags")
		noTagDesire.Tags = nil
		noTagDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeServerSideApply
		_, _ = applyDesireCRUD.Create(context.Background(), noTagDesire, nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleApplyDesires(context.Background(), testKey, applyDesireCRUD, nil)
		require.NoError(t, err)
		assert.False(t, requeue)

		got, err := applyDesireCRUD.Get(context.Background(), "unrelated-notags")
		require.NoError(t, err, "ApplyDesire with no tags should be untouched")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, got.Spec.Type)
	})

	t.Run("skips ApplyDesire without schedule tag", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")

		unrelatedDesire := makeDesiredApplyDesire("unrelated-abc123")
		unrelatedDesire.Tags = map[string]string{"someothertag": ""}
		unrelatedDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeServerSideApply
		_, _ = applyDesireCRUD.Create(context.Background(), unrelatedDesire, nil)

		scheduleDesire := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix + "hourly")
		_, _ = applyDesireCRUD.Create(context.Background(), scheduleDesire, nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleApplyDesires(context.Background(), testKey, applyDesireCRUD,
			map[string]bool{backup.BackupScheduleDesireNamePrefix + "hourly": true})
		require.NoError(t, err)
		assert.False(t, requeue)

		got, err := applyDesireCRUD.Get(context.Background(), "unrelated-abc123")
		require.NoError(t, err, "ApplyDesire without schedule tag should be untouched")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, got.Spec.Type)
	})
}

func TestDeleteStaleReadDesires(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	makeDesiredReadDesire := func(name string) *kubeapplierapi.ReadDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}
	}

	newSyncer := func(clients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) *backupScheduleSyncer {
		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementClusterResourceID}},
			},
		}
		return &backupScheduleSyncer{
			applyDesireLister: &kubeapplierlistertesting.DBApplyDesireLister{Clients: clients, Lister: mcLister},
			readDesireLister:  &kubeapplierlistertesting.DBReadDesireLister{Clients: clients, Lister: mcLister},
		}
	}

	makeApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}
	}

	t.Run("deletes stale ReadDesire when no matching ApplyDesire exists", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		_, _ = readDesireCRUD.Create(context.Background(), makeDesiredReadDesire(backup.BackupScheduleDesireNamePrefix+"old"), nil)
		_, _ = readDesireCRUD.Create(context.Background(), makeDesiredReadDesire(backup.BackupScheduleDesireNamePrefix+"current"), nil)
		_, _ = applyDesireCRUD.Create(context.Background(), makeApplyDesire(backup.BackupScheduleDesireNamePrefix+"current"), nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleReadDesires(context.Background(), testKey, readDesireCRUD)
		require.NoError(t, err)
		assert.True(t, requeue)

		_, err = readDesireCRUD.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"old")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "stale ReadDesire should be removed")
		_, err = readDesireCRUD.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"current")
		assert.NoError(t, err, "ReadDesire with matching ApplyDesire should still exist")
	})

	t.Run("keeps ReadDesire when matching ApplyDesire still exists", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		desireName := backup.BackupScheduleDesireNamePrefix + "old"
		adResourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", desireName)
		adResourceID := metadataapi.Must(azcorearm.ParseResourceID(adResourceIDStr))
		_, _ = applyDesireCRUD.Create(context.Background(), &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: adResourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeDelete,
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}, nil)
		_, _ = readDesireCRUD.Create(context.Background(), makeDesiredReadDesire(desireName), nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleReadDesires(context.Background(), testKey, readDesireCRUD)
		require.NoError(t, err)
		assert.False(t, requeue)

		_, err = readDesireCRUD.Get(context.Background(), desireName)
		assert.NoError(t, err, "ReadDesire should remain while its ApplyDesire is mid-delete")
	})

	t.Run("no-op when all ReadDesires have matching ApplyDesires", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		applyDesireCRUD, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		_, _ = readDesireCRUD.Create(context.Background(), makeDesiredReadDesire(backup.BackupScheduleDesireNamePrefix+"hourly"), nil)
		_, _ = applyDesireCRUD.Create(context.Background(), makeApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly"), nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleReadDesires(context.Background(), testKey, readDesireCRUD)
		require.NoError(t, err)
		assert.False(t, requeue)
	})

	t.Run("skips ReadDesire with no tags", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		noTagReadDesire := makeDesiredReadDesire("unrelated-notags")
		noTagReadDesire.Tags = nil
		_, _ = readDesireCRUD.Create(context.Background(), noTagReadDesire, nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleReadDesires(context.Background(), testKey, readDesireCRUD)
		require.NoError(t, err)
		assert.False(t, requeue)

		_, err = readDesireCRUD.Get(context.Background(), "unrelated-notags")
		assert.NoError(t, err, "ReadDesire with no tags should be untouched")
	})

	t.Run("skips ReadDesire without schedule tag", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(managementClusterResourceID, mockKubeApplier)

		readDesireCRUD, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		unrelatedReadDesire := makeDesiredReadDesire("unrelated-abc123")
		unrelatedReadDesire.Tags = map[string]string{"someothertag": ""}
		_, _ = readDesireCRUD.Create(context.Background(), unrelatedReadDesire, nil)

		syncer := newSyncer(mockClients)
		requeue, err := syncer.deleteStaleReadDesires(context.Background(), testKey, readDesireCRUD)
		require.NoError(t, err)
		assert.False(t, requeue)

		_, err = readDesireCRUD.Get(context.Background(), "unrelated-abc123")
		assert.NoError(t, err, "ReadDesire without schedule tag should be untouched")
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
		BackupCadenceProfile: BackupCadenceProduction,
	}

	testMgmtClusterResourceID := func() *azcorearm.ResourceID {
		return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampID))
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	hostedClusterNamespace := controllerutils.HostedClusterNamespace(testEnvID, testClusterID)
	testArmResourceIDStr := "/subscriptions/" + testKey.SubscriptionID +
		"/resourceGroups/" + testKey.ResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName
	testSchedulePrefix := hostedClusterNamespace

	newTestCluster := func(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		csID := metadataapi.Must(metadataapi.NewInternalID(testClusterIDStr))
		cluster := &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: resourceID},
			},
			CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
				DNS: coreapi.CustomerDNSProfile{
					BaseDomainPrefix: testDomainPrefix,
				},
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState:       coreapi.ProvisioningStateSucceeded,
				ClusterServiceID:        &csID,
				BillingDocumentCosmosID: "test-billing-doc-id",
			},
		}
		for _, opt := range opts {
			opt(cluster)
		}
		return cluster
	}

	newTestServiceProviderCluster := func() *coreapi.ServiceProviderCluster {
		clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testKey.SubscriptionID +
				"/resourceGroups/" + testKey.ResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testKey.HCPClusterName,
		))
		serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			clusterResourceID.String(), coreapi.ServiceProviderClusterResourceTypeName, coreapi.ServiceProviderClusterResourceName)))
		controlPlaneNamespace := fmt.Sprintf("%s-%s", hostedClusterNamespace, testDomainPrefix)
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   serviceProviderClusterResourceID,
				PartitionKey: strings.ToLower(testKey.SubscriptionID),
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ManagementClusterResourceID: testMgmtClusterResourceID(),
				HostedClusterNamespace:      hostedClusterNamespace,
				ControlPlaneNamespace:       controlPlaneNamespace,
			},
		}
	}

	seedAllDesiresForConfig := func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, config *BackupConfig) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
		t.Helper()
		controlPlaneNamespace := fmt.Sprintf("%s-%s", hostedClusterNamespace, testDomainPrefix)
		managementClusterResourceID := testMgmtClusterResourceID()
		configSchedules := config.Schedules()
		schedules := make([]*velerov1api.Schedule, 0, len(configSchedules))
		for _, scheduleConfig := range configSchedules {
			schedules = append(schedules, NewScheduledBackup(testArmResourceIDStr, hostedClusterNamespace, controlPlaneNamespace, scheduleConfig, false))
		}
		applyDesires, err := buildApplyDesiresFromSchedules(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, managementClusterResourceID, schedules)
		require.NoError(t, err)
		applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		for _, applyDesire := range applyDesires {
			_, err := applyDesireCRUD.Create(ctx, applyDesire, nil)
			require.NoError(t, err)
		}
		readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
		require.NoError(t, err)
		var readDesires []*kubeapplierapi.ReadDesire
		for _, applyDesire := range applyDesires {
			rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
				testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, applyDesire.ResourceID.Name,
			)
			rdResourceID := metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr))
			readDesire := &kubeapplierapi.ReadDesire{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rdResourceID, PartitionKey: applyDesire.PartitionKey},
				Spec: kubeapplierapi.ReadDesireSpec{
					ManagementCluster: applyDesire.Spec.ManagementCluster,
					TargetItem:        applyDesire.Spec.TargetItem,
				},
				Tags: applyDesire.Tags,
			}
			_, err := readDesireCRUD.Create(ctx, readDesire, nil)
			require.NoError(t, err)
			readDesires = append(readDesires, readDesire)
		}
		return applyDesires, readDesires
	}

	tests := []struct {
		name            string
		seedDB          func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
		seedKubeApplier func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire)
		afterSync       func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
		hasPlacement    bool
		backupConfig    *BackupConfig
		syncCount       int
		clusterOpts     []func(*coreapi.HCPOpenShiftCluster)
		expectError     bool
		errorContains   string
		verify          func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
	}{
		{
			name: "cluster not found in DB is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
			},
		},
		{
			name: "installing cluster is skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateProvisioning
				}), nil)
				require.NoError(t, err)
			},
		},
		{
			name: "cluster without billing doc is skipped via needsWork",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
			},
			clusterOpts: []func(*coreapi.HCPOpenShiftCluster){func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.BillingDocumentCosmosID = ""
			}},
			hasPlacement: true,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, scheduleConfig := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, scheduleConfig.Name))
					_, err := applyDesireCRUD.Get(ctx, desireName)
					assert.True(t, cosmosstorageutils.IsNotFoundError(err), "desire %s should not exist when BillingDocumentCosmosID is empty", desireName)
				}
			},
		},
		{
			name: "cluster marked for deletion with failed state is skipped",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				now := metav1.Now()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateFailed
					c.ServiceProviderProperties.DeletionTimestamp = &now
				}), nil)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, scheduleConfig := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, scheduleConfig.Name))
					_, err := applyDesireCRUD.Get(ctx, desireName)
					assert.True(t, cosmosstorageutils.IsNotFoundError(err), "desire %s should not exist for cluster targeted for deletion", desireName)
				}
			},
		},
		{
			name: "creates ApplyDesires when not found",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			// 3 production schedules × 2 desires each (ApplyDesire + ReadDesire),
			// one mutation per reconcile = 6 syncs.
			syncCount: 6,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, scheduleConfig := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, scheduleConfig.Name))
					applyDesire, err := applyDesireCRUD.Get(ctx, desireName)
					require.NoError(t, err, "ApplyDesire %s should exist", desireName)
					assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, applyDesire.Spec.Type)
					require.NotNil(t, applyDesire.Spec.ServerSideApply)
					assert.NotNil(t, applyDesire.Spec.ServerSideApply.KubeContent)
				}
			},
		},
		{
			name: "no-op when desires already exist",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
				t.Helper()
				return seedAllDesiresForConfig(t, ctx, mockKubeApplier, testBackupConfig)
			},
		},
		{
			name: "deletes stale desires when schedule is removed from config",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
				t.Helper()
				return seedAllDesiresForConfig(t, ctx, mockKubeApplier, testBackupConfig)
			},
			backupConfig: &BackupConfig{
				BackupCadenceProfile: BackupCadenceTesting,
			},
			// Testing cadence has 1 schedule (10min); production has 3 (hourly, daily, weekly).
			// 2 syncs to create the 10min pair (one create per reconcile), then a
			// single sync flips all three stale desires (hourly/daily/weekly) to
			// Delete in one pass via EnsureApplyDesireRemoved = 3.
			syncCount: 3,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				tenMinDesireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, "10min"))
				tenMinApplyDesire, err := applyDesireCRUD.Get(ctx, tenMinDesireName)
				assert.NoError(t, err, "ApplyDesire %s should still exist", tenMinDesireName)
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, tenMinApplyDesire.Spec.Type)

				for _, name := range []string{"hourly", "daily", "weekly"} {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, name))
					applyDesire, err := applyDesireCRUD.Get(ctx, desireName)
					require.NoError(t, err, "stale %s ApplyDesire should still exist with Delete type", name)
					assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, applyDesire.Spec.Type, "stale %s ApplyDesire should be Delete type", name)
					assert.Nil(t, applyDesire.Spec.ServerSideApply, "stale %s ApplyDesire should not have ServerSideApply", name)
					// ReadDesire cleanup is deferred to Case A after kube-applier confirms deletion.
					_, err = readDesireCRUD.Get(ctx, desireName)
					assert.NoError(t, err, "stale %s ReadDesire must still exist until kube-applier confirms deletion", name)
				}
			},
		},
		{
			name: "cluster marked for deletion converts ApplyDesires to Delete type",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				now := metav1.Now()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &now
				}), nil)
				require.NoError(t, err)
			},
			clusterOpts: []func(*coreapi.HCPOpenShiftCluster){func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
				t.Helper()
				return seedAllDesiresForConfig(t, ctx, mockKubeApplier, testBackupConfig)
			},
			afterSync: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				iter, err := applyDesireCRUD.List(ctx, nil)
				require.NoError(t, err)
				for _, ad := range iter.Items(ctx) {
					if ad.Spec.Type != kubeapplierapi.ApplyDesireTypeDelete {
						continue
					}
					// Simulate kube-applier confirming the delete succeeded.
					// EnsureApplyDesireRemoved gates the purge on the ApplyDesire's
					// own delete condition, so set it here.
					current, err := applyDesireCRUD.Get(ctx, ad.ResourceID.Name)
					if err != nil {
						continue
					}
					current.Status.Conditions = []metav1.Condition{
						{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue},
					}
					_, err = applyDesireCRUD.Replace(ctx, current, nil)
					require.NoError(t, err)
				}
			},
			// 3 schedules × 2 syncs each (convert to Delete + purge after
			// kube-applier confirms via afterSync) = 6.
			syncCount: 6,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				for _, name := range []string{"hourly", "daily", "weekly"} {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, name))
					_, err := applyDesireCRUD.Get(ctx, desireName)
					assert.True(t, cosmosstorageutils.IsNotFoundError(err), "ApplyDesire %s should be purged after full deletion lifecycle", desireName)
				}
			},
		},
		{
			name: "cluster marked for deletion purges successful Delete-type desires",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				now := metav1.Now()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &now
				}), nil)
				require.NoError(t, err)
			},
			clusterOpts: []func(*coreapi.HCPOpenShiftCluster){func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
				t.Helper()
				managementClusterResourceID := testMgmtClusterResourceID()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				var applyDesires []*kubeapplierapi.ApplyDesire
				var readDesires []*kubeapplierapi.ReadDesire
				for _, name := range []string{"hourly", "daily", "weekly"} {
					scheduleName := fmt.Sprintf("%s-%s", testSchedulePrefix, name)
					desireName := backupApplyDesireName(scheduleName)
					resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, desireName)
					resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
					applyDesire := &kubeapplierapi.ApplyDesire{
						CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
						Spec: kubeapplierapi.ApplyDesireSpec{
							ManagementCluster: managementClusterResourceID,
							Type:              kubeapplierapi.ApplyDesireTypeDelete,
							TargetItem: kubeapplierapi.ResourceReference{
								Group: "velero.io", Version: "v1", Resource: "schedules",
								Namespace: "velero", Name: scheduleName,
							},
						},
						Tags: map[string]string{backup.DesireTagKeySchedule: ""},
					}
					applyDesire.Status.Conditions = []metav1.Condition{
						{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
					}
					created, err := applyDesireCRUD.Create(ctx, applyDesire, nil)
					require.NoError(t, err)
					applyDesires = append(applyDesires, created)

					readDesireResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, desireName)
					readDesireResourceID := metadataapi.Must(azcorearm.ParseResourceID(readDesireResourceIDStr))
					readDesire := &kubeapplierapi.ReadDesire{
						CosmosMetadata: coreapi.CosmosMetadata{ResourceID: readDesireResourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
						Spec: kubeapplierapi.ReadDesireSpec{
							ManagementCluster: managementClusterResourceID,
							TargetItem: kubeapplierapi.ResourceReference{
								Group: "velero.io", Version: "v1", Resource: "schedules",
								Namespace: "velero", Name: scheduleName,
							},
						},
						Status: kubeapplierapi.ReadDesireStatus{
							Conditions: []metav1.Condition{
								{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue},
							},
						},
						Tags: map[string]string{backup.DesireTagKeySchedule: ""},
					}
					created2, err := readDesireCRUD.Create(ctx, readDesire, nil)
					require.NoError(t, err)
					readDesires = append(readDesires, created2)
				}
				return applyDesires, readDesires
			},
			// 3 successful Delete-type ApplyDesires to purge (one per sync) + 3 ReadDesires to delete (one per sync) = 6
			syncCount: 6,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				for _, name := range []string{"hourly", "daily", "weekly"} {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, name))
					_, err := applyDesireCRUD.Get(ctx, desireName)
					assert.True(t, cosmosstorageutils.IsNotFoundError(err), "successful Delete-type ApplyDesire %s should be purged", desireName)
					_, err = readDesireCRUD.Get(ctx, desireName)
					assert.True(t, cosmosstorageutils.IsNotFoundError(err), "ReadDesire %s should be deleted", desireName)
				}
			},
		},
		{
			name: "cluster marked for deletion without ManagementClusterResourceID is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				now := metav1.Now()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &now
				}), nil)
				require.NoError(t, err)
			},
			clusterOpts: []func(*coreapi.HCPOpenShiftCluster){func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}},
			// hasPlacement false — no ServiceProviderCluster, so ManagementClusterResourceID is nil
		},
		{
			name: "cluster marked for deletion with no existing desires is no-op",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				now := metav1.Now()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &now
				}), nil)
				require.NoError(t, err)
			},
			clusterOpts: []func(*coreapi.HCPOpenShiftCluster){func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}},
			hasPlacement: true,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, scheduleConfig := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, scheduleConfig.Name))
					_, err := applyDesireCRUD.Get(ctx, desireName)
					assert.True(t, cosmosstorageutils.IsNotFoundError(err), "no desires should exist for cluster being deleted with no pre-existing desires")
				}
			},
		},
		{
			name: "upgrades testing desires to production when cadence changes",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
				t.Helper()
				return seedAllDesiresForConfig(t, ctx, mockKubeApplier, &BackupConfig{BackupCadenceProfile: BackupCadenceTesting})
			},
			backupConfig: &BackupConfig{
				BackupCadenceProfile: BackupCadenceProduction,
			},
			// One mutation per reconcile: 6 syncs to create 3 production pairs +
			// 1 sync to mark stale 10min as Delete = 7.
			syncCount: 7,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				prodConfig := &BackupConfig{BackupCadenceProfile: BackupCadenceProduction}
				for _, scheduleConfig := range prodConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, scheduleConfig.Name))
					applyDesire, err := applyDesireCRUD.Get(ctx, desireName)
					require.NoError(t, err, "production ApplyDesire %s should exist", desireName)
					assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, applyDesire.Spec.Type)
					require.NotNil(t, applyDesire.Spec.ServerSideApply)

					var got velerov1api.Schedule
					require.NoError(t, json.Unmarshal(applyDesire.Spec.ServerSideApply.KubeContent.Raw, &got))
					assert.Equal(t, scheduleConfig.Schedule, got.Spec.Schedule, "production %s should have correct cron", scheduleConfig.Name)
					assert.Equal(t, metav1.Duration{Duration: scheduleConfig.TTL}, got.Spec.Template.TTL, "production %s should have correct TTL", scheduleConfig.Name)

					_, err = readDesireCRUD.Get(ctx, desireName)
					assert.NoError(t, err, "production ReadDesire %s should exist", desireName)
				}

				tenMinDesireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, "10min"))
				tenMinApplyDesire, err := applyDesireCRUD.Get(ctx, tenMinDesireName)
				require.NoError(t, err, "stale 10min ApplyDesire should still exist with Delete type")
				assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, tenMinApplyDesire.Spec.Type, "stale 10min ApplyDesire should be Delete type")
				assert.Nil(t, tenMinApplyDesire.Spec.ServerSideApply, "stale 10min ApplyDesire should not have ServerSideApply")
				_, err = readDesireCRUD.Get(ctx, tenMinDesireName)
				assert.NoError(t, err, "stale 10min ReadDesire must still exist until kube-applier confirms deletion")
			},
		},
		{
			name: "updates existing production desires when config parameters change",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				t.Helper()
				_, err := mockDB.HCPClusters(testKey.SubscriptionID, testKey.ResourceGroupName).Create(ctx, newTestCluster(), nil)
				require.NoError(t, err)
			},
			hasPlacement: true,
			seedKubeApplier: func(t *testing.T, ctx context.Context, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire) {
				t.Helper()
				hostedClusterNamespace := controllerutils.HostedClusterNamespace(testEnvID, testClusterID)
				controlPlaneNamespace := fmt.Sprintf("%s-%s", hostedClusterNamespace, testDomainPrefix)
				managementClusterResourceID := testMgmtClusterResourceID()

				backupConfig := BackupConfig{
					BackupScheduleState:  coreapi.BackupScheduleStateEnabled,
					BackupCadenceProfile: BackupCadenceProduction,
				}
				schedules := backupConfig.Schedules()

				oldSchedules := []*velerov1api.Schedule{}
				for _, schedule := range schedules {
					oldSchedules = append(oldSchedules, NewScheduledBackup(testArmResourceIDStr, hostedClusterNamespace, controlPlaneNamespace, schedule, false))
				}

				applyDesires, err := buildApplyDesiresFromSchedules(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, managementClusterResourceID, oldSchedules)
				require.NoError(t, err)
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				for _, applyDesire := range applyDesires {
					_, err := applyDesireCRUD.Create(ctx, applyDesire, nil)
					require.NoError(t, err)
				}

				readDesireCRUD, err := mockKubeApplier.ReadDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)
				var readDesires []*kubeapplierapi.ReadDesire
				for _, applyDesire := range applyDesires {
					rdResourceIDStr := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
						testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName, applyDesire.ResourceID.Name,
					)
					rdResourceID := metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr))
					readDesire := &kubeapplierapi.ReadDesire{
						CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rdResourceID, PartitionKey: applyDesire.PartitionKey},
						Spec: kubeapplierapi.ReadDesireSpec{
							ManagementCluster: applyDesire.Spec.ManagementCluster,
							TargetItem:        applyDesire.Spec.TargetItem,
						},
						Tags: applyDesire.Tags,
					}
					_, err := readDesireCRUD.Create(ctx, readDesire, nil)
					require.NoError(t, err)
					readDesires = append(readDesires, readDesire)
				}
				return applyDesires, readDesires
			},
			syncCount: 1,
			verify: func(t *testing.T, ctx context.Context, _ *corecosmosstoragetesting.MockResourcesDBClient, mockKubeApplier *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				applyDesireCRUD, err := mockKubeApplier.ApplyDesiresForCluster(testKey.SubscriptionID, testKey.ResourceGroupName, testKey.HCPClusterName)
				require.NoError(t, err)

				for _, scheduleConfig := range testBackupConfig.Schedules() {
					desireName := backupApplyDesireName(fmt.Sprintf("%s-%s", testSchedulePrefix, scheduleConfig.Name))
					applyDesire, err := applyDesireCRUD.Get(ctx, desireName)
					require.NoError(t, err, "ApplyDesire %s should exist", desireName)
					assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, applyDesire.Spec.Type)
					require.NotNil(t, applyDesire.Spec.ServerSideApply)

					var got velerov1api.Schedule
					require.NoError(t, json.Unmarshal(applyDesire.Spec.ServerSideApply.KubeContent.Raw, &got))
					assert.Equal(t, scheduleConfig.Schedule, got.Spec.Schedule, "%s should have updated cron", scheduleConfig.Name)
					assert.Equal(t, metav1.Duration{Duration: scheduleConfig.TTL}, got.Spec.Template.TTL, "%s should have updated TTL", scheduleConfig.Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			mockKubeApplierDBClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierDBClients.Register(testMgmtClusterResourceID(), mockKubeApplierDBClient)

			tt.seedDB(t, ctx, mockResourcesDBClient)
			if tt.seedKubeApplier != nil {
				_, _ = tt.seedKubeApplier(t, ctx, mockKubeApplierDBClient)
			}

			cfg := testBackupConfig
			if tt.backupConfig != nil {
				cfg = tt.backupConfig
			}

			clusterLister := &corelistertesting.SliceClusterLister{
				Clusters: []*coreapi.HCPOpenShiftCluster{newTestCluster(tt.clusterOpts...)},
			}

			var serviceProviderClusterList []*coreapi.ServiceProviderCluster
			if tt.hasPlacement {
				serviceProviderClusterList = []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster()}
			}

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testMgmtClusterResourceID()}},
				},
			}

			syncer := &backupScheduleSyncer{
				cosmosClient:                        mockResourcesDBClient,
				clusterLister:                       clusterLister,
				serviceProviderClusterLister:        &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: serviceProviderClusterList},
				kubeApplierDBClients:                mockKubeApplierDBClients,
				applyDesireLister:                   &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
				readDesireLister:                    &kubeapplierlistertesting.DBReadDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
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
				if tt.afterSync != nil {
					tt.afterSync(t, ctx, mockKubeApplierDBClient)
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
				tt.verify(t, ctx, mockResourcesDBClient, mockKubeApplierDBClient)
			}
		})
	}
}
