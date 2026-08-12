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
	"crypto/sha256"
	"fmt"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/builder"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
)

const (
	BackupScheduleDesireNamePrefix = "backupschedule-"
	DesireTagKeySchedule           = "backupschedule"
	VeleroGroup                    = "velero.io"
	VeleroVersion                  = "v1"
	VeleroScheduleResource         = "schedules"
	VeleroNamespace                = "velero"
	etcdHookName                   = "etcd-snapshot"
	etcdAppLabel                   = "app"
	etcdAppLabelValue              = "etcd"
	etcdContainerName              = "etcd"
	etcdSnapshotPath               = "/var/lib/backup.db"
	etcdCACertPath                 = "/etc/etcd/tls/etcd-ca/ca.crt"
	etcdClientCert                 = "/etc/etcd/tls/client/etcd-client.crt"
	etcdClientKey                  = "/etc/etcd/tls/client/etcd-client.key"
	etcdClientService              = "etcd-client"
	etcdClientPort                 = "2379"
)

// etcdBackupHooks defines velero hooks that are run pre and post backup with the etcd pods via exec.
// The etcdctl snapshot is taken and will be captured by the CSI snapshot of the PVC.  After the backup is
// complete a post hook deletes the backup from the pvc to free up space.
func etcdBackupHooks(controlPlaneNamespace string) velerov1api.BackupHooks {
	endpoint := fmt.Sprintf("https://%s.%s.svc:%s", etcdClientService, controlPlaneNamespace, etcdClientPort)
	return velerov1api.BackupHooks{
		Resources: []velerov1api.BackupResourceHookSpec{
			{
				Name:               etcdHookName,
				IncludedNamespaces: []string{controlPlaneNamespace},
				IncludedResources:  []string{"pods"},
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						etcdAppLabel: etcdAppLabelValue,
					},
				},
				PreHooks: []velerov1api.BackupResourceHook{
					{
						Exec: &velerov1api.ExecHook{
							Container: etcdContainerName,
							Command: []string{
								"/bin/sh", "-c",
								// `etcdctl snapshot save` writes to <path>.part, fsyncs that file, then atomically
								// renames it to <path> — but it does NOT fsync the parent directory. The CSI snapshot
								// captures block-device state, so the rename (a directory metadata op still sitting in
								// page cache/journal) can be missing from the snapshot, leaving only backup.db.part on
								// the restored PVC. `sync -f <path>` issues syncfs() on the data PVC, committing the
								// file data AND the rename to the block device before Velero snapshots the volume.
								// Velero runs this pre-hook to completion before snapshotting the pod's volumes,
								// so the flush is guaranteed to land first.
								fmt.Sprintf(
									"ETCDCTL_API=3 etcdctl --endpoints=%s --cacert %s --cert %s --key %s snapshot save %s && sync -f %s",
									endpoint, etcdCACertPath, etcdClientCert, etcdClientKey, etcdSnapshotPath, etcdSnapshotPath,
								),
							},
							OnError: velerov1api.HookErrorModeFail,
							Timeout: metav1.Duration{Duration: 10 * time.Minute},
						},
					},
				},
				PostHooks: []velerov1api.BackupResourceHook{
					{
						Exec: &velerov1api.ExecHook{
							Container: etcdContainerName,
							Command: []string{
								"/bin/sh", "-c",
								fmt.Sprintf("rm -f %s %s.part", etcdSnapshotPath, etcdSnapshotPath),
							},
							OnError: velerov1api.HookErrorModeContinue,
							Timeout: metav1.Duration{Duration: 2 * time.Minute},
						},
					},
				},
			},
		},
	}
}

var backupIncludedResources = []string{
	"multitenantpodnetworkconfigs",
	"podnetworks",
	"sa",
	"role",
	"rolebinding",
	"pvc", // Not required if using HcpEtcdBackup
	"pv",  // Not required if using HcpEtcdBackup
	"configmap",
	"priorityclasses",
	"pods",
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

// AzureKMSKeyFingerprint mirrors HyperShift's FingerprintAzureKMSKey.
func AzureKMSKeyFingerprint(keyVaultName, keyName, keyVersion string) string {
	h := sha256.Sum256([]byte(keyVaultName + "/" + keyName + "/" + keyVersion))
	return fmt.Sprintf("%x", h)
}

func NewBackup(backupName, resourceID, kmsKeyFingerprint, hostedClusterNamespace, controlPlaneNamespace string, ttl time.Duration) *velerov1api.Backup {
	annotations := map[string]string{
		controllerutils.HcpClusterAzureResourceIdAnnotation: resourceID,
	}
	if kmsKeyFingerprint != "" {
		annotations[controllerutils.HcpClusterKmsKeyFingerprintAnnotation] = kmsKeyFingerprint
	}
	backup := builder.ForBackup("velero", backupName).
		StorageLocation("default").
		ObjectMeta(func(object metav1.Object) {
			object.SetAnnotations(annotations)
		}).
		IncludedNamespaces(hostedClusterNamespace, controlPlaneNamespace).
		IncludedResources(backupIncludedResources...).
		TTL(ttl).
		SnapshotVolumes(true). // Set to false if using HcpEtcdBackup
		DefaultVolumesToFsBackup(false).
		DataMover("velero").
		SnapshotMoveData(true). // Set to false if using HcpEtcdBackup
		CSISnapshotTimeout(10 * time.Minute).
		ItemOperationTimeout(15 * time.Minute).
		Hooks(etcdBackupHooks(controlPlaneNamespace))
	return backup.Result()
}
