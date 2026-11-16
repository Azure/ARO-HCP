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

package apply_desire

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
)

const unblockPoll = 15 * time.Second

var capiFinalizerTargets = []struct {
	gvr       schema.GroupVersionResource
	finalizer string
}{
	{schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machines"}, "machine.cluster.x-k8s.io"},
	{schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machinesets"}, "cluster.x-k8s.io/machineset"},
	{schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machinedeployments"}, "cluster.x-k8s.io/machinedeployment"},
	{schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "clusters"}, "cluster.cluster.x-k8s.io"},
}

var nodePoolsGVR = schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "nodepools"}

func unblockResult(status metav1.ConditionStatus, reason, message string, err error, requeue bool) deleteEvaluation {
	result := deleteEvaluation{mutate: func(d *kubeapplierapi.ApplyDesire) {
		for _, conditionType := range []string{kubeapplierapi.ConditionTypeHostedClusterUnblocked, kubeapplierapi.ConditionTypeSuccessful} {
			apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
				Type: conditionType, Status: status, Reason: reason, Message: message,
			})
		}
		conditions.SetDegraded(&d.Status.Conditions, err)
	}}
	if requeue {
		result.requeueAfter = unblockPoll
	}
	return result
}

func unblockBlocked(format string, args ...any) deleteEvaluation {
	return unblockResult(metav1.ConditionFalse, "PreconditionFailed", fmt.Sprintf(format, args...), nil, true)
}

func unblockWaiting(format string, args ...any) deleteEvaluation {
	return unblockResult(metav1.ConditionFalse, "WaitingForRemoval", fmt.Sprintf(format, args...), nil, true)
}

func unblockError(err error) deleteEvaluation {
	return unblockResult(metav1.ConditionFalse, "KubeAPIError", err.Error(), err, true)
}

