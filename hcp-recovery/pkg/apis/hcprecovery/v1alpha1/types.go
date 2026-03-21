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

// Package v1alpha1 contains API types for the hcprecovery API group.
// The HCPRecovery resource manages disaster recovery operations for Hosted Control Planes,
// coordinating etcd backup restoration and cluster state reconciliation.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestoreState represents the overall state of the recovery operation.
type RestoreState string

const (
	RestoreStateInProgress RestoreState = "InProgress"
	RestoreStateCompleted  RestoreState = "Completed"
	RestoreStateFailed     RestoreState = "Failed"
)

// Condition types for HCPRecovery. These are evaluated in order during reconciliation.
const (
	// ConditionBackupValidated indicates whether the specified backup exists and is valid.
	ConditionBackupValidated = "BackupValidated"
	// ConditionBackupSchedulePaused indicates whether Velero backup schedules
	// for the cluster have been paused to prevent new backups during restore.
	ConditionBackupSchedulePaused = "BackupSchedulePaused"
	// ConditionHostedClusterPaused indicates whether the HostedCluster has been paused
	// to prevent the hypershift operator from interfering with the restore.
	ConditionHostedClusterPaused = "HostedClusterPaused"
	// ConditionCAPIMachinesBackedUp indicates whether CAPI Machine state has been
	// captured before the HCP namespace is deleted.
	ConditionCAPIMachinesBackedUp = "CAPIMachinesBackedUp"
	// ConditionHCPNamespaceDeleted indicates whether the HCP namespace deletion
	// has been initiated to allow a clean restore of the control plane resources.
	ConditionHCPNamespaceDeleted = "HCPNamespaceDeleted"
	// ConditionCloudFinalizersRemoved indicates whether finalizers have been
	// removed from cloud resources (CAPI/CAPZ types) to allow clean deletion.
	ConditionCloudFinalizersRemoved = "CloudFinalizersRemoved"
	// ConditionDeploymentFinalizersRemoved indicates whether finalizers have been
	// removed from deployment resources to allow clean deletion.
	ConditionDeploymentFinalizersRemoved = "DeploymentFinalizersRemoved"
	// ConditionNamespaceFullyRemoved indicates whether the namespace and all its
	// resources have been fully removed from the cluster.
	ConditionNamespaceFullyRemoved = "NamespaceFullyRemoved"
	// ConditionVeleroRestoreCompleted indicates whether the Velero restore operation
	// has completed successfully, restoring etcd and control plane state.
	ConditionVeleroRestoreCompleted = "VeleroRestoreCompleted"
	// ConditionCAPIMachinesReconciled indicates whether CAPI Machine state has been
	// reconciled with the restored control plane.
	ConditionCAPIMachinesReconciled = "CAPIMachinesReconciled"
	// ConditionManagedByReconciled indicates whether the managed-by labels and
	// ownership references have been reconciled after restore.
	ConditionManagedByReconciled = "ManagedByReconciled"
	// ConditionHostedClusterUnpaused indicates whether the HostedCluster has been
	// unpaused after a successful restore, allowing normal operation to resume.
	ConditionHostedClusterUnpaused = "HostedClusterUnpaused"
	// ConditionBackupScheduleUnpaused indicates whether Velero backup schedules
	// have been unpaused after a successful restore.
	ConditionBackupScheduleUnpaused = "BackupScheduleUnpaused"
	// ConditionHealthChecked indicates whether post-restore health checks have
	// passed, confirming the recovered HCP is operational.
	ConditionHealthChecked = "HealthChecked"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HCPRecovery represents a disaster recovery operation for a Hosted Control Plane.
// It orchestrates the restoration of an HCP from a Velero backup, coordinating
// the pause of the HostedCluster, deletion and recreation of the HCP namespace,
// Velero restore execution, CAPI machine reconciliation, and post-restore health checks.
// The spec is immutable after creation — to change recovery parameters, delete and recreate.
type HCPRecovery struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	// spec defines the desired recovery operation, including the target cluster
	// and backup to restore from. This field is immutable after creation.
	Spec HCPRecoverySpec `json:"spec"`

	// +optional
	// status contains the observed state of the recovery operation, including
	// progress through each recovery step and timestamps.
	Status HCPRecoveryStatus `json:"status,omitempty,omitzero"`
}

// HCPRecoverySpec defines the desired state of a recovery operation.
// All fields are immutable after creation to ensure recovery operations
// are auditable and cannot be modified mid-flight.
type HCPRecoverySpec struct {
	// clusterId is the identifier of the Hosted Cluster to recover.
	// This is used to locate the HostedCluster and HCP resources
	// on the management cluster.
	ClusterId string `json:"clusterId,omitempty"`

	// backupId is the identifier of the Velero backup to restore from.
	// The controller validates that this backup exists and is in a
	// completed state before proceeding with the restore.
	BackupId string `json:"backupId,omitempty"`
}

// HCPRecoveryStatus reports the observed state of the recovery operation.
type HCPRecoveryStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// conditions represent the current state of the recovery operation.
	// Each condition corresponds to a step in the recovery process.
	// The controller processes steps sequentially; a condition with status
	// True indicates that step has completed successfully.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	// startedAt is the timestamp when the recovery operation began processing.
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// +optional
	// completedAt is the timestamp when the recovery operation finished,
	// either successfully or with a terminal failure.
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// +optional
	// restoredToTimestamp is the point-in-time that the backup represents,
	// indicating the state the HCP has been restored to.
	RestoredToTimestamp *metav1.Time `json:"restoredToTimestamp,omitempty"`

	// +optional
	// capiMachineBackup is a reference to the stored CAPI Machine state
	// that was captured before the HCP namespace was deleted. This state
	// is used to reconcile machines after the Velero restore completes.
	CAPIMachineBackup string `json:"capiMachineBackup,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HCPRecoveryList is a list of HCPRecovery resources
type HCPRecoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HCPRecovery `json:"items"`
}
