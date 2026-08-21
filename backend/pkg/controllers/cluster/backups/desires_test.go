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

package backups

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/backup"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

func TestBuildApplyDesiresFromSchedules(t *testing.T) {
	clusterID := "11111111111111111111111111111111"
	hostedClusterNamespace := controllerutils.HostedClusterNamespace("testenv", clusterID)
	controlPlaneNamespace := hostedClusterNamespace + "-test-domprefix"
	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))
	backupConfig := BackupConfig{
		BackupScheduleState:  coreapi.BackupScheduleStateEnabled,
		BackupCadenceProfile: BackupCadenceProduction,
	}
	schedules := backupConfig.Schedules()

	veleroSchedules := []*velerov1api.Schedule{}
	for _, schedule := range schedules {
		veleroSchedules = append(veleroSchedules, NewScheduledBackup(clusterID, "", hostedClusterNamespace, controlPlaneNamespace, schedule, false))
	}
	desires, err := buildApplyDesiresFromSchedules("test-sub", "test-rg", "test-cluster", managementClusterResourceID, veleroSchedules)
	require.NoError(t, err)
	require.Len(t, desires, 3)

	for i, schedule := range veleroSchedules {
		applyDesire := desires[i]

		assert.Equal(t, backupApplyDesireName(schedule.Name), applyDesire.ResourceID.Name)
		assert.Equal(t, backup.VeleroGroup, applyDesire.Spec.TargetItem.Group)
		assert.Equal(t, backup.VeleroScheduleResource, applyDesire.Spec.TargetItem.Resource)
		assert.Equal(t, backup.VeleroNamespace, applyDesire.Spec.TargetItem.Namespace)
		assert.Equal(t, schedule.Name, applyDesire.Spec.TargetItem.Name)
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, applyDesire.Spec.Type)
		require.NotNil(t, applyDesire.Spec.ServerSideApply)
		assert.NotNil(t, applyDesire.Spec.ServerSideApply.KubeContent)

		var got velerov1api.Schedule
		require.NoError(t, json.Unmarshal(applyDesire.Spec.ServerSideApply.KubeContent.Raw, &got))
		assert.Equal(t, schedule.Name, got.Name)
		assert.Equal(t, schedule.Namespace, got.Namespace)
		assert.Equal(t, schedule.Spec, got.Spec)
	}
}

func TestEnsureApplyDesire(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	makeDesiredApplyDesire := func(name string, content string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplierapi.ResourceReference{
					Group: backup.VeleroGroup, Version: backup.VeleroVersion,
					Resource: backup.VeleroScheduleResource, Namespace: backup.VeleroNamespace, Name: name,
				},
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(content)},
				},
			},
		}
	}

	t.Run("creates missing ApplyDesire", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		lister := &kubeapplierlistertesting.SliceApplyDesireLister{}

		desired := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *"}`)
		requeue, err := ensureApplyDesire(context.Background(), lister, crud, "test-sub", "test-rg", "test-cluster", desired)
		require.NoError(t, err)
		assert.True(t, requeue, "expected requeue=true on create")

		got, err := crud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
		require.NoError(t, err, "expected ApplyDesire to exist after create")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, got.Spec.Type)
	})

	t.Run("no-op when matching ApplyDesire exists", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		existing := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *"}`)
		_, _ = crud.Create(context.Background(), existing, nil)
		lister := &kubeapplierlistertesting.SliceApplyDesireLister{Desires: []*kubeapplierapi.ApplyDesire{existing}}

		desired := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *"}`)
		requeue, err := ensureApplyDesire(context.Background(), lister, crud, "test-sub", "test-rg", "test-cluster", desired)
		require.NoError(t, err)
		assert.False(t, requeue, "expected requeue=false on no-op")
	})

	t.Run("replaces when content drifts", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		existing := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */6 * * *"}`)
		created, createErr := crud.Create(context.Background(), existing, nil)
		require.NoError(t, createErr)
		lister := &kubeapplierlistertesting.SliceApplyDesireLister{Desires: []*kubeapplierapi.ApplyDesire{created}}

		desired := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"*/5 * * * *"}`)
		requeue, err := ensureApplyDesire(context.Background(), lister, crud, "test-sub", "test-rg", "test-cluster", desired)
		require.NoError(t, err)
		assert.True(t, requeue, "expected requeue=true on replace")

		got, err := crud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
		require.NoError(t, err)
		assert.Contains(t, string(got.Spec.ServerSideApply.KubeContent.Raw), `*/5 * * * *`)
	})

	t.Run("replaces delete-type with ServerSideApply-type", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		ssaApplyDesire := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *"}`)
		existingDeleteDesire := ssaApplyDesire.DeepCopy()
		existingDeleteDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		existingDeleteDesire.Spec.ServerSideApply = nil
		createdDeleteApplyDesire, createErr := crud.Create(context.Background(), existingDeleteDesire, nil)
		require.NoError(t, createErr)
		lister := &kubeapplierlistertesting.SliceApplyDesireLister{Desires: []*kubeapplierapi.ApplyDesire{createdDeleteApplyDesire}}

		desired := makeDesiredApplyDesire(backup.BackupScheduleDesireNamePrefix+"hourly", `{"schedule":"0 */1 * * *"}`)
		requeue, err := ensureApplyDesire(context.Background(), lister, crud, "test-sub", "test-rg", "test-cluster", desired)
		require.NoError(t, err)
		assert.True(t, requeue, "expected requeue=true on replace")

		got, err := crud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
		require.NoError(t, err)
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, got.Spec.Type)
		require.NotNil(t, got.Spec.ServerSideApply)
		assert.Contains(t, string(got.Spec.ServerSideApply.KubeContent.Raw), `0 */1 * * *`)
	})
}

