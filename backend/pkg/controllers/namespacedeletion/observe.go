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

package namespacedeletion

import (
	"context"
	"encoding/json"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	backendcontrollerutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// namespaceObservation is the distilled live state of the target namespace.
//
// pending and absent are kept apart on purpose. The ReadDesire contract is that
// Successful=True with nil KubeContent proves the target is gone, while an
// unready or errored observation proves nothing at all. Collapsing the two
// would let a transient read failure authorize an irreversible mutation.
type namespaceObservation struct {
	pending bool
	absent  bool

	uid               string
	phase             string
	deletionTimestamp *metav1.Time
	specFinalizers    []string
	conditions        []metav1.Condition
}

// hostedClusterObservation is the distilled live state of the HostedCluster in
// the related namespace.
type hostedClusterObservation struct {
	pending bool
	present bool

	name              string
	uid               string
	deletionTimestamp *metav1.Time
	finalizers        []string

	// livenessReason is non-empty when the HostedCluster looks like a working
	// cluster. It is the human-readable evidence, and its emptiness is never
	// treated as proof of death — only its presence is acted on.
	livenessReason string
}

func (s *namespaceDeletionSyncer) observeNamespace(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
) (namespaceObservation, error) {
	raw, state, err := s.ensureReadDesire(ctx, key,
		ReadDesireNameFor(key.Name), namespaceTargetFor(request.Spec.HostedClusterNamespaceName))
	if err != nil {
		return namespaceObservation{}, err
	}
	switch state {
	case observationPending:
		return namespaceObservation{pending: true}, nil
	case observationAbsent:
		return namespaceObservation{absent: true}, nil
	}

	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(raw); err != nil {
		return namespaceObservation{}, fmt.Errorf("decode observed namespace: %w", err)
	}

	observation := namespaceObservation{
		uid:               string(obj.GetUID()),
		deletionTimestamp: obj.GetDeletionTimestamp(),
	}
	observation.phase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")
	observation.specFinalizers, _, _ = unstructured.NestedStringSlice(obj.Object, "spec", "finalizers")
	observation.conditions = namespaceConditions(obj)
	return observation, nil
}

// namespaceConditions carries the namespace's own status.conditions verbatim.
//
// These conditions, including NamespaceDeletionDiscoveryFailure and
// NamespaceContentRemaining, are logged with the cleanup decision.
func namespaceConditions(obj *unstructured.Unstructured) []metav1.Condition {
	rawConditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return nil
	}
	encoded, err := json.Marshal(rawConditions)
	if err != nil {
		return nil
	}
	var conditions []metav1.Condition
	if err := json.Unmarshal(encoded, &conditions); err != nil {
		// Namespace conditions use the same shape as metav1.Condition but are
		// not guaranteed to carry every required field. Evidence we cannot
		// parse is dropped rather than failing the reconcile over it.
		return nil
	}
	return conditions
}

func (s *namespaceDeletionSyncer) observeHostedCluster(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
	relationship *fleetapihelpers.NamespaceRelationship,
) (hostedClusterObservation, error) {
	target := hostedClusterTargetFor(relationship.HostedClusterNamespace, request.Spec.HostedClusterName)
	raw, state, err := s.ensureReadDesire(ctx, key, HostedClusterReadDesireNameFor(key.Name), target)
	if err != nil {
		return hostedClusterObservation{}, err
	}
	switch state {
	case observationPending:
		return hostedClusterObservation{pending: true}, nil
	case observationAbsent:
		return hostedClusterObservation{}, nil
	}

	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(raw); err != nil {
		return hostedClusterObservation{}, fmt.Errorf("decode observed HostedCluster: %w", err)
	}

	observation := hostedClusterObservation{
		present:           true,
		name:              obj.GetName(),
		uid:               string(obj.GetUID()),
		deletionTimestamp: obj.GetDeletionTimestamp(),
		finalizers:        obj.GetFinalizers(),
	}
	observation.livenessReason = hostedClusterLivenessReason(obj)
	return observation, nil
}

