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
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
)

// kubernetesNamespaceFinalizer is the only spec.finalizers entry this
// controller will ever remove. It is the built-in entry the namespace
// controller uses to gate namespace removal on its own content sweep; clearing
// it is what unwedges a namespace whose sweep cannot finish.
const kubernetesNamespaceFinalizer = "kubernetes"

var (
	namespacesGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}

	hostedClustersGVR = schema.GroupVersionResource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters",
	}
	hostedControlPlanesGVR = schema.GroupVersionResource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedcontrolplanes",
	}
)

// finalizeRecheckPeriod is how long to wait before re-evaluating a
// FinalizeNamespace desire whose preconditions did not hold. A precondition
// failure is expected to resolve on its own (the dwell elapses, a stale
// observation refreshes), so this is a poll rather than a backoff.
const finalizeRecheckPeriod = time.Minute

// finalizeSettled records a terminal outcome: err == nil means the namespace is
// gone, anything else means the attempt failed and why.
func finalizeSettled(err error) deleteEvaluation {
	return deleteEvaluation{mutate: func(d *kubeapplierapi.ApplyDesire) {
		conditions.SetNamespaceFinalized(&d.Status.Conditions, err)
		conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
	}}
}

// finalizeBlocked records that a precondition did not hold and schedules a
// re-check. This is never a success and never terminal: every precondition is
// re-evaluated from scratch on the next pass.
func finalizeBlocked(format string, args ...any) deleteEvaluation {
	reason := fmt.Sprintf(format, args...)
	return deleteEvaluation{
		mutate: func(d *kubeapplierapi.ApplyDesire) {
			conditions.SetFinalizePreconditionFailed(&d.Status.Conditions, reason)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		},
		requeueAfter: finalizeRecheckPeriod,
	}
}

