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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

// unpauseHostedCluster clears spec.pausedUntil and removes the paused-by/paused-at
// annotations that were set by pauseHostedCluster. This must run after the Velero
// restore completes so the HostedCluster can reconcile and become Available again.
// The OADP plugin used to handle unpausing during restore, but that was removed
// in OCPBUGS-77530, so the hcp-recovery controller must do it.
func (c *HCPRecoveryController) unpauseHostedCluster(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionHostedClusterUnpaused && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	hcp, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		logger.Error(err, "Error retrieving HostedCluster")
		return c.handleRetryableError(recovery,
			HostedClusterNotUnpausedCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}
	if hcp == nil {
		logger.Info("HostedCluster not found, skipping unpause", "clusterId", recovery.Spec.ClusterId)
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				HostedClusterUnpausedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	if hcp.Spec.PausedUntil == nil {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				HostedClusterUnpausedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	modified := hcp.DeepCopy()
	modified.Spec.PausedUntil = nil
	delete(modified.Annotations, "hcp-recovery.openshift.io/paused-by")
	delete(modified.Annotations, "hcp-recovery.openshift.io/paused-at")
	return true, &actions{
		PatchHostedCluster: &hostedClusterPatch{object: modified, base: hcp},
		Event:              event("UnpausingCluster", "Unpausing HostedCluster %s", recovery.Spec.ClusterId),
	}, nil
}