// hostedClusterLivenessReason reports why a HostedCluster looks like a working
// cluster, or "" when none of the signals is present.
//
// The gate is deliberately one-directional: it can only ever refuse, never
// authorize. Absence of these signals is not proof the cluster is dead — a
// HostedCluster whose status is empty, stale or unreadable simply does not
// trigger the refusal. The dwell and ownership gates still apply.
func hostedClusterLivenessReason(obj *unstructured.Unstructured) string {
	if conditions := namespaceConditions(obj); apimeta.IsStatusConditionTrue(conditions, string(hyperv1.HostedClusterAvailable)) {
		return fmt.Sprintf("status condition %q is True", hyperv1.HostedClusterAvailable)
	}

	history, found, err := unstructured.NestedSlice(obj.Object, "status", "version", "history")
	if err == nil && found {
		for _, entry := range history {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if state, _ := item["state"].(string); state == "Completed" {
				version, _ := item["version"].(string)
				return fmt.Sprintf("status.version.history records a completed rollout of %q", version)
			}
		}
	}

	host, _, _ := unstructured.NestedString(obj.Object, "status", "controlPlaneEndpoint", "host")
	if len(host) > 0 {
		return fmt.Sprintf("status.controlPlaneEndpoint.host is %q", host)
	}

	return ""
}

type observationState int

const (
	// observationReady means KubeContent holds a live object.
	observationReady observationState = iota

	// observationPending means no valid observation has arrived. This proves
	// nothing about the target and must never be read as absence.
	observationPending

	// observationAbsent means the kube-applier confirmed the target is gone:
	// Successful=True with nil KubeContent.
	observationAbsent
)

// ensureReadDesire writes the ReadDesire for a target if it is missing or has
// drifted, then reports what has been observed through it.
func (s *namespaceDeletionSyncer) ensureReadDesire(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	desireName string,
	target kubeapplierapi.ResourceReference,
) ([]byte, observationState, error) {
	managementClusterResourceID, err := s.managementCluster(key)
	if err != nil {
		return nil, observationPending, utils.TrackError(err)
	}

	kubeApplierClient, err := s.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if err != nil {
		return nil, observationPending, utils.TrackError(err)
	}
	if kubeApplierClient == nil {
		return nil, observationPending, fmt.Errorf(
			"kube-applier Cosmos client not available for management cluster %q", key.StampIdentifier)
	}

	crud, err := kubeApplierClient.ReadDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return nil, observationPending, utils.TrackError(err)
	}

	desireIDString := kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(key.StampIdentifier, desireName)
	desired := controllerutil.BuildReadDesire(desireIDString, managementClusterResourceID, target)

	existing, err := s.readDesireLister.GetForManagementCluster(ctx, key.StampIdentifier, desireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		if _, createErr := crud.Create(ctx, desired, nil); createErr != nil {
			if cosmosstorageutils.IsConflictError(createErr) {
				return nil, observationPending, nil
			}
			return nil, observationPending, utils.TrackError(createErr)
		}
		return nil, observationPending, nil
	}
	if err != nil {
		return nil, observationPending, utils.TrackError(err)
	}

	if controllerutil.ReadDesireNeedsWork(existing, desired) {
		replacement := existing.DeepCopy()
		replacement.Spec = *desired.Spec.DeepCopy()
		if _, replaceErr := crud.Replace(ctx, replacement, nil); replaceErr != nil {
			if cosmosstorageutils.IsPreconditionFailedError(replaceErr) {
				return nil, observationPending, nil
			}
			return nil, observationPending, utils.TrackError(replaceErr)
		}
		return nil, observationPending, nil
	}

	// Only Successful=True proves anything. An unready or errored observation
	// is pending, not absent.
	if !apimeta.IsStatusConditionTrue(existing.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful) {
		return nil, observationPending, nil
	}
	if existing.Status.KubeContent == nil || len(existing.Status.KubeContent.Raw) == 0 {
		return nil, observationAbsent, nil
	}
	return existing.Status.KubeContent.Raw, observationReady, nil
}
