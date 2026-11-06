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

// Package namespacedeletion retires HostedCluster namespaces that are wedged in
// Terminating with no live Cosmos cluster document.
//
// Those orphans have nothing for an existing controller to trigger on, so the
// entry point is an explicit operator action: the admin API writes a
// NamespaceDeletion document and this controller reconciles it.
//
// The controller finishes deletions; it does not start them. It issues no
// namespace DELETE — a DELETE on a Terminating namespace succeeds and changes
// nothing — and the only namespace mutation is clearing the namespace's own
// "kubernetes" finalizer. The one deletion it can issue is of a HostedCluster,
// and only when the request explicitly opted in.
//
// See docs/ops/stuck-cluster-cleanup-controller-proposal.md.
package namespacedeletion

import (
	"context"
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	backendcontrollerutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/namespacedeletiongate"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// NamespaceDeletionControllerName is the single source of truth for this
	// controller's name: the controller struct, the workqueue name (and
	// therefore the Prometheus label), the ctx controller name, and the logger
	// field all take their value from here.
	NamespaceDeletionControllerName = "NamespaceDeletion"

	// resyncPeriod re-enqueues each request so an observation that has not
	// arrived yet, or a dwell that has since elapsed, is picked up without an
	// external trigger. The management cluster's state changes outside the
	// Cosmos change feed, so a timer is the only thing that notices.
	resyncPeriod = 2 * time.Minute

	// desireNamePrefix namespaces the desires this controller creates within
	// the management cluster's desire collection, so they are recognisable and
	// cannot collide with another controller's.
	desireNamePrefix = "namespacedeletion-"

	// terminalRetentionPeriod is how long a settled request is kept before the
	// controller deletes it.
	//
	// Nothing else collects these documents: they are stamp-rooted, so
	// DeleteOrphanedCosmosResources cannot reach them by its subscription-prefix
	// walk. The window is long enough that an operator who acts on Friday can
	// still read the audit record the following week.
	terminalRetentionPeriod = 14 * 24 * time.Hour
)

// Config carries the operator-tunable safety margins.
//
// Both are flags rather than constants because every end-to-end test gates on
// them and a one-hour wait per iteration makes personal-dev verification
// impractical. They travel to kube-applier on the desire and are re-checked
// there, so a request can never shorten its own margin — only the operator
// deploying the backend can.
type Config struct {
	// MinimumDwell is how long a namespace must already have been Terminating
	// before its finalizer may be cleared. It is an age measured on the
	// namespace, not a delay this controller imposes: a namespace that has been
	// Terminating for three weeks satisfies it immediately.
	MinimumDwell time.Duration

	// HostedClusterRemovalTimeout bounds the opt-in path's wait for HyperShift
	// to finish after the HostedCluster delete is issued. Unlike the dwell this
	// is a real wait imposed here, so it should be long enough for a healthy
	// deprovision and no longer.
	HostedClusterRemovalTimeout time.Duration

	// EnvIdentifier anchors the ocm-<env>-<id> namespace grammar. The admin API
	// does not receive it and validates structure only, so this is where the
	// environment check actually happens.
	EnvIdentifier string
}

type namespaceDeletionSyncer struct {
	fleetDBClient        fleetcosmosstorage.FleetDBClient
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
	readDesireLister     kubeapplierlisters.ReadDesireLister
	applyDesireLister    kubeapplierlisters.ApplyDesireLister
	config               Config
}

// NewNamespaceDeletionController reconciles NamespaceDeletion documents.
func NewNamespaceDeletionController(
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	applyDesireLister kubeapplierlisters.ApplyDesireLister,
	fleetInformers fleetinformers.FleetInformers,
	config Config,
) *controllerutil.GenericWatchingController[backendcontrollerutils.NamespaceDeletionKey] {
	syncer := &namespaceDeletionSyncer{
		fleetDBClient:        fleetDBClient,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		readDesireLister:     readDesireLister,
		applyDesireLister:    applyDesireLister,
		config:               config,
	}

	return backendcontrollerutils.NewNamespaceDeletionWatchingController(
		NamespaceDeletionControllerName,
		fleetInformers,
		resyncPeriod,
		syncer,
	)
}

