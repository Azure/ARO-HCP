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

package controller

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	applyv1 "k8s.io/client-go/applyconfigurations/meta/v1"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
	hcprecoveryapply "github.com/Azure/ARO-HCP/hcp-recovery/pkg/generated/applyconfiguration/hcprecovery/v1alpha1"
)

type Status struct {
	applyConfig *hcprecoveryapply.HCPRecoveryStatusApplyConfiguration
}

func NewStatus(status hcprecoveryv1alpha1.HCPRecoveryStatus) *Status {
	return &Status{
		applyConfig: ApplyConfigForStatus(status),
	}
}

func (s *Status) WithConditions(updated ...*applyv1.ConditionApplyConfiguration) *Status {
	// Build a map of existing conditions by type for timestamp preservation
	existingByType := make(map[string]*applyv1.ConditionApplyConfiguration)
	for i := range s.applyConfig.Conditions {
		if s.applyConfig.Conditions[i].Type != nil {
			existingByType[*s.applyConfig.Conditions[i].Type] = &s.applyConfig.Conditions[i]
		}
	}

	updatedTypes := sets.New[string]()
	for _, condition := range updated {
		if condition.Type == nil {
			panic(fmt.Errorf("programmer error: must set a type for condition: %#v", condition))
		}
		updatedTypes.Insert(*condition.Type)

		// If the condition content hasn't changed, preserve the existing timestamp
		if existing, ok := existingByType[*condition.Type]; ok {
			if conditionContentEqual(condition, existing) {
				condition.LastTransitionTime = existing.LastTransitionTime
			}
		}
	}
	conditions := make([]*applyv1.ConditionApplyConfiguration, 0, len(updated)+len(s.applyConfig.Conditions))
	conditions = append(conditions, updated...)
	for _, condition := range s.applyConfig.Conditions {
		if !updatedTypes.Has(*condition.Type) {
			conditions = append(conditions, &condition)
		}
	}
	// Clear existing conditions and set the new merged list
	s.applyConfig.Conditions = nil
	s.applyConfig.WithConditions(conditions...)
	return s
}

// conditionContentEqual returns true if two conditions have the same
// status, reason, message, and observed generation (ignoring timestamps).
func conditionContentEqual(a, b *applyv1.ConditionApplyConfiguration) bool {
	if (a.Status == nil) != (b.Status == nil) || (a.Status != nil && *a.Status != *b.Status) {
		return false
	}
	if (a.Reason == nil) != (b.Reason == nil) || (a.Reason != nil && *a.Reason != *b.Reason) {
		return false
	}
	if (a.Message == nil) != (b.Message == nil) || (a.Message != nil && *a.Message != *b.Message) {
		return false
	}
	if (a.ObservedGeneration == nil) != (b.ObservedGeneration == nil) || (a.ObservedGeneration != nil && *a.ObservedGeneration != *b.ObservedGeneration) {
		return false
	}
	return true
}

func (s *Status) WithStartedAt(t metav1.Time) *Status {
	s.applyConfig.WithStartedAt(t)
	return s
}

func (s *Status) WithCompletedAt(t metav1.Time) *Status {
	s.applyConfig.WithCompletedAt(t)
	return s
}

func (s *Status) WithRestoredToTimestamp(t metav1.Time) *Status {
	s.applyConfig.WithRestoredToTimestamp(t)
	return s
}

func (s *Status) WithCAPIMachineBackup(ref string) *Status {
	s.applyConfig.WithCAPIMachineBackup(ref)
	return s
}

