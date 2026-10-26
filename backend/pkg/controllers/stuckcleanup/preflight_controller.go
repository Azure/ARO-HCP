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

// Package stuckcleanup inspects HostedCluster namespaces that are wedged in
// Terminating with no ARM Cosmos document and no Clusters Service record.
//
// Those orphans have nothing for an existing controller to trigger on, so the
// entry point is an explicit operator action: the admin API writes a
// StuckClusterCleanup document and this controller reconciles it.
//
// This release inspects only. It writes one ReadDesire so it can observe the
// namespace — a read of the management cluster, which changes nothing there —
// and records what it finds. It issues no ApplyDesire and deletes nothing.
//
// See docs/ops/stuck-cluster-cleanup-controller-proposal.md.
package stuckcleanup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	backendcontrollerutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// StuckClusterCleanupPreflightControllerName is the single source of truth
	// for this controller's name: the controller struct, the workqueue name (and
	// therefore the Prometheus label), the ctx controller name, and the logger
	// field all take their value from here.
	StuckClusterCleanupPreflightControllerName = "StuckClusterCleanupPreflight"

	// preflightResyncPeriod re-enqueues each cleanup so a namespace observation
	// that has not arrived yet is picked up without an external trigger.
	preflightResyncPeriod = 2 * time.Minute

	// readDesireNamePrefix namespaces the ReadDesires this controller creates
	// within the management cluster's desire collection, so they are
	// recognisable and cannot collide with another controller's desires.
	readDesireNamePrefix = "stuckclustercleanup-"

	// terminalRetentionPeriod is how long a settled request is kept before the
	// controller deletes it.
	//
	// Nothing else collects these documents, so some expiry is required. The
	// window is long enough that an operator who runs an inspection on Friday
	// can still read the findings the following week, and short enough that a
	// sweep of the fleet does not accumulate indefinitely.
	terminalRetentionPeriod = 14 * 24 * time.Hour
)

type preflightSyncer struct {
	fleetDBClient         fleetcosmosstorage.FleetDBClient
	resourcesDBClient     corecosmosstorage.ResourcesDBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
	kubeApplierDBClients  kubeappliercosmosstorage.KubeApplierDBClients
	readDesireLister      kubeapplierlisters.ReadDesireLister
	applyDesireLister     kubeapplierlisters.ApplyDesireLister
}

// NewStuckClusterCleanupPreflightController reconciles StuckClusterCleanup
// documents: it ensures a ReadDesire on the target namespace, runs the three
// preflight ownership checks, and records the findings on the document.
func NewStuckClusterCleanupPreflightController(
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	applyDesireLister kubeapplierlisters.ApplyDesireLister,
	fleetInformers fleetinformers.FleetInformers,
) *controllerutil.GenericWatchingController[backendcontrollerutils.StuckClusterCleanupKey] {
	syncer := &preflightSyncer{
		fleetDBClient:         fleetDBClient,
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
		kubeApplierDBClients:  kubeApplierDBClients,
		readDesireLister:      readDesireLister,
		applyDesireLister:     applyDesireLister,
	}

	return backendcontrollerutils.NewStuckClusterCleanupWatchingController(
		StuckClusterCleanupPreflightControllerName,
		fleetInformers,
		preflightResyncPeriod,
		syncer,
	)
}

// ReadDesireNameFor returns the ReadDesire name this controller uses for a
// given cleanup. It is derived from the cleanup's own name so the mapping is
// stable across restarts and recoverable without consulting status.
func ReadDesireNameFor(cleanupName string) string {
	return readDesireNamePrefix + cleanupName
}

// namespaceTargetFor builds the ReadDesire target for a namespace. Namespaces
// are cluster-scoped, so TargetItem.Namespace stays empty.
func namespaceTargetFor(namespaceName string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
		Name:     namespaceName,
	}
}