func TestEnsureReadDesireFromApplyDesire(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	makeApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplierapi.ResourceReference{
					Group: backup.VeleroGroup, Version: backup.VeleroVersion,
					Resource: backup.VeleroScheduleResource, Namespace: backup.VeleroNamespace, Name: name,
				},
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{"schedule":"0 */1 * * *"}`)},
				},
			},
			Tags: map[string]string{backup.DesireTagKeySchedule: ""},
		}
	}

	t.Run("creates missing ReadDesire from ApplyDesire", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")
		lister := &kubeapplierlistertesting.SliceReadDesireLister{}

		applyDesire := makeApplyDesire(backup.BackupScheduleDesireNamePrefix + "hourly")
		requeue, err := ensureReadDesireFromApplyDesire(context.Background(), lister, crud, "test-sub", "test-rg", "test-cluster", applyDesire)
		require.NoError(t, err)
		assert.True(t, requeue, "expected requeue=true on create")

		got, err := crud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
		require.NoError(t, err, "expected ReadDesire to exist after create")
		assert.Equal(t, applyDesire.Spec.TargetItem, got.Spec.TargetItem)
		assert.Equal(t, applyDesire.Spec.ManagementCluster, got.Spec.ManagementCluster)
	})

	t.Run("no-op when matching ReadDesire exists", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ReadDesiresForCluster("test-sub", "test-rg", "test-cluster")

		applyDesire := makeApplyDesire(backup.BackupScheduleDesireNamePrefix + "hourly")
		_, err := ensureReadDesireFromApplyDesire(context.Background(), &kubeapplierlistertesting.SliceReadDesireLister{}, crud, "test-sub", "test-rg", "test-cluster", applyDesire)
		require.NoError(t, err)

		existing, err := crud.Get(context.Background(), backup.BackupScheduleDesireNamePrefix+"hourly")
		require.NoError(t, err)
		lister := &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{existing}}

		requeue, err := ensureReadDesireFromApplyDesire(context.Background(), lister, crud, "test-sub", "test-rg", "test-cluster", applyDesire)
		require.NoError(t, err)
		assert.False(t, requeue, "expected requeue=false on no-op")
	})
}

// TestPurgeApplyDesire covers the one-shot retirement path used for on-demand
// key-rotation backups: purgeApplyDesire must delete the ApplyDesire document
// directly (so the kube-applier stops reconciling without deleting the applied
// Velero Backup), rather than converting to Type=Delete which would tear the
// target down.
func TestPurgeApplyDesire(t *testing.T) {
	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID("mc1"))

	makeOnDemandApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplierapi.ResourceReference{
					Group: backup.VeleroGroup, Version: backup.VeleroVersion,
					Resource: backup.VeleroBackupResource, Namespace: backup.VeleroNamespace, Name: name,
				},
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{"kind":"Backup"}`)},
				},
			},
			Tags: map[string]string{backup.DesireTagKeyOndemandBackup: ""},
		}
	}

	t.Run("removes the document instead of converting it to Delete", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		desire := makeOnDemandApplyDesire("ondemand-backup")
		created, err := crud.Create(context.Background(), desire, nil)
		require.NoError(t, err)

		err = purgeApplyDesire(context.Background(), *created, crud)
		require.NoError(t, err)

		_, err = crud.Get(context.Background(), "ondemand-backup")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err),
			"purgeApplyDesire should delete the ApplyDesire document, never leave a Type=Delete desire that would delete the Velero Backup")
	})

	t.Run("is idempotent when the document is already gone", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster("test-sub", "test-rg", "test-cluster")
		desire := makeOnDemandApplyDesire("ondemand-backup")

		// The document was never created; purge must tolerate NotFound and stay a no-op.
		err := purgeApplyDesire(context.Background(), *desire, crud)
		require.NoError(t, err, "purging an already-absent ApplyDesire should not error")
	})
}