// ReadDesireNameFor returns the namespace ReadDesire name for a request. It is
// derived from the request's own name so the mapping is stable across restarts
// and recoverable without consulting status.
func ReadDesireNameFor(requestName string) string {
	return desireNamePrefix + requestName
}

// HostedClusterReadDesireNameFor returns the HostedCluster ReadDesire name.
//
// Each request owns its own read of the shared HostedCluster rather than
// sharing one, so completing or retiring one namespace request can never strip
// the other request's observation.
func HostedClusterReadDesireNameFor(requestName string) string {
	return desireNamePrefix + requestName + "-hostedcluster"
}

// FinalizeDesireNameFor returns the FinalizeNamespace ApplyDesire name.
func FinalizeDesireNameFor(requestName string) string {
	return desireNamePrefix + requestName + "-finalize"
}

// HostedClusterDeleteDesireNameFor returns the opt-in HostedCluster delete
// ApplyDesire name.
func HostedClusterDeleteDesireNameFor(requestName string) string {
	return desireNamePrefix + requestName + "-hcdelete"
}

func namespaceTargetFor(namespaceName string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
		Name:     namespaceName,
	}
}

func hostedClusterTargetFor(namespace, name string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:     "hypershift.openshift.io",
		Version:   "v1beta1",
		Resource:  "hostedclusters",
		Namespace: namespace,
		Name:      name,
	}
}

// SyncOnce reconciles one NamespaceDeletion.
//
// The ordering of the gates is a safety property, not sequencing convenience:
// the UID pin, the ownership gate and the dwell all precede the first
// destructive act on every path. Reordering them to delete the HostedCluster
// earlier would defeat the invariant that this API cannot destroy a healthy
// cluster or namespace.
func (s *namespaceDeletionSyncer) SyncOnce(ctx context.Context, key backendcontrollerutils.NamespaceDeletionKey) error {
	requestCRUD := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).NamespaceDeletions()

	existing, err := requestCRUD.Get(ctx, key.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The request is gone, but the desires it created are not owned by
		// anything else and nothing else will collect them.
		return s.retireOwnedDesires(ctx, key)
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if isSettled(existing) {
		return s.reconcileTerminal(ctx, requestCRUD, key, existing)
	}

	updated := existing.DeepCopy()
	outcome := s.reconcile(ctx, key, updated)
	return s.persist(ctx, requestCRUD, updated, existing, outcome)
}

