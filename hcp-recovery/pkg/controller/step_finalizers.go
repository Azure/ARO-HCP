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
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	capzv1beta1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
)

// cloudFinalizerRemovalTypes lists cloud resource types whose finalizers must
// be stripped to allow a terminating HCP namespace to be cleaned up.
var cloudFinalizerRemovalTypes = []ctrlclient.ObjectList{
	&capzv1beta1.AzureMachineList{},
	&clusterv1beta1.MachineList{},
	&clusterv1beta1.MachineSetList{},
	&clusterv1beta1.MachineDeploymentList{},
	&v1beta1.HostedControlPlaneList{},
	&clusterv1beta1.ClusterList{},
}

// collectCloudFinalizerRemovals lists all objects of the given types in the
// namespace and returns finalizerRemoval entries for any that have finalizers set.
func (c *HCPRecoveryController) collectCloudFinalizerRemovals(ctx context.Context, namespace string, listTypes []ctrlclient.ObjectList) ([]finalizerRemoval, error) {
	var removals []finalizerRemoval
	for _, listTemplate := range listTypes {
		list := listTemplate.DeepCopyObject().(ctrlclient.ObjectList)
		if err := c.ctrlClient.List(ctx, list, ctrlclient.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("listing %T in namespace %s: %w", listTemplate, namespace, err)
		}
		items, err := apimeta.ExtractList(list)
		if err != nil {
			return nil, fmt.Errorf("extracting items from %T: %w", listTemplate, err)
		}
		for _, item := range items {
			obj := item.(ctrlclient.Object)
			if len(obj.GetFinalizers()) > 0 {
				base := obj.DeepCopyObject().(ctrlclient.Object)
				obj.SetFinalizers(nil)
				removals = append(removals, finalizerRemoval{object: obj, base: base})
			}
		}
	}
	return removals, nil
}

// collectDeploymentFinalizerRemovals returns finalizerRemoval entries for the
// cluster-api and capi-provider deployments in the namespace, waiting for
// replicas to reach zero before including them.
func (c *HCPRecoveryController) collectDeploymentFinalizerRemovals(ctx context.Context, namespace string) ([]finalizerRemoval, error) {
	var removals []finalizerRemoval
	for _, name := range []string{"cluster-api", "capi-provider"} {
		dep, err := c.kubeClient.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}

		if len(dep.GetFinalizers()) == 0 {
			continue
		}

		// The namespace is terminating, wait for pods to be terminating before removing finalizers to prevent orphaned processes.
		if dep.Status.Replicas > 0 {
			return nil, fmt.Errorf("deployment %s/%s still has %d replicas, waiting for scale-down before removing finalizers", namespace, name, dep.Status.Replicas)
		}

		base := dep.DeepCopyObject().(ctrlclient.Object)
		dep.SetFinalizers(nil)
		removals = append(removals, finalizerRemoval{object: dep, base: base})
	}
	return removals, nil
}