// evaluateFinalizeNamespace runs the state machine for one ApplyDesire with
// Type=FinalizeNamespace.
//
// It clears the built-in "kubernetes" finalizer from a namespace that is
// already Terminating, which deletes the namespace object. It issues no DELETE:
// a DELETE on a Terminating namespace succeeds and changes nothing, so there is
// no weaker step to try first and no stronger one to fall back on.
//
// Every precondition is re-read from the live API here rather than trusted from
// the backend's observation, because a ReadDesire does not guarantee a fresh
// snapshot at the instant the mutation runs. The checks run in order and any
// one of them blocks:
//
//  1. the pinned UID still matches — the namespace was not replaced;
//  2. deletionTimestamp is set and older than MinimumDwell;
//  3. spec.finalizers holds nothing but "kubernetes";
//  4. the related parent object is absent: HostedControlPlane for the
//     control-plane namespace, or HostedCluster for the HostedCluster namespace.
//
// Check 4 protects HostedCluster teardown, and it is why this cannot be a
// flag on Delete: it has no meaning for an ordinary deletion.
func (c *ApplyDesireController) evaluateFinalizeNamespace(ctx context.Context, d *kubeapplierapi.ApplyDesire) deleteEvaluation {
	target := d.Spec.TargetItem
	cfg := d.Spec.FinalizeNamespace

	if cfg == nil {
		return finalizeSettled(conditions.NewPreCheckError(
			errors.New("spec.finalizeNamespace is required when spec.type is FinalizeNamespace")))
	}
	if len(cfg.UID) == 0 {
		return finalizeSettled(conditions.NewPreCheckError(
			errors.New("spec.finalizeNamespace.uid is required: the namespace identity must be pinned before it can be finalized")))
	}
	if cfg.AuthorizationExpiresAt.IsZero() || !time.Now().Before(cfg.AuthorizationExpiresAt.Time) {
		return finalizeBlocked("backend authorization has expired; waiting for fresh Cosmos ownership checks")
	}
	if len(target.Name) == 0 {
		return finalizeSettled(conditions.NewPreCheckError(
			errors.New("spec.targetItem requires name")))
	}
	relationship, err := fleetapihelpers.RelateHostedClusterNamespace(cfg.HostedClusterNamespace, cfg.HostedClusterName, "")
	if err != nil {
		return finalizeSettled(conditions.NewPreCheckError(fmt.Errorf(
			"invalid HostedCluster namespace relationship: %v", err)))
	}
	var namespaceRole string
	switch target.Name {
	case relationship.HostedClusterNamespace:
		namespaceRole = "HostedClusterNamespace"
	case relationship.ControlPlaneNamespace:
		namespaceRole = "ControlPlaneNamespace"
	default:
		return finalizeSettled(conditions.NewPreCheckError(fmt.Errorf(
			"target namespace %q is not the related HostedCluster or control-plane namespace", target.Name)))
	}
	// Refuse anything that is not a cluster-scoped core/v1 Namespace. This
	// controller removes a finalizer from exactly one kind of object, and
	// accepting a different GVR would silently widen that.
	if target.Group != namespacesGVR.Group || target.Version != namespacesGVR.Version ||
		target.Resource != namespacesGVR.Resource || len(target.Namespace) > 0 {
		return finalizeSettled(conditions.NewPreCheckError(fmt.Errorf(
			"spec.targetItem must be a cluster-scoped core/v1 namespaces resource, got group=%q version=%q resource=%q namespace=%q",
			target.Group, target.Version, target.Resource, target.Namespace)))
	}

	namespaces := c.dyn.Resource(namespacesGVR)

	got, getErr := namespaces.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		// Already gone. This is the success path on every pass after the
		// finalize lands, so it must not be reported as an error.
		return finalizeSettled(nil)
	}
	if getErr != nil {
		return finalizeSettled(fmt.Errorf("get namespace: %w", getErr))
	}

	// 1. The pinned UID still matches.
	if observedUID := string(got.GetUID()); observedUID != cfg.UID {
		return finalizeBlocked(
			"namespace %q has uid %q but this request pinned %q; the namespace was replaced and this attempt is void",
			target.Name, observedUID, cfg.UID)
	}

	// 2. Terminating, and stuck for at least the dwell.
	deletionTimestamp := got.GetDeletionTimestamp()
	if deletionTimestamp == nil {
		return finalizeBlocked(
			"namespace %q has no deletionTimestamp; this operation finishes deletions and does not start them",
			target.Name)
	}
	if elapsed := time.Since(deletionTimestamp.Time); elapsed < cfg.MinimumDwell.Duration {
		return finalizeBlocked(
			"namespace %q has been terminating for %s, which is less than the required dwell of %s",
			target.Name, elapsed.Round(time.Second), cfg.MinimumDwell.Duration)
	}

	// 3. Nothing but the built-in finalizer. An extra entry means something
	// other than the namespace controller is holding the namespace, and
	// clearing that is not this operation's business.
	specFinalizers, _, err := unstructured.NestedStringSlice(got.Object, "spec", "finalizers")
	if err != nil {
		return finalizeSettled(conditions.NewPreCheckError(fmt.Errorf("read spec.finalizers: %w", err)))
	}
	if len(specFinalizers) == 0 {
		return finalizeBlocked(
			"namespace %q has no spec.finalizers left to clear; waiting for it to disappear", target.Name)
	}
	if len(specFinalizers) != 1 || specFinalizers[0] != kubernetesNamespaceFinalizer {
		return finalizeBlocked(
			"namespace %q has spec.finalizers %v; only the single %q entry may be removed",
			target.Name, specFinalizers, kubernetesNamespaceFinalizer)
	}

	if blocked := c.parentObjectBlocksFinalize(ctx, cfg, namespaceRole); blocked != nil {
		return *blocked
	}
	if namespaceRole == "HostedClusterNamespace" {
		_, err := namespaces.Get(ctx, relationship.ControlPlaneNamespace, metav1.GetOptions{})
		if err == nil {
			return finalizeBlocked("control-plane namespace %q still exists", relationship.ControlPlaneNamespace)
		}
		if !apierrors.IsNotFound(err) {
			return finalizeBlocked("could not confirm control-plane namespace absence: %v", err)
		}
	}
	if blocker := namespaceInventoryBlock(got); len(blocker) != 0 {
		return finalizeBlocked("namespace %q inventory is not clear: %s", target.Name, blocker)
	}

	// Use the resourceVersion from the read above so a namespace that changed
	// underneath us is rejected by the apiserver rather than overwritten.
	got.Object["spec"] = map[string]any{"finalizers": []any{}}

	if _, finErr := namespaces.Update(ctx, got, metav1.UpdateOptions{}, "finalize"); finErr != nil {
		if apierrors.IsNotFound(finErr) {
			return finalizeSettled(nil)
		}
		if apierrors.IsConflict(finErr) {
			// Re-observe rather than overwrite a newer namespace.
			return finalizeBlocked(
				"namespace %q changed while finalizing (resourceVersion conflict); re-observing",
				target.Name)
		}
		return finalizeSettled(fmt.Errorf("finalize namespace: %w", finErr))
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("cleared namespace finalizer",
		"namespace", target.Name, "uid", cfg.UID,
		"finalizersRemoved", specFinalizers,
		"terminatingFor", time.Since(deletionTimestamp.Time).Round(time.Second).String())

	// Confirm absence rather than assuming it: the desire is only successful
	// once the namespace is actually gone.
	if _, postErr := namespaces.Get(ctx, target.Name, metav1.GetOptions{}); apierrors.IsNotFound(postErr) {
		return finalizeSettled(nil)
	}
	return finalizeBlocked("namespace %q still present immediately after finalize; re-checking", target.Name)
}