func (s *preflightSyncer) SyncOnce(ctx context.Context, key backendcontrollerutils.StuckClusterCleanupKey) error {
	cleanupCRUD := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).StuckClusterCleanups()

	existing, err := cleanupCRUD.Get(ctx, key.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The request is gone, but the ReadDesire it created is not owned by
		// anything else and nothing else will collect it, so drop it here.
		return s.deleteReadDesire(ctx, key)
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// A settled request has no further observation to do. Release the watch it
	// was holding, and retire the document itself once it has aged out.
	if existing.Status.Phase.IsTerminal() {
		return s.reconcileTerminal(ctx, cleanupCRUD, key, existing)
	}

	updated := existing.DeepCopy()

	observed, observeErr := s.ensureAndReadNamespace(ctx, key, updated)
	if observeErr != nil {
		setPhase(updated, fleetapi.StuckClusterCleanupPhaseFailed, fmt.Sprintf("could not observe namespace: %v", observeErr))
		return s.persist(ctx, cleanupCRUD, updated, existing, observeErr)
	}

	if observed == nil {
		setPhase(updated, fleetapi.StuckClusterCleanupPhaseObserving,
			fmt.Sprintf("waiting for the kube-applier to mirror namespace %q", updated.Spec.NamespaceName))
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    fleetapi.ConditionTypeStuckClusterCleanupNamespaceObserved,
			Status:  metav1.ConditionFalse,
			Reason:  "NotYetMirrored",
			Message: "the ReadDesire has been written but no namespace content has been observed yet",
		})
		return s.persist(ctx, cleanupCRUD, updated, existing, nil)
	}

	updated.Status.ObservedNamespace = observed
	apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
		Type:    fleetapi.ConditionTypeStuckClusterCleanupNamespaceObserved,
		Status:  metav1.ConditionTrue,
		Reason:  "Mirrored",
		Message: fmt.Sprintf("namespace %q observed in phase %q", updated.Spec.NamespaceName, observed.Phase),
	})

	// Pin the UID on first observation. Once pinned a changed UID means the
	// namespace was recreated, so this request no longer refers to the object
	// the operator asked about and must not proceed.
	switch {
	case len(updated.Spec.NamespaceUID) == 0:
		updated.Spec.NamespaceUID = observed.UID
	case updated.Spec.NamespaceUID != observed.UID:
		setPhase(updated, fleetapi.StuckClusterCleanupPhaseRefused, fmt.Sprintf(
			"namespace %q now has UID %q but this request pinned UID %q; it was recreated and this request no longer refers to it",
			updated.Spec.NamespaceName, observed.UID, updated.Spec.NamespaceUID))
		setPreflightRefused(updated, "NamespaceUIDChanged")
		return s.persist(ctx, cleanupCRUD, updated, existing, nil)
	}

	// Eligibility before ownership. A mistyped namespace name can land on a
	// healthy namespace that no cluster record refers to, in which case every
	// ownership check would pass; only this gate catches that.
	if verdict := evaluateEligibility(updated.Spec.NamespaceName, observed, time.Now()); !verdict.eligible {
		setPhase(updated, fleetapi.StuckClusterCleanupPhaseRefused, verdict.message)
		setIneligible(updated, verdict)
		return s.persist(ctx, cleanupCRUD, updated, existing, nil)
	}

	s.runPreflight(ctx, key, updated)
	return s.persist(ctx, cleanupCRUD, updated, existing, nil)
}

// reconcileTerminal handles a request that has settled.
//
// Two things have to happen, and neither happens on its own. The ReadDesire the
// controller created is released, because otherwise every invocation would
// leave a permanent informer watch on a namespace that no longer exists. Then,
// once the request has aged past the retention window, the document itself is
// deleted: it is stamp-rooted, so DeleteOrphanedCosmosResources cannot reach it
// and no other sweeper will.
//
// The ReadDesire goes first. Deleting the document first would lose the only
// reference to the desire and strand it permanently.
func (s *preflightSyncer) reconcileTerminal(
	ctx context.Context,
	cleanupCRUD cosmosstorageutils.ValidatingResourceCRUD[fleetapi.StuckClusterCleanup, *fleetapi.StuckClusterCleanup],
	key backendcontrollerutils.StuckClusterCleanupKey,
	cleanup *fleetapi.StuckClusterCleanup,
) error {
	logger := utils.LoggerFromContext(ctx)

	if err := s.deleteReadDesire(ctx, key); err != nil {
		return err
	}

	// A terminal request that predates this field, or one whose stamp was lost,
	// gets a fresh clock rather than being retired immediately.
	if cleanup.Status.CompletedAt == nil {
		updated := cleanup.DeepCopy()
		now := metav1.NewTime(time.Now())
		updated.Status.CompletedAt = &now
		return s.persist(ctx, cleanupCRUD, updated, cleanup, nil)
	}

	if time.Since(cleanup.Status.CompletedAt.Time) < terminalRetentionPeriod {
		return nil
	}

	logger.Info("retiring stuck cluster cleanup request past its retention window",
		"completedAt", cleanup.Status.CompletedAt.Time, "retentionPeriod", terminalRetentionPeriod)

	if err := cleanupCRUD.Delete(ctx, key.Name); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}
	return nil
}