// reconcile runs the gate sequence and records the outcome on updated. It
// returns an error only for conditions worth retrying at the workqueue level;
// everything the operator needs to see is written to conditions.
func (s *namespaceDeletionSyncer) reconcile(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
) error {
	relationship, err := fleetapihelpers.ClassifyNamespace(
		request.Spec.NamespaceName, request.Spec.HostedClusterName, s.config.EnvIdentifier)
	if err != nil {
		// The grammar is checked at POST too, but the environment identifier is
		// only known here, so a request can reach this point and fail. It is a
		// latched stop rather than a retry: no amount of waiting fixes a name.
		setStopped(request, fleetapi.ReasonNamespaceIdentityMismatch, err.Error())
		return nil
	}

	// Step 1: observations. Both ReadDesires are established before anything
	// else so that a missing observation is reported as pending rather than
	// mistaken for absence.
	namespaceObs, err := s.observeNamespace(ctx, key, request)
	if err != nil {
		setDegraded(request, fleetapi.ReasonObservationFailed, err.Error())
		return err
	}
	hostedClusterObs, err := s.observeHostedCluster(ctx, key, request, relationship)
	if err != nil {
		setDegraded(request, fleetapi.ReasonObservationFailed, err.Error())
		return err
	}
	clearDegraded(request)

	if namespaceObs.pending || hostedClusterObs.pending {
		setIncomplete(request, fleetapi.ReasonObservationPending, fmt.Sprintf(
			"Waiting for namespace %q to be observed on management cluster %q; nothing has been modified.",
			request.Spec.NamespaceName, key.StampIdentifier))
		return nil
	}

	// Step 2: already gone.
	if namespaceObs.absent {
		setComplete(request, fleetapi.ReasonNamespaceAbsent, fmt.Sprintf(
			"Namespace %q is absent; no action was needed.", request.Spec.NamespaceName))
		return nil
	}

	// Step 3: this API finishes deletions, it does not start them. No request
	// field overrides this.
	if namespaceObs.deletionTimestamp == nil {
		setStopped(request, fleetapi.ReasonNamespaceNotTerminating, fmt.Sprintf(
			"Namespace %q is Active. This API only unblocks deletions already in progress; it does not start them. No change was made.",
			request.Spec.NamespaceName))
		return nil
	}

	// Step 4: identity of the observed HostedCluster.
	if hostedClusterObs.present && hostedClusterObs.name != request.Spec.HostedClusterName {
		setStopped(request, fleetapi.ReasonHostedClusterIdentityMismatch, fmt.Sprintf(
			"Requested HostedCluster %q in namespace %q, but observed HostedCluster %q. No change was made. Verify the name, then submit a corrected request.",
			request.Spec.HostedClusterName, relationship.HostedClusterNamespace, hostedClusterObs.name))
		setIncomplete(request, fleetapi.ReasonBlockedByIdentityMismatch, "blocked by a HostedCluster identity mismatch")
		return nil
	}

	// Step 5: pin the UID and run the live ownership gate.
	switch {
	case len(request.Spec.NamespaceUID) == 0:
		request.Spec.NamespaceUID = namespaceObs.uid
	case request.Spec.NamespaceUID != namespaceObs.uid:
		setStopped(request, fleetapi.ReasonNamespaceUIDChanged, fmt.Sprintf(
			"Namespace %q now has UID %q but this request pinned %q; it was replaced and this attempt is void.",
			request.Spec.NamespaceName, namespaceObs.uid, request.Spec.NamespaceUID))
		return nil
	}

	if stop := s.checkOwnership(ctx, key, request, relationship); stop != nil {
		return stop.apply(request)
	}

	// Step 6: the dwell gates every mutation, including the HostedCluster
	// delete below. Nothing destructive happens on a namespace that has only
	// just entered Terminating.
	elapsed := time.Since(namespaceObs.deletionTimestamp.Time)
	if elapsed < s.config.MinimumDwell {
		setIncomplete(request, fleetapi.ReasonDwellNotElapsed, fmt.Sprintf(
			"Namespace %q has been terminating for %s, which is less than the required dwell of %s. deletionTimestamp=%s.",
			request.Spec.NamespaceName, elapsed.Round(time.Second), s.config.MinimumDwell,
			namespaceObs.deletionTimestamp.UTC().Format(time.RFC3339)))
		return nil
	}

	// Step 7: the HostedCluster gates.
	if hostedClusterObs.present && hostedClusterObs.deletionTimestamp == nil {
		return s.handleHostedClusterNotDeleting(ctx, key, request, relationship, namespaceObs, hostedClusterObs)
	}

	// Step 8: audit, then finalize.
	recordAudit(request, namespaceObs, hostedClusterObs, false)
	if err := s.ensureFinalizeDesire(ctx, key, request, relationship, namespaceObs); err != nil {
		setDegraded(request, fleetapi.ReasonObservationFailed, err.Error())
		return err
	}
	setIncomplete(request, fleetapi.ReasonFinalizingNamespace, fmt.Sprintf(
		"Clearing the %q finalizer on namespace %q (uid %s).",
		"kubernetes", request.Spec.NamespaceName, request.Spec.NamespaceUID))
	return nil
}

