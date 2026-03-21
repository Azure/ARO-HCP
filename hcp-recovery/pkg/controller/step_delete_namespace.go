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

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

func (c *HCPRecoveryController) deleteHcpNamespace(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		} else if condition.Type == hcprecoveryv1alpha1.ConditionHCPNamespaceDeleted && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}
	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		logger.Error(err, "Error retrieving HostedCluster")
		return c.handleRetryableError(recovery,
			HCPNamespaceNotDeletedCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}
	if hostedCluster == nil {
		// HC doesn't exist — nothing to delete, skip ahead
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				HCPNamespaceDeletedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	namespaceName := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)
	namespace, err := c.kubeClient.CoreV1().Namespaces().Get(ctx, namespaceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(
					HCPNamespaceDeletedCondition(recovery.Generation, time.Now()),
				).AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		logger.Error(err, "Error retrieving namespace", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			HCPNamespaceNotDeletedCondition("NamespaceRetrievalError",
				fmt.Sprintf("Error retrieving namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	// Already terminating — move on to finalizer removal
	if namespace.Status.Phase == v1.NamespaceTerminating {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				HCPNamespaceDeletedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	// Active namespace — issue delete
	return true, &actions{
		DeleteHcpNamespace: namespace,
		Event:              event("DeletingNamespace", "Deleting HCP namespace %s", namespaceName),
	}, nil
}

func (c *HCPRecoveryController) waitForNamespaceDeletion(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		} else if condition.Type == hcprecoveryv1alpha1.ConditionVeleroRestoreCompleted && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		logger.Error(err, "Error retrieving HostedCluster")
		return c.handleRetryableError(recovery,
			NamespaceNotFullyRemovedCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}
	if hostedCluster == nil {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				NamespaceFullyRemovedCondition(recovery.Generation, time.Now()),
			).AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	namespaceName := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)
	_, err = c.kubeClient.CoreV1().Namespaces().Get(ctx, namespaceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(
					NamespaceFullyRemovedCondition(recovery.Generation, time.Now()),
				).AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		logger.Error(err, "Error retrieving namespace", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			NamespaceNotFullyRemovedCondition("NamespaceRetrievalError",
				fmt.Sprintf("Error retrieving namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	// Namespace still exists — requeue to wait for deletion to complete
	return c.handleRetryableError(recovery,
		NamespaceNotFullyRemovedCondition("NamespaceStillExists",
			fmt.Sprintf("Namespace %s still exists, waiting for deletion to complete", namespaceName),
			recovery.Generation, time.Now()),
		fmt.Errorf("namespace %s still exists", namespaceName))
}