// deleteReadDesire removes the namespace ReadDesire this controller created for
// the given request. It is idempotent: a missing desire is success.
func (s *preflightSyncer) deleteReadDesire(ctx context.Context, key backendcontrollerutils.StuckClusterCleanupKey) error {
	managementClusterResourceID, err := fleetapihelpers.ToManagementClusterResourceID(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	kubeApplierClient := s.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
		// The management cluster is not reachable from here. Retrying on the
		// next resync is correct; reporting an error would wedge the request.
		return nil
	}

	crud, err := kubeApplierClient.ReadDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	desireIDString := kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(
		key.StampIdentifier, ReadDesireNameFor(key.Name))
	if err := crud.Delete(ctx, desireIDString); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}
	return nil
}

// ensureAndReadNamespace writes the ReadDesire for the target namespace if it is
// missing or drifted, then returns the mirrored namespace if one is available.
// A nil result with a nil error means "not observed yet".
func (s *preflightSyncer) ensureAndReadNamespace(
	ctx context.Context,
	key backendcontrollerutils.StuckClusterCleanupKey,
	cleanup *fleetapi.StuckClusterCleanup,
) (*fleetapi.ObservedTerminatingNamespace, error) {
	managementClusterResourceID, err := fleetapihelpers.ToManagementClusterResourceID(key.StampIdentifier)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	kubeApplierClient := s.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
		return nil, fmt.Errorf("kube-applier Cosmos client not available for management cluster %q", key.StampIdentifier)
	}

	crud, err := kubeApplierClient.ReadDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	readDesireName := ReadDesireNameFor(key.Name)
	desireIDString := kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(key.StampIdentifier, readDesireName)
	desired := controllerutil.BuildReadDesire(desireIDString, managementClusterResourceID, namespaceTargetFor(cleanup.Spec.NamespaceName))

	existingDesire, err := s.readDesireLister.GetForManagementCluster(ctx, key.StampIdentifier, readDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		if _, createErr := crud.Create(ctx, desired, nil); createErr != nil {
			if cosmosstorageutils.IsConflictError(createErr) {
				return nil, nil
			}
			return nil, utils.TrackError(createErr)
		}
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if controllerutil.ReadDesireNeedsWork(existingDesire, desired) {
		replacement := existingDesire.DeepCopy()
		replacement.Spec = *desired.Spec.DeepCopy()
		if _, replaceErr := crud.Replace(ctx, replacement, nil); replaceErr != nil {
			if cosmosstorageutils.IsPreconditionFailedError(replaceErr) {
				// Another writer won the race; a resync reconciles any drift.
				return nil, nil
			}
			return nil, utils.TrackError(replaceErr)
		}
		return nil, nil
	}

	if existingDesire.Status.KubeContent == nil {
		return nil, nil
	}
	return distillNamespace(existingDesire.Status.KubeContent.Raw)
}

