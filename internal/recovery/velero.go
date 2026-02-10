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

package recovery

import (
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/builder"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
			object.SetLabels(map[string]string{"api.openshift.com/id": clusterId}) // associates a backup with a cluster for filtering
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

func NewScheduledBackup(backupName, hcpNamespace, hcNamespace string) *velerov1api.Schedule {
	backup := NewBackup(backupName, hcpNamespace, hcNamespace)
	schedule := builder.ForSchedule("velero", backupName).
		Template(backup.Spec)
	return schedule.Result()
}
