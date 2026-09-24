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

// Package repositorycleanup retires orphaned Kopia repositories using durable,
// immutable cleanup intents. Completed intents are retained and swept again: a
// Velero worker can create a late maintenance Job after its repository disappears.
// Deleting a tombstone explicitly ends this protection after one final sweep.
// Live reads narrow, but cannot atomically fence, concurrent Kubernetes writers
// and Azure blob writes. No global Velero pause or cross-system lock is assumed.
package repositorycleanup

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/repositorycleanup/storage"
)

const (
	RepositoryCleanupControllerName = "RepositoryCleanup"
	Namespace                       = "velero"
	Finalizer                       = "mgmtagent.aro-hcp.azure.com/repository-cleanup"
	PreserveBackupAnnotation        = "mgmt-agent.aro-hcp.azure.com/preserve-backup"
	RepositoryFieldManager          = "repository-cleanup-repository"
	CleanupFieldManager             = "repository-cleanup-intent"
	StatusFieldManager              = "repository-cleanup-status"
	Complete                        = "Complete"
	WaitingForRepository            = "WaitingForRepository"
	DrainingMaintenance             = "DrainingMaintenance"
	DeletingBlobs                   = "DeletingBlobs"
	SweepFailed                     = "SweepFailed"
	Blocked                         = "Blocked"
	CleanupComplete                 = "CleanupComplete"
	recheckInterval                 = time.Minute
	tombstoneRecheckInterval        = 10 * time.Minute
	repoNameLabel                   = "velero.io/repo-name"
)

var (
	BackupRepositoryCleanupsGVR = schema.GroupVersionResource{Group: "mgmtagent.aro-hcp.azure.com", Version: "v1alpha1", Resource: "backuprepositorycleanups"}
	BackupRepositoriesGVR       = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backuprepositories"}
	BackupStorageLocationsGVR   = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backupstoragelocations"}
	BackupsGVR                  = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backups"}
	SchedulesGVR                = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "schedules"}
	RestoresGVR                 = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "restores"}
	DataUploadsGVR              = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datauploads"}
	DataDownloadsGVR            = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datadownloads"}
	PodVolumeBackupsGVR         = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "podvolumebackups"}
	PodVolumeRestoresGVR        = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "podvolumerestores"}
	HostedClustersGVR           = schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"}
	JobsGVR                     = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	PodsGVR                     = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// RequiredResources must be watched without label filtering in velero, except
// HostedClusters which must be watched across all namespaces. Start the supplied
// informers outside Run. All informers except HostedClusters are unstructured.
func RequiredResources() []schema.GroupVersionResource {
	return []schema.GroupVersionResource{BackupRepositoriesGVR, BackupRepositoryCleanupsGVR,
		BackupStorageLocationsGVR, BackupsGVR, SchedulesGVR, RestoresGVR, DataUploadsGVR,
		DataDownloadsGVR, PodVolumeBackupsGVR, PodVolumeRestoresGVR, HostedClustersGVR, JobsGVR, PodsGVR}
}

// BackupRepositoryCleanup's entire Spec is immutable. It deliberately has no
// owner reference: repository deletion must not garbage-collect the intent.
type BackupRepositoryCleanup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupRepositoryCleanupSpec   `json:"spec"`
	Status            BackupRepositoryCleanupStatus `json:"status,omitempty"`
}

type BackupRepositoryCleanupSpec struct {
	Repository RepositoryIdentity `json:"repository"`
	Storage    storage.Target     `json:"storage"`
}

type RepositoryIdentity struct {
	Name            string    `json:"name"`
	UID             types.UID `json:"uid"`
	VolumeNamespace string    `json:"volumeNamespace"`
}

type BackupRepositoryCleanupStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func CleanupName(uid types.UID) string { return "repo-" + string(uid) }

// Observations contains live API observations, not informer-derived evidence.
// DependenciesComplete is set only after all dependency lists and the HC list
// have been read completely. Partial observations can only block or attach
// protection, never authorize destructive actions. Swept is an in-memory receipt
// from a successful prefix deletion, valid only for this reconciliation. Status
// is never a receipt. Now is supplied by the driver to keep planning pure.
type Observations struct {
	Repository           *unstructured.Unstructured
	Cleanup              *unstructured.Unstructured
	Objects              map[schema.GroupVersionResource][]unstructured.Unstructured
	DependenciesComplete bool
	HostedClusters       []unstructured.Unstructured
	Now                  time.Time
	Swept                *storage.Target
}

type Apply struct {
	Resource     schema.GroupVersionResource
	Object       *unstructured.Unstructured
	FieldManager string
	Subresource  string
}

type Delete struct {
	Resource        schema.GroupVersionResource
	Name            string
	UID             types.UID
	ResourceVersion string
}

// Plan contains at most one lifecycle mutation (status applies are independent).
// The driver replans from fresh observations before every destructive mutation.
type Plan struct {
	ObserveDependencies bool
	Applies             []Apply
	Deletes             []Delete
	DeletePrefix        *storage.Target
	RequeueAfter        time.Duration
	Condition           *metav1.Condition
}