// runPreflight performs the three ownership checks and records the outcome.
//
// Each check is tri-state and preflight fails closed: a check that could not be
// performed stays nil and the overall verdict is a refusal, never a pass. The
// inspect release still records everything it learned, because the findings are
// the point of the release even when the verdict is "cannot confirm".
func (s *preflightSyncer) runPreflight(
	ctx context.Context,
	key backendcontrollerutils.StuckClusterCleanupKey,
	cleanup *fleetapi.StuckClusterCleanup,
) {
	logger := utils.LoggerFromContext(ctx)
	now := metav1.NewTime(time.Now())
	// Reaching here means eligibility already passed; record it so the GET
	// always carries an explicit verdict rather than an absent one.
	eligibleNamespace := true
	result := &fleetapi.StuckClusterCleanupPreflightResult{
		CheckedAt:         &now,
		NamespaceEligible: &eligibleNamespace,
	}

	// Check 1: ARM Cosmos. Only performed when the operator supplied the ARM
	// resource ID. Deriving one from the namespace name is deliberately not
	// attempted: a wrong derivation produces a spurious NotFound, which reads as
	// "safe to delete" — the one error direction that matters here.
	if cleanup.Spec.ClusterResourceID != nil {
		absent, err := s.armCosmosRecordAbsent(ctx, cleanup.Spec.ClusterResourceID)
		if err != nil {
			logger.Error(err, "ARM Cosmos preflight check failed", "clusterResourceID", cleanup.Spec.ClusterResourceID.String())
		} else {
			result.ARMCosmosRecordAbsent = &absent
		}
	}

	// Check 2: Clusters Service, under the same rule.
	if len(cleanup.Spec.ClusterServiceID) > 0 {
		absent, err := s.clusterServiceRecordAbsent(ctx, cleanup.Spec.ClusterServiceID)
		if err != nil {
			logger.Error(err, "Clusters Service preflight check failed", "clusterServiceID", cleanup.Spec.ClusterServiceID)
		} else {
			result.ClusterServiceRecordAbsent = &absent
		}
	}

	// Check 3: surviving publishers. This one needs no cluster identifier — it
	// asks whether anything still server-side-applies into the namespace — so it
	// always runs, and it is the check most likely to catch a live cluster.
	survivors, err := s.survivingApplyDesires(ctx, key.StampIdentifier, cleanup.Spec.NamespaceName)
	if err != nil {
		logger.Error(err, "surviving-desire preflight scan failed")
	} else {
		none := len(survivors) == 0
		result.NoSurvivingApplyDesires = &none
		result.SurvivingApplyDesireNames = survivors
	}

	cleanup.Status.Preflight = result

	switch {
	case result.NoSurvivingApplyDesires != nil && !*result.NoSurvivingApplyDesires:
		result.RefusalReason = "SurvivingApplyDesires"
		setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseRefused, fmt.Sprintf(
			"%d ServerSideApply desire(s) still publish into this namespace: %s",
			len(result.SurvivingApplyDesireNames), strings.Join(result.SurvivingApplyDesireNames, ", ")))
		setPreflightCondition(cleanup, metav1.ConditionFalse, "SurvivingApplyDesires",
			"something is still reconciling into this namespace")

	case result.ARMCosmosRecordAbsent != nil && !*result.ARMCosmosRecordAbsent:
		result.RefusalReason = "ARMCosmosRecordPresent"
		setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseRefused,
			"a live ARM Cosmos cluster document exists for this cluster")
		setPreflightCondition(cleanup, metav1.ConditionFalse, "ARMCosmosRecordPresent",
			"a live cluster document was found by point read")

	case result.ClusterServiceRecordAbsent != nil && !*result.ClusterServiceRecordAbsent:
		result.RefusalReason = "ClusterServiceRecordPresent"
		setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseRefused,
			"a live Clusters Service record exists for this cluster")
		setPreflightCondition(cleanup, metav1.ConditionFalse, "ClusterServiceRecordPresent",
			"a live CS record was found by point read")

	case result.ARMCosmosRecordAbsent == nil || result.ClusterServiceRecordAbsent == nil || result.NoSurvivingApplyDesires == nil:
		// Fails closed. Inspection findings are still recorded and reported.
		result.RefusalReason = "ChecksIncomplete"
		setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseInspected, strings.TrimSpace(fmt.Sprintf(
			"inspection complete; ownership could not be fully confirmed, so no mutation would be permitted. %s",
			missingCheckSummary(result, cleanup))))
		setPreflightCondition(cleanup, metav1.ConditionUnknown, "ChecksIncomplete",
			"one or more ownership checks could not be completed; preflight fails closed")

	default:
		setPhase(cleanup, fleetapi.StuckClusterCleanupPhaseInspected,
			"inspection complete; no live owner found")
		setPreflightCondition(cleanup, metav1.ConditionTrue, "NoLiveOwner",
			"ARM Cosmos, Clusters Service and the surviving-desire scan all found no live owner")
	}
}

func missingCheckSummary(result *fleetapi.StuckClusterCleanupPreflightResult, cleanup *fleetapi.StuckClusterCleanup) string {
	var missing []string
	if result.ARMCosmosRecordAbsent == nil {
		if cleanup.Spec.ClusterResourceID == nil {
			missing = append(missing, "ARM Cosmos (no spec.clusterResourceID supplied)")
		} else {
			missing = append(missing, "ARM Cosmos (point read failed)")
		}
	}
	if result.ClusterServiceRecordAbsent == nil {
		if len(cleanup.Spec.ClusterServiceID) == 0 {
			missing = append(missing, "Clusters Service (no spec.clusterServiceID supplied)")
		} else {
			missing = append(missing, "Clusters Service (point read failed)")
		}
	}
	if result.NoSurvivingApplyDesires == nil {
		missing = append(missing, "surviving-desire scan (scan failed)")
	}
	if len(missing) == 0 {
		return ""
	}
	return "Incomplete: " + strings.Join(missing, "; ") + "."
}

