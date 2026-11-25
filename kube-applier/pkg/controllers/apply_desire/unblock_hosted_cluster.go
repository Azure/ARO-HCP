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
}

var (
	deploymentsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	clustersGVR    = schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "clusters"}
	nodePoolsGVR   = schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "nodepools"}
)

const (
	hypershiftComponentFinalizer = "hypershift.openshift.io/component-finalizer"
	hypershiftFinalizer          = "hypershift.openshift.io/finalizer"
	clusterFinalizer             = "cluster.cluster.x-k8s.io"
)

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
// deletionTimestamp. It removes one known finalizer layer at a time, retiring
// the control plane before the HostedCluster namespace teardown can continue.
func (c *ApplyDesireController) evaluateUnblockHostedCluster(ctx context.Context, d *kubeapplierapi.ApplyDesire) deleteEvaluation {
	cfg := d.Spec.UnblockHostedCluster
	target := d.Spec.TargetItem
	if cfg == nil || len(cfg.NamespaceUID) == 0 || len(cfg.HostedClusterUID) == 0 || len(cfg.HostedClusterName) == 0 ||
		len(target.Group) != 0 || target.Version != "v1" || target.Resource != "namespaces" || len(target.Namespace) != 0 {
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
		return unblockRecreated("HostedCluster namespace UID changed from %q to %q", cfg.NamespaceUID, ns.GetUID())
	}
	deletedAt := ns.GetDeletionTimestamp()
	if deletedAt == nil || time.Since(deletedAt.Time) < cfg.MinimumDwell.Duration {
		return unblockBlocked("HostedCluster namespace is not Terminating beyond the required dwell")
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
		return unblockRecreated("HostedCluster identity in namespace %q differs from the pinned name and UID", target.Name)
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
		if len(cfg.ControlPlaneNamespaceUID) == 0 {
			return unblockBlocked("control-plane namespace %q is not the pinned Terminating namespace", relationship.ControlPlaneNamespace)
		}
		if string(cpNamespace.GetUID()) != cfg.ControlPlaneNamespaceUID {
			return unblockRecreated("control-plane namespace %q UID changed from %q to %q", relationship.ControlPlaneNamespace, cfg.ControlPlaneNamespaceUID, cpNamespace.GetUID())
		}
		if cpNamespace.GetDeletionTimestamp() == nil {
			return unblockBlocked("control-plane namespace %q is not Terminating", relationship.ControlPlaneNamespace)
		}
		clusterID := clusterIDFromNamespace(target.Name)
		for _, entry := range capiFinalizerTargets {
			objects, err := listAll(ctx, c.dyn.Resource(entry.gvr).Namespace(relationship.ControlPlaneNamespace))
			if err != nil {
				return unblockError(fmt.Errorf("list %s: %w", entry.gvr.Resource, err))
			}
			for _, object := range objects {
				if !belongsToCluster(&object, clusterID, cfg.HostedClusterName) {
					return unblockRecreated("unexpected %s %q in control-plane namespace", entry.gvr.Resource, object.GetName())
				}
				if result := clearExpectedFinalizer(ctx, c.dyn.Resource(entry.gvr).Namespace(relationship.ControlPlaneNamespace), &object, entry.finalizer); result != nil {
					return *result
				}
			}
			if len(objects) > 0 {
				return unblockWaiting("waiting for %s in control-plane namespace to disappear before clearing parent finalizers", entry.gvr.Resource)
			}
		}

		// These Deployments lack CAPI ownership metadata, so match by name in the pinned namespace.
		deployments, err := listAll(ctx, c.dyn.Resource(deploymentsGVR).Namespace(relationship.ControlPlaneNamespace))
		if err != nil {
			return unblockError(fmt.Errorf("list Deployments: %w", err))
		}
		knownComponentDeploymentPresent := false
		for _, deployment := range deployments {
			finalizers := deployment.GetFinalizers()
			knownName := deployment.GetName() == "capi-provider" || deployment.GetName() == "cluster-api"
			if containsString(finalizers, hypershiftComponentFinalizer) && (!knownName || len(finalizers) != 1) {
				return unblockRecreated("Deployment %q in control-plane namespace is not owned by this cleanup request", deployment.GetName())
			}
			if !knownName {
				continue
			}
			knownComponentDeploymentPresent = true
			if containsString(finalizers, hypershiftComponentFinalizer) {
				if result := clearExpectedFinalizer(ctx, c.dyn.Resource(deploymentsGVR).Namespace(relationship.ControlPlaneNamespace), &deployment, hypershiftComponentFinalizer); result != nil {
					return *result
				}
			}
		}
		if knownComponentDeploymentPresent {
			return unblockWaiting("waiting for capi-provider and cluster-api Deployments in control-plane namespace to disappear")
		}

		clusters, err := listAll(ctx, c.dyn.Resource(clustersGVR).Namespace(relationship.ControlPlaneNamespace))
		if err != nil {
			return unblockError(fmt.Errorf("list CAPI Clusters: %w", err))
		}
		for _, cluster := range clusters {
			if cluster.GetName() != clusterID {
				return unblockRecreated("unexpected CAPI Cluster %q in control-plane namespace", cluster.GetName())
			}
			if result := clearExpectedFinalizer(ctx, c.dyn.Resource(clustersGVR).Namespace(relationship.ControlPlaneNamespace), &cluster, clusterFinalizer); result != nil {
				return *result
			}
		}
		if len(clusters) > 0 {
			return unblockWaiting("waiting for the CAPI Cluster to disappear before clearing HostedControlPlane finalizers")
		}

		hcps, err := listAll(ctx, c.dyn.Resource(hostedControlPlanesGVR).Namespace(relationship.ControlPlaneNamespace))
		if err != nil {
			return unblockError(fmt.Errorf("list HostedControlPlanes: %w", err))
		}
		for _, hcp := range hcps {
			if hcp.GetName() != cfg.HostedClusterName || !createdBeforeDeletion(&hcp, deletedAt) || !hostedControlPlaneOwnedByCluster(&hcp, clusterID) {
				return unblockRecreated("HostedControlPlane %q does not match the pinned cleanup identity", hcp.GetName())
			}
			if result := clearExpectedFinalizer(ctx, c.dyn.Resource(hostedControlPlanesGVR).Namespace(relationship.ControlPlaneNamespace), &hcp, hypershiftFinalizer); result != nil {
				return *result
			}
		}
		if len(hcps) > 0 {
			return unblockWaiting("waiting for HostedControlPlane to disappear before clearing NodePool finalizers")
		}

		// NodePools and the HostedCluster cannot be touched while this
		// namespace remains, even if all known control-plane layers are gone.
		cpNamespace, err = c.dyn.Resource(namespacesGVR).Get(ctx, relationship.ControlPlaneNamespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			cpNamespace = nil
		} else if err != nil {
			return unblockError(fmt.Errorf("recheck control-plane namespace: %w", err))
		} else if len(cfg.ControlPlaneNamespaceUID) == 0 || string(cpNamespace.GetUID()) != cfg.ControlPlaneNamespaceUID {
			return unblockRecreated("control-plane namespace %q was recreated during cleanup", relationship.ControlPlaneNamespace)
		}
		if cpNamespace != nil {
			return unblockControlPlaneWaiting("known control-plane layers are gone; waiting for control-plane namespace %q to disappear", relationship.ControlPlaneNamespace)
		}
	}

	nodePools, err := listAll(ctx, c.dyn.Resource(nodePoolsGVR).Namespace(target.Name))
	if err != nil {
		return unblockError(fmt.Errorf("list NodePools: %w", err))
	}
	for _, nodePool := range nodePools {
		clusterName, _, _ := unstructured.NestedString(nodePool.Object, "spec", "clusterName")
		if clusterName != cfg.HostedClusterName {
			return unblockRecreated("NodePool %q belongs to unexpected HostedCluster %q", nodePool.GetName(), clusterName)
		}
		if result := clearExpectedFinalizer(ctx, c.dyn.Resource(nodePoolsGVR).Namespace(target.Name), &nodePool, hypershiftFinalizer); result != nil {
			return *result
		}
	}
	if len(nodePools) > 0 {
		return unblockWaiting("waiting for NodePools to disappear before clearing HostedCluster finalizer")
	}
	if result := clearExpectedFinalizer(ctx, c.dyn.Resource(hostedClustersGVR).Namespace(target.Name), hc, hypershiftFinalizer); result != nil {
		return *result
	}
	return unblockWaiting("HostedCluster finalizer is clear; waiting for object removal")
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func createdBeforeDeletion(object *unstructured.Unstructured, deletionTimestamp *metav1.Time) bool {
	created := object.GetCreationTimestamp()
	return deletionTimestamp != nil && !created.IsZero() && created.Before(deletionTimestamp)
}

func hostedControlPlaneOwnedByCluster(hcp *unstructured.Unstructured, clusterID string) bool {
	owners := hcp.GetOwnerReferences()
	if len(owners) == 0 {
		return true
	}
	for _, owner := range owners {
		if owner.Kind == "Cluster" && owner.Name == clusterID {
			return true
		}
	}
	return false
}

func unblockRecreated(format string, args ...any) deleteEvaluation {
	return unblockResult(metav1.ConditionFalse, kubeapplierapi.ConditionReasonRecreatedDuringCleanup, fmt.Sprintf(format, args...), nil, true)
}

func unblockControlPlaneWaiting(format string, args ...any) deleteEvaluation {
	return unblockResult(metav1.ConditionFalse, kubeapplierapi.ConditionReasonWaitingForControlPlaneNamespace, fmt.Sprintf(format, args...), nil, true)
}

func belongsToCluster(object *unstructured.Unstructured, clusterID, hostedClusterName string) bool {
	if label := object.GetLabels()["cluster.x-k8s.io/cluster-name"]; len(label) != 0 {
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
