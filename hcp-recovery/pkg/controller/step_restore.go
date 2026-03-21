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
	recoverypkg "github.com/Azure/ARO-HCP/hcp-recovery/pkg/recovery"
)

func (c *HCPRecoveryController) createVeleroRestore(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	name := restoreName(recovery.Name)
	existing := &velerov1api.Restore{}
	err := c.ctrlClient.Get(ctx, ctrlclient.ObjectKey{Namespace: "velero", Name: name}, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Error checking for existing Velero Restore")
		return c.handleTransientError(err)
	}

	if err == nil {
		// Restore already exists — check its status
		switch existing.Status.Phase {
		case velerov1api.RestorePhaseCompleted:
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(
					VeleroRestoreCompletedCondition(recovery.Generation, time.Now()),
				).AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		case velerov1api.RestorePhaseFailed, velerov1api.RestorePhasePartiallyFailed,
			velerov1api.RestorePhaseFailedValidation:
			return c.handlePermanentError(recovery,
				VeleroRestoreNotCompletedCondition("RestoreFailed",
					fmt.Sprintf("Velero Restore %s failed with phase %s", name, existing.Status.Phase),
					recovery.Generation, time.Now()))
		default:
			// Still in progress
			return c.handleRetryableError(recovery,
				VeleroRestoreNotCompletedCondition("RestoreInProgress",
					fmt.Sprintf("Velero Restore %s is in phase %s", name, existing.Status.Phase),
					recovery.Generation, time.Now()),
				fmt.Errorf("velero restore %s is in phase %s", name, existing.Status.Phase))
		}
	}

	// Restore does not exist — create it
	restore := recoverypkg.NewRestore(name, recovery.Spec.BackupId)
	return true, &actions{
		CreateVeleroRestore: restore,
		Event:               event("CreatingRestore", "Creating Velero Restore %s from backup %s", name, recovery.Spec.BackupId),
	}, nil
}
