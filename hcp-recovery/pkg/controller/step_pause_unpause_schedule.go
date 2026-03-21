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
	"context"
	"fmt"
	"time"

	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func (c *HCPRecoveryController) pauseBackupSchedule(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	// If already paused, skip to prevent re-pausing after restore.
	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionBackupSchedulePaused && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	schedules := &velerov1api.ScheduleList{}
	if err := c.ctrlClient.List(ctx, schedules, ctrlclient.MatchingLabels{"api.openshift.com/id": recovery.Spec.ClusterId}); err != nil {
		logger.Error(err, "Error listing backup schedules")
		return c.handleRetryableError(recovery,
			BackupScheduleNotPausedCondition("ScheduleListError",
				fmt.Sprintf("Error listing backup schedules for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}

	var toPause []velerov1api.Schedule
	for _, s := range schedules.Items {
		if !s.Spec.Paused {
			toPause = append(toPause, s)
		}
	}

	if len(toPause) == 0 {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				BackupSchedulePausedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	paused := make([]velerov1api.Schedule, 0, len(toPause))
	for _, s := range toPause {
		modified := *s.DeepCopy()
		modified.Spec.Paused = true
		paused = append(paused, modified)
	}

	logger.Info("Pausing backup schedules", "count", len(paused))
	return true, &actions{
		PatchVeleroSchedules: paused,
		Event:                event("PausingSchedules", "Pausing %d backup schedule(s) for cluster %s", len(paused), recovery.Spec.ClusterId),
	}, nil
}

func (c *HCPRecoveryController) unpauseBackupSchedule(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	// If already unpaused, skip.
	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionBackupScheduleUnpaused && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	schedules := &velerov1api.ScheduleList{}
	if err := c.ctrlClient.List(ctx, schedules, ctrlclient.MatchingLabels{"api.openshift.com/id": recovery.Spec.ClusterId}); err != nil {
		logger.Error(err, "Error listing backup schedules")
		return c.handleRetryableError(recovery,
			BackupScheduleNotUnpausedCondition("ScheduleListError",
				fmt.Sprintf("Error listing backup schedules for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}

	var toUnpause []velerov1api.Schedule
	for _, s := range schedules.Items {
		if s.Spec.Paused {
			toUnpause = append(toUnpause, s)
		}
	}

	if len(toUnpause) == 0 {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				BackupScheduleUnpausedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	unpaused := make([]velerov1api.Schedule, 0, len(toUnpause))
	for _, s := range toUnpause {
		modified := *s.DeepCopy()
		modified.Spec.Paused = false
		unpaused = append(unpaused, modified)
	}

	logger.Info("Unpausing backup schedules", "count", len(unpaused))
	return true, &actions{
		PatchVeleroSchedules: unpaused,
		Event:                event("UnpausingSchedules", "Unpausing %d backup schedule(s) for cluster %s", len(unpaused), recovery.Spec.ClusterId),
	}, nil
}
