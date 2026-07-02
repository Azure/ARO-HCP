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

package backup

import (
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/builder"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	BackupDesireNamePrefix   = "backup-"
	OndemandDesireNamePrefix = "ondemand-"
)

var backupIncludedResources = []string{
	"sa",
	"role",
	"rolebinding",
	"pod",
	"pvc", // Not required if using HcpEtcdBackup
	"pv",  // Not required if using HcpEtcdBackup
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

func NewBackup(backupName, clusterID string, ttl time.Duration, namespaces ...string) *velerov1api.Backup {
	backup := builder.ForBackup("velero", backupName).
		StorageLocation("default").
		ObjectMeta(func(object metav1.Object) {
			object.SetLabels(map[string]string{"api.openshift.com/id": clusterID})
		}).
		IncludedNamespaces(namespaces...).
		IncludedResources(backupIncludedResources...).
		TTL(ttl).
		SnapshotVolumes(true). // Set to false if using HcpEtcdBackup
		DefaultVolumesToFsBackup(false).
		DataMover("velero").
		SnapshotMoveData(true). // Set to false if using HcpEtcdBackup
		CSISnapshotTimeout(10 * time.Minute).
		ItemOperationTimeout(30 * time.Minute)
	return backup.Result()
}