func (s *preflightSyncer) armCosmosRecordAbsent(ctx context.Context, clusterResourceID *azcorearm.ResourceID) (bool, error) {
	if clusterResourceID.Parent == nil || clusterResourceID.Parent.Parent == nil {
		return false, fmt.Errorf("cluster resource ID %q has no resource group parent", clusterResourceID.String())
	}
	subscriptionID := clusterResourceID.SubscriptionID
	resourceGroupName := clusterResourceID.ResourceGroupName

	_, err := s.resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterResourceID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(err)
	}
	return false, nil
}

func (s *preflightSyncer) clusterServiceRecordAbsent(ctx context.Context, clusterServiceID string) (bool, error) {
	internalID, err := metadataapi.NewInternalID(clusterServiceID)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("invalid clusterServiceID %q: %w", clusterServiceID, err))
	}

	_, err = s.clustersServiceClient.GetCluster(ctx, internalID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
			return true, nil
		}
		return false, utils.TrackError(err)
	}
	return false, nil
}

// survivingApplyDesires returns the names of ServerSideApply desires on this
// management cluster that still target the namespace. Delete desires are not
// publishers and are excluded.
func (s *preflightSyncer) survivingApplyDesires(ctx context.Context, stampIdentifier, namespaceName string) ([]string, error) {
	managementClusterResourceID, err := fleetapihelpers.ToManagementClusterResourceID(stampIdentifier)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	desires, err := s.applyDesireLister.ListForManagementCluster(ctx, managementClusterResourceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	var survivors []string
	for _, desire := range desires {
		if desire.Spec.Type != kubeapplierapi.ApplyDesireTypeServerSideApply {
			continue
		}
		// A desire targeting the namespace object itself counts too: something
		// is still asserting the namespace should exist.
		targetsNamespace := desire.Spec.TargetItem.Namespace == namespaceName ||
			(desire.Spec.TargetItem.Resource == "namespaces" && desire.Spec.TargetItem.Name == namespaceName)
		if !targetsNamespace {
			continue
		}
		if desire.GetResourceID() != nil {
			survivors = append(survivors, desire.GetResourceID().Name)
		}
	}
	return survivors, nil
}

// distillNamespace extracts the fields the findings need from the mirrored
// namespace object.
func distillNamespace(raw []byte) (*fleetapi.ObservedTerminatingNamespace, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(raw); err != nil {
		return nil, utils.TrackError(fmt.Errorf("could not decode mirrored namespace: %w", err))
	}

	observed := &fleetapi.ObservedTerminatingNamespace{
		UID: string(obj.GetUID()),
	}
	if deletionTimestamp := obj.GetDeletionTimestamp(); deletionTimestamp != nil {
		observed.DeletionTimestamp = deletionTimestamp
	}

	if phase, found, err := unstructured.NestedString(obj.Object, "status", "phase"); err == nil && found {
		observed.Phase = phase
	}
	if finalizers, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "finalizers"); err == nil && found {
		observed.SpecFinalizers = finalizers
	}

	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return observed, nil
	}

	for _, raw := range conditions {
		condMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(condMap, "type")
		condStatus, _, _ := unstructured.NestedString(condMap, "status")
		condReason, _, _ := unstructured.NestedString(condMap, "reason")
		condMessage, _, _ := unstructured.NestedString(condMap, "message")

		observed.NamespaceConditions = append(observed.NamespaceConditions, metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionStatus(condStatus),
			Reason:  condReason,
			Message: condMessage,
		})

		// NamespaceContentRemaining and NamespaceFinalizersRemaining spell the
		// blocking content out in their message as a comma-separated list of
		// group-qualified resources. That message is the only enumeration
		// available: a ReadDesire reads one object by exact name and there is no
		// list primitive, so this is how the inventory is obtained.
		if condType == "NamespaceContentRemaining" || condType == "NamespaceFinalizersRemaining" {
			observed.RemainingContent = append(observed.RemainingContent, parseRemainingContent(condMessage)...)
		}
	}

	observed.RemainingContent = dedupeRemainingContent(observed.RemainingContent)
	return observed, nil
}

