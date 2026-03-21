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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func (c *HCPRecoveryController) pauseHostedCluster(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	// Need to check for labels, the cluster is paused by another source, don't touch it.
	// oadp.openshift.io/paused-at: "2026-03-17T18:29:23Z"
	// oadp.openshift.io/paused-by: hypershift-oadp-plugin
	// future versions of oadp do not pause anymore, need to look at hypershift operator to see if it pauses.
	// Need to add a pause similar to above to indicate that the cluster was paused by the hcp-recovery controller

	logger := klog.FromContext(ctx)

	// if status == pause then skip to prevent a repause after restore.
	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionHostedClusterPaused && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	// TODO: If a hosted cluster is not found then pause is not required and continue to delete namespaces and then restore.
	hcp, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		logger.Error(err, "Error retrieving HostedCluster")
		return c.handleRetryableError(recovery,
			HostedClusterNotPausedCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}
	if hcp == nil {
		logger.Error(nil, "HostedCluster not found", "clusterId", recovery.Spec.ClusterId)
		return c.handlePermanentError(recovery,
			HostedClusterNotPausedCondition("HostedClusterNotFound",
				fmt.Sprintf("HostedCluster not found for cluster %s", recovery.Spec.ClusterId),
				recovery.Generation, time.Now()))
	}

	if hcp.Spec.PausedUntil != nil && strings.ToLower(*hcp.Spec.PausedUntil) == "true" {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				HostedClusterPausedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	pausedTrue := "true"
	modified := hcp.DeepCopy()
	modified.Spec.PausedUntil = &pausedTrue
	modified.Annotations["hcp-recovery.openshift.io/paused-by"] = "hcp-recovery"
	modified.Annotations["hcp-recovery.openshift.io/paused-at"] = time.Now().Format(time.RFC3339)
	return true, &actions{
		PatchHostedCluster: &hostedClusterPatch{object: modified, base: hcp},
		Event:              event("PausingCluster", "Pausing HostedCluster %s", recovery.Spec.ClusterId),
	}, nil
}
