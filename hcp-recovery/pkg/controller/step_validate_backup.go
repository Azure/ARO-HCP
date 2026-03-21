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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func (c *HCPRecoveryController) validateBackup(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	veleroBackup := velerov1api.Backup{}
	key := ctrlclient.ObjectKey{
		Name:      recovery.Spec.BackupId,
		Namespace: "velero",
	}
	err := c.ctrlClient.Get(ctx, key, &veleroBackup)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "Backup not found")
			return c.handlePermanentError(recovery,
				BackupNotValidatedCondition("BackupNotFound", fmt.Sprintf("Backup %s not found", recovery.Spec.BackupId), recovery.Generation, time.Now()))
		}
		logger.Error(err, "Error retrieving backup")
		return c.handleRetryableError(recovery,
			BackupNotValidatedCondition("BackupRetrievalError", fmt.Sprintf("Error retrieving backup %s: %v", recovery.Spec.BackupId, err), recovery.Generation, time.Now()), err)
	}

	if id, ok := veleroBackup.Labels["api.openshift.com/id"]; !ok || id != recovery.Spec.ClusterId {
		logger.Error(nil, "Backup does not belong to cluster", "backupClusterId", veleroBackup.Labels["api.openshift.com/id"], "expectedClusterId", recovery.Spec.ClusterId)
		return c.handlePermanentError(recovery,
			BackupNotValidatedCondition("BackupClusterMismatch", fmt.Sprintf("Backup %s does not belong to cluster %s", recovery.Spec.BackupId, recovery.Spec.ClusterId), recovery.Generation, time.Now()))
	}

	if veleroBackup.Status.Phase != velerov1api.BackupPhaseCompleted {
		logger.Info("Backup is not in completed state", "phase", veleroBackup.Status.Phase)
		return c.handleRetryableError(recovery,
			BackupNotValidatedCondition("BackupNotCompleted", fmt.Sprintf("Backup %s is in phase %s, expected Completed", recovery.Spec.BackupId, veleroBackup.Status.Phase), recovery.Generation, time.Now()),
			fmt.Errorf("backup %s is in phase %s, expected Completed", recovery.Spec.BackupId, veleroBackup.Status.Phase))
	}

	statusUpdate, needsUpdate := NewStatus(recovery.Status).
		WithConditions(
			BackupValidatedCondition(recovery.Generation, time.Now()),
		).AsApplyConfiguration(recovery)
	if needsUpdate {
		return true, &actions{StatusUpdate: statusUpdate}, nil
	}
	return false, nil, nil
}