// handleHostedClusterNotDeleting covers the one observation the
// DeleteHostedCluster opt-in exists for: a HostedCluster with no
// deletionTimestamp.
//
// Without the opt-in this is a refusal, because HyperShift's finalizer never
// started and the cluster's Azure infrastructure is provably still live. With
// it, the controller may delete the HostedCluster — but only after the liveness
// gate confirms the cluster does not look like a working one, and only waiting
// for the HostedCluster to actually go before it finalizes the namespace.
func (s *namespaceDeletionSyncer) handleHostedClusterNotDeleting(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
	relationship *fleetapihelpers.NamespaceRelationship,
	namespaceObs namespaceObservation,
	hostedClusterObs hostedClusterObservation,
) error {
	if !request.Spec.DeleteHostedCluster {
		setStopped(request, fleetapi.ReasonHostedClusterNotDeleting, fmt.Sprintf(
			"HostedCluster %q exists and has not been asked to delete, so its Azure infrastructure is still live. This request did not set deleteHostedCluster, so no deletion was initiated. Investigate manually, or submit a new request with deleteHostedCluster set. No change was made.",
			hostedClusterObs.name))
		return nil
	}

	// The liveness gate. The opt-in removes the check that made a live cluster
	// safe, so something has to replace it, and the Cosmos gate cannot: this API
	// exists for the case where Cosmos records are missing or stale, which is
	// exactly when "no record" must not be read as "no cluster".
	//
	// This refusal is not overridable by any request field.
	if reason := hostedClusterObs.livenessReason; len(reason) > 0 {
		setStopped(request, fleetapi.ReasonHostedClusterLive, fmt.Sprintf(
			"HostedCluster %q looks like a working cluster (%s). A live cluster is never a cleanup target, and deleteHostedCluster does not override this. No change was made.",
			hostedClusterObs.name, reason))
		return nil
	}

	// Audit before the mutation, not after: this is the only artifact an
	// operator will have if the decision turns out to be wrong.
	recordAudit(request, namespaceObs, hostedClusterObs, true)

	if err := s.ensureHostedClusterDeleteDesire(ctx, key, request, relationship); err != nil {
		setDegraded(request, fleetapi.ReasonObservationFailed, err.Error())
		return err
	}

	// Bounded wait. On expiry the request stops and deliberately does NOT fall
	// back to finalizing: Azure was live when this attempt began, so finalizing
	// now would convert a recoverable situation into a guaranteed leak.
	if issuedAt := hostedClusterDeleteIssuedAt(request); issuedAt != nil {
		if waited := time.Since(issuedAt.Time); waited > s.config.HostedClusterRemovalTimeout {
			setStopped(request, fleetapi.ReasonHostedClusterRemovalTimedOut, fmt.Sprintf(
				"HostedCluster %q was deleted %s ago but still exists; HyperShift has not finished. The namespace was NOT finalized, because its Azure infrastructure was live when this attempt began. The HostedCluster now carries a deletionTimestamp set by this request. Investigate manually.",
				hostedClusterObs.name, waited.Round(time.Second)))
			return nil
		}
	}

	setIncomplete(request, fleetapi.ReasonDeletingHostedCluster, fmt.Sprintf(
		"Deleted HostedCluster %q as authorized by deleteHostedCluster; waiting for HyperShift to finish deprovisioning before the namespace is finalized. Timeout %s.",
		hostedClusterObs.name, s.config.HostedClusterRemovalTimeout))
	return nil
}

// ownershipStop carries a gate verdict that blocks the request.
type ownershipStop struct {
	degraded bool
	reason   string
	message  string
}

func (o *ownershipStop) apply(request *fleetapi.NamespaceDeletion) error {
	if o.degraded {
		setDegraded(request, o.reason, o.message)
		// Degraded is retried indefinitely with the workqueue's backoff and
		// gates no mutation, so there is nothing to return to the caller.
		return nil
	}
	setStopped(request, o.reason, o.message)
	return nil
}