func (c *ApplyDesireController) parentObjectBlocksFinalize(ctx context.Context, cfg *kubeapplierapi.FinalizeNamespaceConfig, namespaceRole string) *deleteEvaluation {
	if len(cfg.HostedClusterNamespace) == 0 || len(cfg.HostedClusterName) == 0 {
		blocked := finalizeSettled(conditions.NewPreCheckError(
			errors.New("spec.finalizeNamespace.hostedClusterNamespace and hostedClusterName are required")))
		return &blocked
	}

	if namespaceRole == "ControlPlaneNamespace" {
		hostedControlPlanes, err := listAll(ctx, c.dyn.Resource(hostedControlPlanesGVR).Namespace(cfg.HostedClusterNamespace+"-"+cfg.HostedClusterName))
		if err != nil {
			blocked := finalizeBlocked(
				"could not list HostedControlPlanes in control-plane namespace to confirm absence: %v", err)
			return &blocked
		}
		if len(hostedControlPlanes) > 0 {
			blocked := finalizeBlocked("HostedControlPlane %q in control-plane namespace still exists; waiting for its removal before namespace finalization", hostedControlPlanes[0].GetName())
			return &blocked
		}
		return nil
	}

	hostedClusters, err := listAll(ctx, c.dyn.Resource(hostedClustersGVR).Namespace(cfg.HostedClusterNamespace))
	if err != nil {
		blocked := finalizeBlocked(
			"could not list HostedClusters in namespace %q to confirm absence: %v",
			cfg.HostedClusterNamespace, err)
		return &blocked
	}
	for _, hc := range hostedClusters {
		blocked := finalizeBlocked(
			"HostedCluster %q in namespace %q still exists (deletionTimestamp=%v); waiting for its removal before namespace finalization",
			hc.GetName(), cfg.HostedClusterNamespace, hc.GetDeletionTimestamp())
		return &blocked
	}
	return nil
}

// namespaceInventoryBlock uses the namespace controller's own discovery and
// content sweep result. These conditions are read live immediately before the
// /finalize update; missing or positive conditions do not authorize bypassing
// the namespace controller's ordinary cleanup.
func namespaceInventoryBlock(namespace *unstructured.Unstructured) string {
	conditions, found, err := unstructured.NestedSlice(namespace.Object, "status", "conditions")
	if err != nil || !found {
		return "namespace controller discovery conditions are unavailable"
	}
	required := map[string]bool{
		"NamespaceDeletionDiscoveryFailure":           false,
		"NamespaceDeletionGroupVersionParsingFailure": false,
		"NamespaceDeletionContentFailure":             false,
		"NamespaceContentRemaining":                   false,
		"NamespaceFinalizersRemaining":                false,
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := condition["type"].(string)
		if _, needed := required[typeName]; !needed {
			continue
		}
		status, _ := condition["status"].(string)
		if status != "False" {
			return fmt.Sprintf("%s is %q: %v", typeName, status, condition["message"])
		}
		required[typeName] = true
	}
	for typeName, observed := range required {
		if !observed {
			return fmt.Sprintf("%s has not reported successful discovery and cleanup", typeName)
		}
	}
	return ""
}
