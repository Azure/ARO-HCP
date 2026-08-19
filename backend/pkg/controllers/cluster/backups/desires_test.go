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
)

// TestBuildApplyDesiresFromSchedules validates what an ApplyDesire built for a
// backup schedule is expected to look like: the scope-correct name, the Velero
// Schedule target, the server-side-apply type, and the marshaled Schedule as
// the apply content.
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
		veleroSchedules = append(veleroSchedules, NewScheduledBackup(clusterID, hostedClusterNamespace, controlPlaneNamespace, schedule, false))
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

// TestBuildReadDesireFromApplyDesire validates what a ReadDesire built from a
// backup-schedule ApplyDesire is expected to look like: it reuses the
// ApplyDesire's name, target, management cluster, partition key, and tags so the
// apply/read pair stays in lockstep, and it carries the cluster-scoped ReadDesire
// resource ID.
func TestBuildReadDesireFromApplyDesire(t *testing.T) {
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

	name := backupApplyDesireName("hourly")
	applyDesire := makeApplyDesire(name)

	readDesire, err := buildReadDesireFromApplyDesire("test-sub", "test-rg", "test-cluster", applyDesire)
	require.NoError(t, err)
	require.NotNil(t, readDesire)

	// The ReadDesire observes the same object the ApplyDesire lands, so it shares
	// the desire name, target, management cluster, partition key, and tags.
	assert.Equal(t, name, readDesire.ResourceID.Name)
	assert.Equal(t, applyDesire.Spec.ManagementCluster, readDesire.Spec.ManagementCluster)
	assert.Equal(t, applyDesire.Spec.TargetItem, readDesire.Spec.TargetItem)
	assert.Equal(t, applyDesire.PartitionKey, readDesire.PartitionKey)
	assert.Equal(t, applyDesire.Tags, readDesire.Tags)

	// The ReadDesire carries the cluster-scoped ReadDesire resource ID (not the
	// ApplyDesire resource type).
	wantReadDesireID := kubeapplierapi.ToClusterScopedReadDesireResourceIDString("test-sub", "test-rg", "test-cluster", name)
	assert.True(t, strings.EqualFold(wantReadDesireID, readDesire.ResourceID.String()),
		"expected ReadDesire resource ID %q, got %q", wantReadDesireID, readDesire.ResourceID.String())
}
