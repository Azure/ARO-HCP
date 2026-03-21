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

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

// If any of the below conditions are not true, the cluster is not ready, update the hcprecovery status
/*
  - lastTransitionTime: "2026-03-18T18:22:57Z"
    message: ingress-operator deployment has 1 unavailable replicas
    observedGeneration: 25
    reason: UnavailableReplicas
    status: "True"
    type: Degraded
  - lastTransitionTime: "2026-03-18T18:24:03Z"
    message: ""
    observedGeneration: 25
    reason: QuorumAvailable
    status: "True"
    type: EtcdAvailable
  - lastTransitionTime: "2026-03-18T18:24:15Z"
    message: Kube APIServer deployment is available
    observedGeneration: 25
    reason: AsExpected
    status: "True"
    type: KubeAPIServerAvailable
  - lastTransitionTime: "2026-03-18T18:22:57Z"
    message: All is well
    observedGeneration: 25
    reason: AsExpected
    status: "True"
    type: InfrastructureReady
  - lastTransitionTime: "2026-03-18T18:22:57Z"
    message: All is well
    observedGeneration: 25
    reason: AsExpected
    status: "True"
    type: ExternalDNSReachable
*/

func (c *HCPRecoveryController) validateHostedCluster(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {

	logger := klog.FromContext(ctx)

	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "HostedCluster not found")
			return c.handlePermanentError(recovery, HealthCheckFailedCondition("HostedClusterNotFound",
				fmt.Sprintf("HostedCluster not found for cluster %s", recovery.Spec.ClusterId),
				recovery.Generation, time.Now(),
			))
		}
		return false, nil, err
	}
	if hostedCluster == nil {
		logger.Error(nil, "HostedCluster is nil")
		return c.handlePermanentError(recovery, HealthCheckFailedCondition("HostedClusterNotFound",
			fmt.Sprintf("HostedCluster not found for cluster %s", recovery.Spec.ClusterId),
			recovery.Generation, time.Now(),
		))
	}

	for _, condition := range hostedCluster.Status.Conditions {
		if condition.Type != string(v1beta1.HostedClusterAvailable) {
			continue
		}
		if condition.Status != v1.ConditionTrue {
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(
					HealthCheckFailedCondition("HostedClusterNotAvailable", fmt.Sprintf("HostedCluster %s Condition: %s is %s", recovery.Spec.ClusterId, v1beta1.HostedClusterAvailable, condition.Status), recovery.Generation, time.Now()),
				).AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				HealthCheckedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	// HostedClusterAvailable condition not found
	statusUpdate, needsUpdate := NewStatus(recovery.Status).
		WithConditions(
			HealthCheckFailedCondition("ConditionNotFound", fmt.Sprintf("HostedCluster %s does not have %s condition", recovery.Spec.ClusterId, v1beta1.HostedClusterAvailable), recovery.Generation, time.Now()),
		).AsApplyConfiguration(recovery)
	if needsUpdate {
		return true, &actions{StatusUpdate: statusUpdate}, nil
	}
	return false, nil, nil
}