// AsApplyConfiguration returns the apply configuration for the status and a boolean indicating
// if the status needs to be updated. The needsUpdate check is required because the controller
// uses an action-based reconciliation pattern where each sync loop performs at most one mutating
// action. The controller must know whether a status update is necessary before deciding to emit
// it as the action for the current loop iteration, rather than falling through to the next step.
func (s *Status) AsApplyConfiguration(recovery *hcprecoveryv1alpha1.HCPRecovery) (*hcprecoveryapply.HCPRecoveryApplyConfiguration, bool) {
	var needsUpdate bool

	if s.applyConfig.StartedAt != nil {
		if recovery.Status.StartedAt == nil {
			needsUpdate = true
		} else if !s.applyConfig.StartedAt.Equal(recovery.Status.StartedAt) {
			needsUpdate = true
		}
	}

	if s.applyConfig.CompletedAt != nil {
		if recovery.Status.CompletedAt == nil {
			needsUpdate = true
		} else if !s.applyConfig.CompletedAt.Equal(recovery.Status.CompletedAt) {
			needsUpdate = true
		}
	}

	if s.applyConfig.RestoredToTimestamp != nil {
		if recovery.Status.RestoredToTimestamp == nil {
			needsUpdate = true
		} else if !s.applyConfig.RestoredToTimestamp.Equal(recovery.Status.RestoredToTimestamp) {
			needsUpdate = true
		}
	}

	if (s.applyConfig.CAPIMachineBackup != nil && *s.applyConfig.CAPIMachineBackup != recovery.Status.CAPIMachineBackup) ||
		(s.applyConfig.CAPIMachineBackup == nil && recovery.Status.CAPIMachineBackup != "") {
		needsUpdate = true
	}

	if !conditionsEqual(s.applyConfig.Conditions, recovery.Status.Conditions) {
		needsUpdate = true
	}

	cfg := hcprecoveryapply.HCPRecovery(recovery.Name, recovery.Namespace)
	cfg.Status = s.applyConfig
	return cfg, needsUpdate
}

func conditionsEqual(applyConditions []applyv1.ConditionApplyConfiguration, statusConditions []metav1.Condition) bool {
	if len(applyConditions) != len(statusConditions) {
		return false
	}

	applyMap := make(map[string]*applyv1.ConditionApplyConfiguration)
	for i := range applyConditions {
		if applyConditions[i].Type != nil {
			applyMap[*applyConditions[i].Type] = &applyConditions[i]
		}
	}

	for _, statusCond := range statusConditions {
		applyCond, exists := applyMap[statusCond.Type]
		if !exists {
			return false
		}
		if applyCond.Status == nil || *applyCond.Status != statusCond.Status {
			return false
		}
		if applyCond.Reason == nil || *applyCond.Reason != statusCond.Reason {
			return false
		}
		if applyCond.Message == nil || *applyCond.Message != statusCond.Message {
			return false
		}
		if applyCond.ObservedGeneration == nil && statusCond.ObservedGeneration != 0 {
			return false
		}
		if applyCond.ObservedGeneration != nil && *applyCond.ObservedGeneration != statusCond.ObservedGeneration {
			return false
		}
	}

	return true
}

func ApplyConfigForStatus(status hcprecoveryv1alpha1.HCPRecoveryStatus) *hcprecoveryapply.HCPRecoveryStatusApplyConfiguration {
	cfg := hcprecoveryapply.HCPRecoveryStatus()

	if status.StartedAt != nil {
		cfg.WithStartedAt(*status.StartedAt)
	}
	if status.CompletedAt != nil {
		cfg.WithCompletedAt(*status.CompletedAt)
	}
	if status.RestoredToTimestamp != nil {
		cfg.WithRestoredToTimestamp(*status.RestoredToTimestamp)
	}
	if status.CAPIMachineBackup != "" {
		cfg.WithCAPIMachineBackup(status.CAPIMachineBackup)
	}

	conditions := make([]*applyv1.ConditionApplyConfiguration, 0, len(status.Conditions))
	for _, c := range status.Conditions {
		conditions = append(conditions, &applyv1.ConditionApplyConfiguration{
			Type:               &c.Type,
			Status:             &c.Status,
			Reason:             &c.Reason,
			Message:            &c.Message,
			ObservedGeneration: &c.ObservedGeneration,
			LastTransitionTime: &c.LastTransitionTime,
		})
	}
	cfg.WithConditions(conditions...)

	return cfg
}

// Condition builders — each recovery step has a success (True) and failure (False) variant.

func BackupValidatedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionBackupValidated).
		WithStatus(metav1.ConditionTrue).
		WithReason("BackupValid").
		WithMessage("Backup exists and is in a completed state").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func BackupNotValidatedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionBackupValidated).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func BackupSchedulePausedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionBackupSchedulePaused).
		WithStatus(metav1.ConditionTrue).
		WithReason("SchedulesPaused").
		WithMessage("Backup schedules have been paused").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func BackupScheduleNotPausedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionBackupSchedulePaused).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HostedClusterPausedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHostedClusterPaused).
		WithStatus(metav1.ConditionTrue).
		WithReason("ClusterPaused").
		WithMessage("HostedCluster has been paused").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HostedClusterNotPausedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHostedClusterPaused).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CAPIMachinesBackedUpCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionCAPIMachinesBackedUp).
		WithStatus(metav1.ConditionTrue).
		WithReason("MachinesBackedUp").
		WithMessage("CAPI Machine state has been captured").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CAPIMachinesNotBackedUpCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionCAPIMachinesBackedUp).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HCPNamespaceDeletedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHCPNamespaceDeleted).
		WithStatus(metav1.ConditionTrue).
		WithReason("NamespaceDeletionInitiated").
		WithMessage("HCP namespace deletion has been initiated").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HCPNamespaceNotDeletedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHCPNamespaceDeleted).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CloudFinalizersRemovedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionCloudFinalizersRemoved).
		WithStatus(metav1.ConditionTrue).
		WithReason("CloudFinalizersRemoved").
		WithMessage("Finalizers have been removed from cloud resources").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CloudFinalizersNotRemovedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionCloudFinalizersRemoved).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func DeploymentFinalizersRemovedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionDeploymentFinalizersRemoved).
		WithStatus(metav1.ConditionTrue).
		WithReason("DeploymentFinalizersRemoved").
		WithMessage("Finalizers have been removed from deployment resources").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func DeploymentFinalizersNotRemovedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionDeploymentFinalizersRemoved).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func NamespaceFullyRemovedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved).
		WithStatus(metav1.ConditionTrue).
		WithReason("NamespaceRemoved").
		WithMessage("Namespace and all resources have been fully removed").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func NamespaceNotFullyRemovedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func VeleroRestoreCompletedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionVeleroRestoreCompleted).
		WithStatus(metav1.ConditionTrue).
		WithReason("RestoreCompleted").
		WithMessage("Velero restore operation has completed successfully").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func VeleroRestoreNotCompletedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionVeleroRestoreCompleted).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CAPIMachinesReconciledCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionCAPIMachinesReconciled).
		WithStatus(metav1.ConditionTrue).
		WithReason("MachinesReconciled").
		WithMessage("CAPI Machine state has been reconciled with the restored control plane").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CAPIMachinesNotReconciledCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionCAPIMachinesReconciled).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func ManagedByReconciledCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionManagedByReconciled).
		WithStatus(metav1.ConditionTrue).
		WithReason("ManagedByReconciled").
		WithMessage("Managed-by labels and ownership references have been reconciled").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func ManagedByNotReconciledCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionManagedByReconciled).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HostedClusterUnpausedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHostedClusterUnpaused).
		WithStatus(metav1.ConditionTrue).
		WithReason("ClusterUnpaused").
		WithMessage("HostedCluster has been unpaused after successful restore").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HostedClusterNotUnpausedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHostedClusterUnpaused).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func BackupScheduleUnpausedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionBackupScheduleUnpaused).
		WithStatus(metav1.ConditionTrue).
		WithReason("SchedulesUnpaused").
		WithMessage("Backup schedules have been unpaused after successful restore").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func BackupScheduleNotUnpausedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionBackupScheduleUnpaused).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HealthCheckedCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHealthChecked).
		WithStatus(metav1.ConditionTrue).
		WithReason("HealthCheckPassed").
		WithMessage("Post-restore health checks have passed").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func HealthCheckFailedCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(hcprecoveryv1alpha1.ConditionHealthChecked).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}
