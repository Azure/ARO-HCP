// Copyright 2025 Microsoft Corporation
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

package backupcontroller

import (
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/builder"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var restoreExcludedResources = []string{
	"nodes",
	"events",
	"events.events.k8s.io",
	"backups.velero.io",
	"restores.velero.io",
	"resticrepositories.velero.io",
}

func NewRestore(restoreName, backupName string) *velerov1api.Restore {
	restore := builder.ForRestore("velero", restoreName).
		RestorePVs(true).
		Backup(backupName).
		ExistingResourcePolicy("update").
		ExcludedResources(restoreExcludedResources...).
		ItemOperationTimeout(4 * time.Hour)
	return restore.Result()
}

var backupIncludedResources = []string{
	"sa",
	"role",
	"rolebinding",
	"pod",
	"pvc",
	"pv",
	"configmap",
	"priorityclasses",
	"pdb",
	"hostedcluster",
	"nodepool",
	"secrets",
	"secretproviderclass",
	"services",
	"deployments",
	"statefulsets",
	"hostedcontrolplane",
	"cluster",
	"azurecluster",
	"azuremachinetemplate",
	"azuremachine",
	"machinedeployment",
	"machineset",
	"machine",
	"route",
	"clusterdeployment",
}

func NewBackup(backupName, clusterId string, namespaces ...string) *velerov1api.Backup {
	backup := builder.ForBackup("velero", backupName).
		StorageLocation("default").
		ObjectMeta(func(object metav1.Object) {
			object.SetLabels(map[string]string{"api.openshift.com/id": clusterId})
		}).
		IncludedNamespaces(namespaces...).
		IncludedResources(backupIncludedResources...).
		TTL(7 * 24 * time.Hour).
		SnapshotVolumes(true).
		DefaultVolumesToFsBackup(false).
		DataMover("velero").
		SnapshotMoveData(true)
	return backup.Result()
}

func NewScheduledBackup(clusterID, clusterName, hcNamespace, hcpNamespace string) *velerov1api.Schedule {
	scheduleName := ScheduleNameForCluster(clusterID)
	backup := NewBackup(scheduleName, clusterID, hcNamespace, hcpNamespace)
	schedule := builder.ForSchedule("velero", scheduleName).
		CronSchedule("0 */1 * * *").
		Template(backup.Spec).
		ObjectMeta(func(object metav1.Object) {
			object.SetLabels(map[string]string{
				"velero.io/storage-location":                       "default",
				"hypershift.openshift.io/hosted-cluster":           clusterName,
				"hypershift.openshift.io/hosted-cluster-namespace": hcNamespace,
				"api.openshift.com/id":                             clusterID,
			})
		})
	return schedule.Result()
}