func (c *HCPRecoveryController) removeCloudResourcesFinalizers(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
		if condition.Type == hcprecoveryv1alpha1.ConditionCloudFinalizersRemoved && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		logger.Error(err, "Error retrieving HostedCluster")
		return c.handleRetryableError(recovery,
			CloudFinalizersNotRemovedCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}

	if hostedCluster == nil {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				CloudFinalizersRemovedCondition(recovery.Generation, time.Now()),
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
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(
					CloudFinalizersRemovedCondition(recovery.Generation, time.Now()),
				).AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		logger.Error(err, "Error retrieving namespace", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			CloudFinalizersNotRemovedCondition("NamespaceRetrievalError",
				fmt.Sprintf("Error retrieving namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	if namespace.Status.Phase != v1.NamespaceTerminating {
		return c.handlePermanentError(recovery,
			CloudFinalizersNotRemovedCondition("NamespaceNotTerminating",
				fmt.Sprintf("Namespace %s is not terminating, cannot remove finalizers", namespaceName),
				recovery.Generation, time.Now()))
	}

	removals, err := c.collectCloudFinalizerRemovals(ctx, namespaceName, cloudFinalizerRemovalTypes)
	if err != nil {
		logger.Error(err, "Error collecting cloud finalizer removals", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			CloudFinalizersNotRemovedCondition("FinalizerCollectionError",
				fmt.Sprintf("Error collecting cloud finalizers in namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	if len(removals) > 0 {
		return true, &actions{
			RemoveCloudResourceFinalizers: removals,
			Event:                         event("RemovingCloudFinalizers", "Removing finalizers from %d cloud resources in namespace %s", len(removals), namespaceName),
		}, nil
	}

	statusUpdate, needsUpdate := NewStatus(recovery.Status).
		WithConditions(
			CloudFinalizersRemovedCondition(recovery.Generation, time.Now()),
		).AsApplyConfiguration(recovery)
	if needsUpdate {
		return true, &actions{StatusUpdate: statusUpdate}, nil
	}
	return false, nil, nil
}

func (c *HCPRecoveryController) removeDeploymentResourceFinalizers(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error) {
	logger := klog.FromContext(ctx)

	for _, condition := range recovery.Status.Conditions {
		if condition.Type == hcprecoveryv1alpha1.ConditionNamespaceFullyRemoved && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
		if condition.Type == hcprecoveryv1alpha1.ConditionDeploymentFinalizersRemoved && condition.Status == metav1.ConditionTrue {
			return false, nil, nil
		}
	}

	hostedCluster, err := c.getHostedCluster(ctx, recovery.Spec.ClusterId)
	if err != nil {
		logger.Error(err, "Error retrieving HostedCluster")
		return c.handleRetryableError(recovery,
			DeploymentFinalizersNotRemovedCondition("HostedClusterRetrievalError",
				fmt.Sprintf("Error retrieving HostedCluster for cluster %s: %v", recovery.Spec.ClusterId, err),
				recovery.Generation, time.Now()), err)
	}

	if hostedCluster == nil {
		statusUpdate, needsUpdate := NewStatus(recovery.Status).
			WithConditions(
				DeploymentFinalizersRemovedCondition(recovery.Generation, time.Now()),
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
			statusUpdate, needsUpdate := NewStatus(recovery.Status).
				WithConditions(
					DeploymentFinalizersRemovedCondition(recovery.Generation, time.Now()),
				).AsApplyConfiguration(recovery)
			if needsUpdate {
				return true, &actions{StatusUpdate: statusUpdate}, nil
			}
			return false, nil, nil
		}
		logger.Error(err, "Error retrieving namespace", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			DeploymentFinalizersNotRemovedCondition("NamespaceRetrievalError",
				fmt.Sprintf("Error retrieving namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	if namespace.Status.Phase != v1.NamespaceTerminating {
		return c.handlePermanentError(recovery,
			DeploymentFinalizersNotRemovedCondition("NamespaceNotTerminating",
				fmt.Sprintf("Namespace %s is not terminating, cannot remove finalizers", namespaceName),
				recovery.Generation, time.Now()))
	}

	removals, err := c.collectDeploymentFinalizerRemovals(ctx, namespaceName)
	if err != nil {
		logger.Error(err, "Error collecting deployment finalizer removals", "namespace", namespaceName)
		return c.handleRetryableError(recovery,
			DeploymentFinalizersNotRemovedCondition("FinalizerCollectionError",
				fmt.Sprintf("Error collecting deployment finalizers in namespace %s: %v", namespaceName, err),
				recovery.Generation, time.Now()), err)
	}

	if len(removals) > 0 {
		return true, &actions{
			RemoveDeploymentResourceFinalizers: removals,
			Event:                              event("RemovingDeploymentFinalizers", "Removing finalizers from %d deployments in namespace %s", len(removals), namespaceName),
		}, nil
	}

	statusUpdate, needsUpdate := NewStatus(recovery.Status).
		WithConditions(
			DeploymentFinalizersRemovedCondition(recovery.Generation, time.Now()),
		).AsApplyConfiguration(recovery)
	if needsUpdate {
		return true, &actions{StatusUpdate: statusUpdate}, nil
	}
	return false, nil, nil
}