// checkOwnership runs the live Cosmos ownership gate. A nil result means the
// target is unowned and the request may proceed.
//
// This runs immediately before the finalize and, on the opt-in path, before the
// HostedCluster delete as well: a delete is at least as irreversible as a
// finalize.
func (s *namespaceDeletionSyncer) checkOwnership(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
	relationship *fleetapihelpers.NamespaceRelationship,
) *ownershipStop {
	managementClusterResourceID, err := fleetapihelpers.ToManagementClusterResourceID(key.StampIdentifier)
	if err != nil {
		return &ownershipStop{degraded: true, reason: fleetapi.ReasonOwnershipCheckFailed, message: err.Error()}
	}

	var clusterLister namespacedeletiongate.ServiceProviderClusterLister
	if s.resourcesDBClient != nil {
		clusterLister = s.resourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters()
	}

	var desireLister namespacedeletiongate.ApplyDesireLister
	if s.kubeApplierDBClients != nil {
		client, clientErr := s.kubeApplierDBClients.For(ctx, managementClusterResourceID)
		if clientErr != nil {
			return &ownershipStop{degraded: true, reason: fleetapi.ReasonOwnershipCheckFailed, message: clientErr.Error()}
		}
		if client != nil {
			desireLister = client.Listers().ApplyDesires()
		}
	}

	result := namespacedeletiongate.Evaluate(ctx, clusterLister, desireLister, namespacedeletiongate.Target{
		ManagementClusterResourceID: managementClusterResourceID.String(),
		HostedClusterNamespace:      relationship.HostedClusterNamespace,
		ControlPlaneNamespace:       relationship.ControlPlaneNamespace,
		HostedClusterName:           request.Spec.HostedClusterName,
	}, desireNamePrefix+key.Name)

	switch result.Verdict {
	case namespacedeletiongate.VerdictUnowned:
		return nil
	case namespacedeletiongate.VerdictOwned:
		if len(result.ConflictingDesire) > 0 {
			return &ownershipStop{reason: fleetapi.ReasonActiveDesirePresent, message: fmt.Sprintf(
				"ApplyDesire %q still maintains namespace %q; resolve that owner before retrying. No change was made.",
				result.ConflictingDesire, request.Spec.NamespaceName)}
		}
		return &ownershipStop{reason: fleetapi.ReasonClusterRecordAppeared, message: fmt.Sprintf(
			"Cluster %q now claims namespace %q (%s); use the normal cluster deletion flow. No change was made.",
			result.OwnerResourceID, request.Spec.NamespaceName, result.Detail)}
	default:
		// Indeterminate and Unavailable both block and both are retryable. A
		// failed lookup is never reported as absence.
		return &ownershipStop{degraded: true, reason: fleetapi.ReasonOwnershipCheckFailed, message: fmt.Sprintf(
			"The ownership check for namespace %q did not complete (%s): %s. This is not evidence that the namespace is unowned.",
			request.Spec.NamespaceName, result.Verdict, result.Detail)}
	}
}

// reconcileTerminal handles a request that has settled.
//
// Two things have to happen and neither happens on its own. The desires this
// controller created are released, because otherwise every invocation would
// leave a permanent watch behind. Then, once the request has aged past the
// retention window, the document itself is deleted.
//
// The desires go first. Deleting the document first would lose the only
// reference to them and strand them permanently — and failure to retire is not
// success.
func (s *namespaceDeletionSyncer) reconcileTerminal(
	ctx context.Context,
	requestCRUD cosmosstorageutils.ValidatingResourceCRUD[fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion],
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
) error {
	logger := utils.LoggerFromContext(ctx)

	if err := s.retireOwnedDesires(ctx, key); err != nil {
		return err
	}

	if request.Status.CompletedAt == nil {
		updated := request.DeepCopy()
		now := metav1.NewTime(time.Now())
		updated.Status.CompletedAt = &now
		return s.persist(ctx, requestCRUD, updated, request, nil)
	}

	if time.Since(request.Status.CompletedAt.Time) < terminalRetentionPeriod {
		return nil
	}

	logger.Info("retiring settled namespace deletion request past its retention window",
		"completedAt", request.Status.CompletedAt.Time, "retentionPeriod", terminalRetentionPeriod)

	if err := requestCRUD.Delete(ctx, key.Name); err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}
	return nil
}

// retireOwnedDesires removes every desire this controller created for a
// request. It is idempotent: a missing desire is success.
//
// An unreachable management cluster is reported as an error rather than
// swallowed. Returning success here would let retention delete the request
// while its watch may still exist, losing the only durable reference to it.
func (s *namespaceDeletionSyncer) retireOwnedDesires(ctx context.Context, key backendcontrollerutils.NamespaceDeletionKey) error {
	managementClusterResourceID, err := fleetapihelpers.ToManagementClusterResourceID(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	kubeApplierClient, err := s.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	if kubeApplierClient == nil {
		return fmt.Errorf("kube-applier Cosmos client not available for management cluster %q; cannot confirm desires were retired", key.StampIdentifier)
	}

	readCRUD, err := kubeApplierClient.ReadDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}
	// The scoped CRUD takes a leaf name and builds the full resource ID itself.
	// Passing a full resource ID here would make it prepend the scope a second
	// time, so the delete would miss and its NotFound would be swallowed as
	// success — leaving the desire behind forever.
	for _, name := range []string{ReadDesireNameFor(key.Name), HostedClusterReadDesireNameFor(key.Name)} {
		if err := readCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(err)
		}
	}

	applyCRUD, err := kubeApplierClient.ApplyDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, name := range []string{FinalizeDesireNameFor(key.Name), HostedClusterDeleteDesireNameFor(key.Name)} {
		if err := applyCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(err)
		}
	}
	return nil
}