// parseRemainingContent pulls group/resource pairs out of a kube namespace
// condition message such as:
//
//	"Some resources are remaining: poddisruptionbudgets.policy has 2 resource instances"
//	"Some content in the namespace has finalizers remaining: cluster.x-k8s.io/machine in 1 resource instances"
func parseRemainingContent(message string) []fleetapi.RemainingGroupResource {
	_, list, found := strings.Cut(message, ":")
	if !found {
		return nil
	}

	var out []fleetapi.RemainingGroupResource
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if len(entry) == 0 {
			continue
		}
		// Trim the trailing " has N resource instances" / " in N resource instances".
		if idx := strings.Index(entry, " "); idx > 0 {
			entry = entry[:idx]
		}
		if len(entry) == 0 {
			continue
		}

		var groupResource fleetapi.RemainingGroupResource
		switch {
		case strings.Contains(entry, "/"):
			// NamespaceFinalizersRemaining spells finalizers as group/name.
			group, name, _ := strings.Cut(entry, "/")
			groupResource = fleetapi.RemainingGroupResource{Group: group, Resource: name}
		case strings.Contains(entry, "."):
			// NamespaceContentRemaining spells content as resource.group.
			resource, group, _ := strings.Cut(entry, ".")
			groupResource = fleetapi.RemainingGroupResource{Group: group, Resource: resource}
		default:
			groupResource = fleetapi.RemainingGroupResource{Resource: entry}
		}

		permitted := kubeApplierCanDelete(groupResource.Group, groupResource.Resource)
		groupResource.KubeApplierPermitted = &permitted
		out = append(out, groupResource)
	}
	return out
}

func dedupeRemainingContent(in []fleetapi.RemainingGroupResource) []fleetapi.RemainingGroupResource {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]fleetapi.RemainingGroupResource, 0, len(in))
	for _, groupResource := range in {
		fingerprint := groupResource.Group + "/" + groupResource.Resource
		if _, duplicate := seen[fingerprint]; duplicate {
			continue
		}
		seen[fingerprint] = struct{}{}
		out = append(out, groupResource)
	}
	return out
}

// setPhase records the phase and its explanation, and starts the retention
// clock the first time the request settles. CompletedAt is only ever set once:
// re-stamping it on a later pass would keep pushing the expiry out and the
// document would never be retired.
func setPhase(cleanup *fleetapi.StuckClusterCleanup, phase fleetapi.StuckClusterCleanupPhase, message string) {
	cleanup.Status.Phase = phase
	cleanup.Status.Message = message
	if phase.IsTerminal() && cleanup.Status.CompletedAt == nil {
		now := metav1.NewTime(time.Now())
		cleanup.Status.CompletedAt = &now
	}
}

func setPreflightCondition(cleanup *fleetapi.StuckClusterCleanup, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&cleanup.Status.Conditions, metav1.Condition{
		Type:    fleetapi.ConditionTypeStuckClusterCleanupPreflightPassed,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

// setIneligible records an eligibility refusal. The namespace is not a valid
// target, so the ownership checks are not run at all and are left unset: a
// refusal here is definitive and does not need corroborating.
func setIneligible(cleanup *fleetapi.StuckClusterCleanup, verdict eligibilityVerdict) {
	if cleanup.Status.Preflight == nil {
		now := metav1.NewTime(time.Now())
		cleanup.Status.Preflight = &fleetapi.StuckClusterCleanupPreflightResult{CheckedAt: &now}
	}
	ineligible := false
	cleanup.Status.Preflight.NamespaceEligible = &ineligible
	cleanup.Status.Preflight.IneligibleReason = verdict.reason
	cleanup.Status.Preflight.RefusalReason = verdict.reason
	setPreflightCondition(cleanup, metav1.ConditionFalse, verdict.reason, verdict.message)
}

func setPreflightRefused(cleanup *fleetapi.StuckClusterCleanup, reason string) {
	if cleanup.Status.Preflight == nil {
		now := metav1.NewTime(time.Now())
		cleanup.Status.Preflight = &fleetapi.StuckClusterCleanupPreflightResult{CheckedAt: &now}
	}
	cleanup.Status.Preflight.RefusalReason = reason
	setPreflightCondition(cleanup, metav1.ConditionFalse, reason, cleanup.Status.Message)
}

// persist writes the document back, folding in any sync error so a failed
// observation is still reported to the operator rather than only logged.
func (s *preflightSyncer) persist(
	ctx context.Context,
	crud cosmosstorageutils.ValidatingResourceCRUD[fleetapi.StuckClusterCleanup, *fleetapi.StuckClusterCleanup],
	updated, existing *fleetapi.StuckClusterCleanup,
	syncErr error,
) error {
	if _, err := crud.Replace(ctx, updated, existing, nil); err != nil {
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// Another writer won the race; the resync reconciles any drift.
			return syncErr
		}
		return utils.TrackError(err)
	}
	return syncErr
}
