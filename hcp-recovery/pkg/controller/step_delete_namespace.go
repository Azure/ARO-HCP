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
	applyv1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/klog/v2"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

type conditionBuilderTrue func(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration
type conditionBuilderFalse func(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration

func (c *HCPRecoveryController) initiateNamespaceDeletion(
	ctx context.Context,
	recovery *hcprecoveryv1alpha1.HCPRecovery,
	namespaceName string,
	skipConditions []string,
	deletedCondition conditionBuilderTrue,
	notDeletedCondition conditionBuilderFalse,
) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		for _, skip := range skipConditions {
			if condition.Type == skip && condition.Status == metav1.ConditionTrue {
				return false, nil, nil
			}
		}
	}

	namespace, err := c.kubeClient.CoreV1().Namespaces().Get(ctx, namespaceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(deletedCondition(recovery.Generation, time.Now())).
				AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		logger.Error(err, "Error retrieving namespace", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			notDeletedCondition("NamespaceRetrievalError",
				fmt.Sprintf("Error retrieving namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	if namespace.Status.Phase == v1.NamespaceTerminating {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(deletedCondition(recovery.Generation, time.Now())).
			AsApplyConfiguration(recovery)
		if needsUpdate {
			return true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return false, nil, nil
	}

	return true, &actions{
		DeleteNamespace: namespace,
		Event:           event("DeletingNamespace", "Deleting namespace %s", namespaceName),
	}, nil
}

func (c *HCPRecoveryController) waitForNamespaceGone(
	ctx context.Context,
	recovery *hcprecoveryv1alpha1.HCPRecovery,
	namespaceName string,
	skipConditions []string,
	removedCondition conditionBuilderTrue,
	notRemovedCondition conditionBuilderFalse,
) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		for _, skip := range skipConditions {
			if condition.Type == skip && condition.Status == metav1.ConditionTrue {
				return false, nil, nil
			}
		}
	}

	_, err := c.kubeClient.CoreV1().Namespaces().Get(ctx, namespaceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(removedCondition(recovery.Generation, time.Now())).
				AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		logger.Error(err, "Error retrieving namespace", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			notRemovedCondition("NamespaceRetrievalError",
				fmt.Sprintf("Error retrieving namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	return c.handleRetryableError(recovery,
		notRemovedCondition("NamespaceStillExists",
			fmt.Sprintf("Namespace %s still exists, waiting for deletion to complete", namespaceName),
			recovery.Generation, time.Now()),
		fmt.Errorf("namespace %s still exists", namespaceName))
}

// resolveHcpNamespace resolves the derived HCP namespace name from the HostedCluster.
// Returns ("", true, action, err) if the step should be skipped (HC not found or error).
func (c *HCPRecoveryController) resolveHcpNamespace(
	ctx context.Context,
	recovery *hcprecoveryv1alpha1.HCPRecovery,
	doneCondition conditionBuilderTrue,
	errCondition conditionBuilderFalse,
) (string, bool, *actions, error) {
	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		klog.FromContext(ctx).Error(err, "Error retrieving HostedCluster")
		done, action, err := c.handleRetryableError(recovery,
			errCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
		return "", done, action, err
	}
	if hostedCluster == nil {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(doneCondition(recovery.Generation, time.Now())).
			AsApplyConfiguration(recovery)
		if needsUpdate {
			return "", true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return "", false, nil, nil
	}
	return fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name), false, nil, nil
}

// resolveHcNamespace resolves the HC namespace name from the HostedCluster.
// Returns ("", true, action, err) if the step should be skipped (HC not found or error).
func (c *HCPRecoveryController) resolveHcNamespace(
	ctx context.Context,
	recovery *hcprecoveryv1alpha1.HCPRecovery,
	doneCondition conditionBuilderTrue,
	errCondition conditionBuilderFalse,
) (string, bool, *actions, error) {
	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		klog.FromContext(ctx).Error(err, "Error retrieving HostedCluster")
		done, action, err := c.handleRetryableError(recovery,
			errCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
		return "", done, action, err
	}
	if hostedCluster == nil {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(doneCondition(recovery.Generation, time.Now())).
			AsApplyConfiguration(recovery)
		if needsUpdate {
			return "", true, &actions{StatusUpdate: statusUpdate}, nil
		}
		return "", false, nil, nil
	}
	return hostedCluster.Namespace, false, nil, nil
}

// HCP namespace steps — operate on the derived control plane namespace ({HC.Namespace}-{HC.Name})

func (c *HCPRecoveryController) deleteHcpNamespace(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	skipConditions := []string{hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved, hcprecoveryv1alpha1.ConditionHCPNamespaceDeleted}
	if shouldSkip(recovery, skipConditions) {
		return false, nil, nil
	}

	namespaceName, done, action, err := c.resolveHcpNamespace(ctx, recovery, HCPNamespaceDeletedCondition, HCPNamespaceNotDeletedCondition)
	if namespaceName == "" {
		return done, action, err
	}

	return c.initiateNamespaceDeletion(ctx, recovery, namespaceName,
		skipConditions, HCPNamespaceDeletedCondition, HCPNamespaceNotDeletedCondition)
}

func (c *HCPRecoveryController) waitForHcpNamespaceDeletion(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	skipConditions := []string{hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved, hcprecoveryv1alpha1.ConditionVeleroRestoreCompleted}
	if shouldSkip(recovery, skipConditions) {
		return false, nil, nil
	}

	namespaceName, done, action, err := c.resolveHcpNamespace(ctx, recovery, NamespaceFullyRemovedCondition, NamespaceNotFullyRemovedCondition)
	if namespaceName == "" {
		return done, action, err
	}

	return c.waitForNamespaceGone(ctx, recovery, namespaceName,
		skipConditions, NamespaceFullyRemovedCondition, NamespaceNotFullyRemovedCondition)
}

// HC namespace steps — operate on the HostedCluster namespace (HC.Namespace)

func (c *HCPRecoveryController) deleteHcNamespace(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	skipConditions := []string{hcprecoveryv1alpha1.ConditionHCNamespaceFullyRemoved, hcprecoveryv1alpha1.ConditionHCNamespaceDeleted}
	if shouldSkip(recovery, skipConditions) {
		return false, nil, nil
	}

	namespaceName, done, action, err := c.resolveHcNamespace(ctx, recovery, HCNamespaceDeletedCondition, HCNamespaceNotDeletedCondition)
	if namespaceName == "" {
		return done, action, err
	}

	return c.initiateNamespaceDeletion(ctx, recovery, namespaceName,
		skipConditions, HCNamespaceDeletedCondition, HCNamespaceNotDeletedCondition)
}

func (c *HCPRecoveryController) waitForHcNamespaceDeletion(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	skipConditions := []string{hcprecoveryv1alpha1.ConditionHCNamespaceFullyRemoved, hcprecoveryv1alpha1.ConditionVeleroRestoreCompleted}
	if shouldSkip(recovery, skipConditions) {
		return false, nil, nil
	}

	// After the HC namespace is deleted, getHostedCluster returns nil because the
	// HostedCluster CR lived inside that namespace. If the HC is gone, the namespace is gone.
	namespaceName, done, action, err := c.resolveHcNamespace(ctx, recovery, HCNamespaceFullyRemovedCondition, HCNamespaceNotFullyRemovedCondition)
	if namespaceName == "" {
		return done, action, err
	}

	return c.waitForNamespaceGone(ctx, recovery, namespaceName,
		skipConditions, HCNamespaceFullyRemovedCondition, HCNamespaceNotFullyRemovedCondition)
}

func shouldSkip(recovery *hcprecoveryv1alpha1.HCPRecovery, skipConditions []string) bool {
	for _, condition := range recovery.Status.Conditions {
		for _, skip := range skipConditions {
			if condition.Type == skip && condition.Status == metav1.ConditionTrue {
				return true
			}
		}
	}
	return false
}