func (s *namespaceDeletionSyncer) persist(
	ctx context.Context,
	crud cosmosstorageutils.ValidatingResourceCRUD[fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion],
	updated, existing *fleetapi.NamespaceDeletion,
	syncErr error,
) error {
	// An unchanged status must not trigger a Cosmos write. Each write changes
	// the ETag and triggers another informer update, which would turn a waiting
	// request into a self-sustaining read/write loop instead of resting until
	// the next resync.
	if !controllerutil.NeedsUpdate(existing, updated) {
		return syncErr
	}
	if _, err := crud.Replace(ctx, updated, existing, nil); err != nil {
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// Another writer won the race; the resync reconciles any drift.
			return syncErr
		}
		return utils.TrackError(err)
	}
	return syncErr
}

func (s *namespaceDeletionSyncer) managementCluster(key backendcontrollerutils.NamespaceDeletionKey) (*azcorearm.ResourceID, error) {
	return fleetapihelpers.ToManagementClusterResourceID(key.StampIdentifier)
}

// ensureFinalizeDesire writes the FinalizeNamespace desire carrying the pinned
// UID, the dwell and the HostedCluster identity, so kube-applier can re-check
// every precondition against live data immediately before acting.
func (s *namespaceDeletionSyncer) ensureFinalizeDesire(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
	relationship *fleetapihelpers.NamespaceRelationship,
	namespaceObs namespaceObservation,
) error {
	managementCluster, err := s.managementCluster(key)
	if err != nil {
		return utils.TrackError(err)
	}
	spec := kubeapplierapi.ApplyDesireSpec{
		ManagementCluster: managementCluster,
		Type:              kubeapplierapi.ApplyDesireTypeFinalizeNamespace,
		TargetItem:        namespaceTargetFor(request.Spec.NamespaceName),
		FinalizeNamespace: &kubeapplierapi.FinalizeNamespaceConfig{
			UID:                    request.Spec.NamespaceUID,
			MinimumDwell:           metav1.Duration{Duration: s.config.MinimumDwell},
			HostedClusterNamespace: relationship.HostedClusterNamespace,
			HostedClusterName:      request.Spec.HostedClusterName,
		},
	}
	_ = namespaceObs
	return s.ensureApplyDesire(ctx, key, FinalizeDesireNameFor(key.Name), spec)
}

// ensureHostedClusterDeleteDesire writes the opt-in HostedCluster delete.
//
// It reuses the existing Type=Delete union member and kube-applier behaviour —
// the same mechanism the normal removal chain uses — so no kube-applier change
// is required for this path.
func (s *namespaceDeletionSyncer) ensureHostedClusterDeleteDesire(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	request *fleetapi.NamespaceDeletion,
	relationship *fleetapihelpers.NamespaceRelationship,
) error {
	managementCluster, err := s.managementCluster(key)
	if err != nil {
		return utils.TrackError(err)
	}
	spec := kubeapplierapi.ApplyDesireSpec{
		ManagementCluster: managementCluster,
		Type:              kubeapplierapi.ApplyDesireTypeDelete,
		TargetItem:        hostedClusterTargetFor(relationship.HostedClusterNamespace, request.Spec.HostedClusterName),
	}
	return s.ensureApplyDesire(ctx, key, HostedClusterDeleteDesireNameFor(key.Name), spec)
}