// evaluateUnblockHostedCluster handles only objects that already have a
// deletionTimestamp. It removes one known finalizer at a time, from worker
// CAPI resources, NodePools, then HostedCluster. HostedControlPlane and the
// control-plane namespace are never patched by this operation.
func (c *ApplyDesireController) evaluateUnblockHostedCluster(ctx context.Context, d *kubeapplierapi.ApplyDesire) deleteEvaluation {
	cfg := d.Spec.UnblockHostedCluster
	target := d.Spec.TargetItem
	if cfg == nil || cfg.NamespaceUID == "" || cfg.HostedClusterUID == "" || cfg.HostedClusterName == "" ||
		target.Group != "" || target.Version != "v1" || target.Resource != "namespaces" || target.Namespace != "" {
		return unblockBlocked("invalid UnblockHostedCluster identity or namespace target")
	}
	relationship, err := fleetapihelpers.RelateHostedClusterNamespace(target.Name, cfg.HostedClusterName, "")
	if err != nil {
		return unblockBlocked("invalid HostedCluster namespace relationship: %v", err)
	}
	if cfg.AuthorizationExpiresAt.IsZero() || !time.Now().Before(cfg.AuthorizationExpiresAt.Time) {
		return unblockBlocked("backend authorization has expired; waiting for fresh Cosmos and Azure checks")
	}

	ns, err := c.dyn.Resource(namespacesGVR).Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return unblockResult(metav1.ConditionTrue, "NoErrors", "HostedCluster namespace is absent", nil, false)
	}
	if err != nil {
		return unblockError(fmt.Errorf("get HostedCluster namespace: %w", err))
	}
	if string(ns.GetUID()) != cfg.NamespaceUID {
		return unblockBlocked("HostedCluster namespace UID changed from %q to %q", cfg.NamespaceUID, ns.GetUID())
	}
	deletedAt := ns.GetDeletionTimestamp()
	if deletedAt == nil || time.Since(deletedAt.Time) < cfg.MinimumDwell.Duration {
		return unblockBlocked("HostedCluster namespace is not Terminating beyond the required dwell")
	}
	if blocker, err := c.legacyOwnerBlock(ctx, relationship.HostedClusterNamespace, relationship.ControlPlaneNamespace); err != nil {
		return unblockError(err)
	} else if blocker != "" {
		return unblockBlocked("%s; cleanup must stop", blocker)
	}

	hostedClusters, err := listAll(ctx, c.dyn.Resource(hostedClustersGVR).Namespace(target.Name))
	if err != nil {
		return unblockError(fmt.Errorf("list HostedClusters: %w", err))
	}
	if len(hostedClusters) == 0 {
		return unblockResult(metav1.ConditionTrue, "NoErrors", "HostedCluster is absent", nil, false)
	}
	if len(hostedClusters) != 1 || hostedClusters[0].GetName() != cfg.HostedClusterName ||
		string(hostedClusters[0].GetUID()) != cfg.HostedClusterUID {
		return unblockBlocked("HostedCluster identity in namespace %q differs from the pinned name and UID", target.Name)
	}
	hc := &hostedClusters[0]
	if hc.GetDeletionTimestamp() == nil {
		return unblockBlocked("HostedCluster %q is not already deleting", hc.GetName())
	}

	cpNamespace, err := c.dyn.Resource(namespacesGVR).Get(ctx, relationship.ControlPlaneNamespace, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return unblockError(fmt.Errorf("get control-plane namespace: %w", err))
	}
	if err == nil {
		if cfg.ControlPlaneNamespaceUID == "" || string(cpNamespace.GetUID()) != cfg.ControlPlaneNamespaceUID ||
			cpNamespace.GetDeletionTimestamp() == nil {
			return unblockBlocked("control-plane namespace %q is not the pinned Terminating namespace", relationship.ControlPlaneNamespace)
		}
		clusterID := clusterIDFromNamespace(target.Name)
		for _, entry := range capiFinalizerTargets {
			objects, err := listAll(ctx, c.dyn.Resource(entry.gvr).Namespace(relationship.ControlPlaneNamespace))
			if err != nil {
				return unblockError(fmt.Errorf("list %s: %w", entry.gvr.Resource, err))
			}
			for _, object := range objects {
				if !belongsToCluster(&object, entry.gvr.Resource, clusterID, cfg.HostedClusterName) {
					return unblockBlocked("unexpected %s %q in control-plane namespace", entry.gvr.Resource, object.GetName())
				}
				if result := clearExpectedFinalizer(ctx, c.dyn.Resource(entry.gvr).Namespace(relationship.ControlPlaneNamespace), &object, entry.finalizer); result != nil {
					return *result
				}
			}
			if len(objects) > 0 {
				return unblockWaiting("waiting for %s in control-plane namespace to disappear before clearing parent finalizers", entry.gvr.Resource)
			}
		}
	}

	nodePools, err := listAll(ctx, c.dyn.Resource(nodePoolsGVR).Namespace(target.Name))
	if err != nil {
		return unblockError(fmt.Errorf("list NodePools: %w", err))
	}
	for _, nodePool := range nodePools {
		clusterName, _, _ := unstructured.NestedString(nodePool.Object, "spec", "clusterName")
		if clusterName != cfg.HostedClusterName {
			return unblockBlocked("NodePool %q belongs to unexpected HostedCluster %q", nodePool.GetName(), clusterName)
		}
		if result := clearExpectedFinalizer(ctx, c.dyn.Resource(nodePoolsGVR).Namespace(target.Name), &nodePool, "hypershift.openshift.io/finalizer"); result != nil {
			return *result
		}
	}
	if len(nodePools) > 0 {
		return unblockWaiting("waiting for NodePools to disappear before clearing HostedCluster finalizer")
	}
	if result := clearExpectedFinalizer(ctx, c.dyn.Resource(hostedClustersGVR).Namespace(target.Name), hc, "hypershift.openshift.io/finalizer"); result != nil {
		return *result
	}
	return unblockWaiting("HostedCluster finalizer is clear; waiting for object removal")
}

func belongsToCluster(object *unstructured.Unstructured, kind, clusterID, hostedClusterName string) bool {
	if kind == "clusters" {
		return object.GetName() == clusterID
	}
	if label := object.GetLabels()["cluster.x-k8s.io/cluster-name"]; label != "" {
		return label == clusterID
	}
	return strings.HasPrefix(object.GetName(), hostedClusterName+"-")
}

// clearExpectedFinalizer returns nil when this object has no blocking
// finalizer. An Update uses the listed resourceVersion as a concurrency fence;
// an intervening replacement or finalizer change is retried from a new list.
func clearExpectedFinalizer(ctx context.Context, resource dynamic.ResourceInterface, object *unstructured.Unstructured, expected string) *deleteEvaluation {
	if object.GetDeletionTimestamp() == nil {
		result := unblockBlocked("%s %q is not already deleting", object.GetKind(), object.GetName())
		return &result
	}
	finalizers := object.GetFinalizers()
	if len(finalizers) == 0 {
		return nil
	}
	if len(finalizers) != 1 || finalizers[0] != expected {
		result := unblockBlocked("%s %q has unexpected finalizers %v", object.GetKind(), object.GetName(), finalizers)
		return &result
	}
	copy := object.DeepCopy()
	copy.SetFinalizers(nil)
	if _, err := resource.Update(ctx, copy, metav1.UpdateOptions{}); err != nil {
		result := unblockError(fmt.Errorf("clear %s finalizer on %s %q: %w", expected, object.GetKind(), object.GetName(), err))
		return &result
	}
	result := unblockWaiting("cleared %s finalizer on %s %q; rechecking teardown", expected, object.GetKind(), object.GetName())
	return &result
}