func (s *namespaceDeletionSyncer) ensureApplyDesire(
	ctx context.Context,
	key backendcontrollerutils.NamespaceDeletionKey,
	desireName string,
	spec kubeapplierapi.ApplyDesireSpec,
) error {
	managementCluster, err := s.managementCluster(key)
	if err != nil {
		return utils.TrackError(err)
	}
	kubeApplierClient, err := s.kubeApplierDBClients.For(ctx, managementCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if kubeApplierClient == nil {
		return fmt.Errorf("kube-applier Cosmos client not available for management cluster %q", key.StampIdentifier)
	}
	crud, err := kubeApplierClient.ApplyDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	resourceID, err := azcorearm.ParseResourceID(
		kubeapplierapihelpers.ToManagementClusterScopedApplyDesireResourceIDString(key.StampIdentifier, desireName))
	if err != nil {
		return utils.TrackError(err)
	}

	existing, err := s.applyDesireLister.GetForManagementCluster(ctx, key.StampIdentifier, desireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		desired := &kubeapplierapi.ApplyDesire{Spec: spec}
		desired.ResourceID = resourceID
		if _, createErr := crud.Create(ctx, desired, nil); createErr != nil {
			if cosmosstorageutils.IsConflictError(createErr) {
				return nil
			}
			return utils.TrackError(createErr)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !controllerutil.NeedsUpdate(existing.Spec, spec) {
		return nil
	}
	replacement := existing.DeepCopy()
	replacement.Spec = spec
	if _, replaceErr := crud.Replace(ctx, replacement, nil); replaceErr != nil {
		if cosmosstorageutils.IsPreconditionFailedError(replaceErr) {
			return nil
		}
		return utils.TrackError(replaceErr)
	}
	return nil
}

// hostedClusterDeleteIssuedAt returns when this request issued the HostedCluster
// delete, which is the start of the removal-timeout clock. It is read from the
// audit record because that is written before the mutation.
func hostedClusterDeleteIssuedAt(request *fleetapi.NamespaceDeletion) *metav1.Time {
	if request.Status.Audit == nil || request.Status.Audit.HostedCluster == nil {
		return nil
	}
	if !request.Status.Audit.HostedCluster.DeletedByThisRequest {
		return nil
	}
	return request.Status.Audit.RecordedAt
}

// recordAudit captures the durable evidence before a mutation.
//
// It is written once and not overwritten on later passes: the point of the
// record is what was true at the moment the decision was made.
func recordAudit(
	request *fleetapi.NamespaceDeletion,
	namespaceObs namespaceObservation,
	hostedClusterObs hostedClusterObservation,
	deletingHostedCluster bool,
) {
	if request.Status.Audit != nil {
		return
	}
	now := metav1.NewTime(time.Now())
	audit := &fleetapi.NamespaceDeletionAudit{
		RecordedAt:                 &now,
		NamespaceUID:               namespaceObs.uid,
		NamespaceDeletionTimestamp: namespaceObs.deletionTimestamp,
		NamespaceConditions:        namespaceObs.conditions,
		FinalizersRemoved:          namespaceObs.specFinalizers,
	}
	if hostedClusterObs.present {
		audit.HostedCluster = &fleetapi.NamespaceDeletionAuditHostedCluster{
			Name:                 hostedClusterObs.name,
			UID:                  hostedClusterObs.uid,
			DeletionTimestamp:    hostedClusterObs.deletionTimestamp,
			Finalizers:           hostedClusterObs.finalizers,
			DeletedByThisRequest: deletingHostedCluster,
		}
	}
	request.Status.Audit = audit
}

// isSettled reports whether a request has reached an outcome no further
// reconciliation will change.
func isSettled(request *fleetapi.NamespaceDeletion) bool {
	return apimeta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete) ||
		apimeta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped)
}

func setComplete(request *fleetapi.NamespaceDeletion, reason, message string) {
	apimeta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionComplete, Status: metav1.ConditionTrue,
		Reason: reason, Message: message,
	})
}

func setIncomplete(request *fleetapi.NamespaceDeletion, reason, message string) {
	apimeta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionComplete, Status: metav1.ConditionFalse,
		Reason: reason, Message: message,
	})
}

// setStopped latches a safety stop. Complete is pinned False alongside it so a
// reader cannot mistake a stopped attempt for an unfinished one that is still
// making progress.
func setStopped(request *fleetapi.NamespaceDeletion, reason, message string) {
	apimeta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionStopped, Status: metav1.ConditionTrue,
		Reason: reason, Message: message,
	})
	if !apimeta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete) {
		setIncomplete(request, reason, message)
	}
}

func setDegraded(request *fleetapi.NamespaceDeletion, reason, message string) {
	apimeta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionDegraded, Status: metav1.ConditionTrue,
		Reason: reason, Message: message,
	})
}

func clearDegraded(request *fleetapi.NamespaceDeletion) {
	apimeta.SetStatusCondition(&request.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionDegraded, Status: metav1.ConditionFalse,
		Reason: fleetapi.ReasonObservationPending, Message: "no retryable error is preventing progress",
	})
}
