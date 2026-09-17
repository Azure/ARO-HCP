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

package clusterresources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// applyDesireRemovalStep is one step of the ApplyDesire teardown chain that
// runs while a cluster is being deleted. Steps run in the order they appear in
// applyDesireRemovalChain, and a step that has not fully drained blocks every
// later step until a subsequent resync.
//
// A step owns both halves of its job: which of the cluster's ApplyDesires it is
// responsible for, and how those desires are torn down. Nothing outside the
// step needs to know its selection rule, so a step whose teardown is more than
// "flip every match to Type=Delete" is free to implement remove differently.
//
// The steps themselves live in apply_desire_removal_steps.go.
type applyDesireRemovalStep interface {
	// name identifies the step in log output.
	name() string

	// remove tears down the desires this step is responsible for, picking
	// them out of owned — every ApplyDesire this controller owns for the
	// cluster. done reports whether the step is drained: false means it
	// is still waiting on at least one desire and the chain must stop here
	// until a later resync. A step that returns an error is never treated as
	// drained.
	remove(
		ctx context.Context,
		kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
		owned []*kubeapplierapi.ApplyDesire,
	) (done bool, err error)
}

// applyDesireRemovalChain orders cluster-deletion teardown around the objects
// whose removal has to be sequenced, and lets the namespace cascade take care of
// everything else.
//
// cascadeCoveredRemovalStep goes first because it is the one step that waits for
// nothing. It drops documents without deleting objects, so the inert
// configuration the HostedCluster teardown still reads stays right where it is;
// all that stops is the reconcile loop. Putting it anywhere else would park it
// behind a finalizer for no reason, and it must in any case precede
// namespacesRemovalStep — a desire left live while its namespace is Terminating
// makes the kube-applier retry an apply that can never succeed.
//
// The NodePools are deleted next. Their CRs hold Azure machines behind a CAPI
// finalizer, so they are deleted deliberately and waited out rather than left to
// the cascade — see nodePoolDesireNames for why neither the HostedCluster
// finalizer nor Cluster Service is something to lean on here. Deleting them
// before the HostedCluster is also what unblocks Cluster Service while it is
// still in the picture: it removes the node pool ManifestWorks with foreground
// propagation early in its own removal chain, and the work-agent cannot retire a
// ManifestWork until the CR it applied stays deleted.
//
// Deleting the HostedCluster next gives HyperShift the chance to run its own
// finalizer, which deprovisions Azure infrastructure and tears down the control
// plane while the namespaces it depends on are still intact. Deleting a
// Namespace out from under it would garbage collect the HostedCluster CR before
// that finalizer could complete, stranding Azure resources and, because the
// namespace cascade honors finalizers, likely wedging the namespace in
// Terminating.
//
// The SWIFT networking resources come after it, in reverse order of creation.
// Both hold Azure networking state behind finalizers, and PodNetwork is
// cluster-scoped besides, so neither can be left to the cascade.
//
// The namespaces go last, once every object that needed deleting on its own
// terms is gone. Their deletion is what actually reclaims everything
// cascadeCoveredRemovalStep stopped reconciling.
//
// Steps claim desires by name, and the names come from classifyClusterResource,
// which errors on anything it does not recognize. Between the two, the claim
// sets are exhaustive; deleteAllOwnedApplyDesires warns if a desire nonetheless
// shows up unclaimed, since nothing would tear it down.
var applyDesireRemovalChain = []applyDesireRemovalStep{
	cascadeCoveredRemovalStep{},
	nodePoolRemovalStep{},
	hostedClusterRemovalStep{},
	swiftPodNetworkInstanceRemovalStep{},
	swiftPodNetworkRemovalStep{},
	namespacesRemovalStep{},
}

// deleteAllOwnedApplyDesires tears down every ApplyDesire owned by this
// controller for the given cluster, walking applyDesireRemovalChain in order.
//
// A desire whose Kubernetes object has to be deleted deliberately is flipped to
// Type=Delete, so the kube-applier issues the delete and waits out its
// finalizers before the Cosmos document is removed; dropping the document on its
// own would strand the object, because the kube-applier reconciles desires and
// does not garbage collect what a vanished desire once applied. A desire whose
// object the namespace cascade will remove anyway only needs its document
// dropped — see the two primitives below.
//
// The call returns nil as soon as a step is still waiting on one of its
// desires, leaving the remaining steps for the next resync. Progress is
// therefore driven by the controller's poll interval, and the function is safe
// to re-run at any point.
func (c *clusterResourcesController) deleteAllOwnedApplyDesires(ctx context.Context, key controllerutils.HCPClusterKey, managementCluster *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires for deletion cleanup: %w", err))
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}

	claimed := claimedDesireNames()
	owned := make([]*kubeapplierapi.ApplyDesire, 0, len(existing))
	for _, desire := range existing {
		if desire.Tags == nil ||
			desire.Tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
			continue
		}
		// No step claims this name, so no step will tear it down and the chain
		// will report itself drained with the desire still live. Deleting it
		// blindly is worse than leaking it — the right fix is to assign the name
		// to a step — so say so loudly and carry on.
		if !claimed.selects(desire) {
			logger.Error(
				fmt.Errorf("ApplyDesire %q is not claimed by any teardown step", desire.ResourceID.Name),
				"ApplyDesire will not be removed; assign its name to a step in applyDesireRemovalChain",
				"resourceID", desire.ResourceID.String())
		}
		owned = append(owned, desire)
	}

	for _, step := range applyDesireRemovalChain {
		done, err := step.remove(ctx, kubeApplierDBClient, owned)
		if err != nil {
			return err
		}
		if !done {
			return nil
		}
	}

	// All ApplyDesire teardown steps are complete. Sweep for orphaned ReadDesires:
	// if a ReadDesire deletion failed after its ApplyDesire was purged, the
	// step loop above cannot retry (the ApplyDesire is gone from owned). This
	// sweep catches those orphans and deletes them now.
	if err := c.sweepOrphanedReadDesires(ctx, key, kubeApplierDBClient); err != nil {
		return err
	}

	return nil
}

// ensureMatchingApplyDesiresRemoved is the teardown primitive the removal steps share:
// it flips every desire in owned that matches selects to Type=Delete and purges
// the ones the kube-applier reports as deleted. owned must already be filtered
// to the desires this controller owns. done is true only when nothing
// matching selects is left.
func ensureMatchingApplyDesiresRemoved(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	stepName string,
	owned []*kubeapplierapi.ApplyDesire,
	selects func(*kubeapplierapi.ApplyDesire) bool,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	var waitingFor []string
	var errs []error
	for _, desire := range owned {
		if !selects(desire) {
			continue
		}

		desireName := desire.ResourceID.Name
		crud, err := applyDesireCRUDFor(kubeApplierDBClient, desire)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		removed, err := kubeapplierhelpers.EnsureApplyDesireRemoved(ctx, desireName, crud)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !removed {
			waitingFor = append(waitingFor, desireName)
			continue
		}
		logger.Info("deleted ApplyDesire", "step", stepName, "desireName", desireName)

		// Now that the ApplyDesire is removed and the object is gone from the
		// management cluster, delete the corresponding ReadDesire. It was only
		// needed for observability during deletion; now that deletion is complete,
		// it's no longer useful.
		//
		// ReadDesire deletion errors are fatal: if we log and continue, the step
		// reports done=true while the ReadDesire is still live. The cleanup gate
		// preserves ClusterResources-owned ReadDesires and ServiceProviderCluster
		// deletion waits for every desire, so a transient Cosmos failure here would
		// permanently block cluster deletion. Return the error so the step stays
		// incomplete and retries on the next reconcile.
		readDesireCRUD, readErr := readDesireCRUDFor(kubeApplierDBClient, desire)
		if readErr != nil {
			return false, readErr
		}
		if delErr := readDesireCRUD.Delete(ctx, desireName); delErr != nil && !cosmosstorageutils.IsNotFoundError(delErr) {
			return false, utils.TrackError(fmt.Errorf("delete ReadDesire %s: %w", desireName, delErr))
		}
		logger.Info("deleted ReadDesire", "step", stepName, "desireName", desireName)
	}

	// Report errors ahead of the wait list: a step that could not be fully
	// processed must not be treated as drained, so the chain stops here.
	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	if len(waitingFor) > 0 {
		logger.Info("ApplyDesire teardown in progress",
			"step", stepName, "waitingFor", waitingFor)
		return false, nil
	}

	return true, nil
}

// deleteMatchingApplyDesiresDocuments is the other teardown primitive: it removes the
// Cosmos document for every desire in owned that matches selects, leaving
// spec.targetItem alone. The kube-applier stops reconciling the object, and
// deleting the namespace it lives in reclaims it.
//
// Use this only for objects the namespace cascade genuinely covers. For anything
// with a finalizer that reaches outside its own namespace, dropping the document
// strands the object and whatever it holds — use deleteMatchingApplyDesires.
//
// There is nothing to wait for, so done is true unless a drop failed.
func deleteMatchingApplyDesiresDocuments(
	ctx context.Context,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	stepName string,
	owned []*kubeapplierapi.ApplyDesire,
	selects func(*kubeapplierapi.ApplyDesire) bool,
) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	var errs []error
	for _, desire := range owned {
		if !selects(desire) {
			continue
		}

		desireName := desire.ResourceID.Name
		crud, err := applyDesireCRUDFor(kubeApplierDBClient, desire)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		// Already gone is the outcome we wanted; the lister is a cache and can
		// hand back a desire a previous pass deleted.
		if err := crud.Delete(ctx, desireName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			errs = append(errs, utils.TrackError(fmt.Errorf("drop ApplyDesire %s: %w", desireName, err)))
			continue
		}
		logger.Info("dropped ApplyDesire, leaving its object to the namespace cascade",
			"step", stepName, "desireName", desireName)
	}

	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	return true, nil
}

// applyDesireCRUDFor resolves the Cosmos handle for a desire without the caller
// having to know whether it is cluster- or nodepool-scoped.
func applyDesireCRUDFor(
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	desire *kubeapplierapi.ApplyDesire,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire], error) {
	desireName := desire.ResourceID.Name

	scope, err := kubeappliercosmosstorage.ParseDesireScope(desire.ResourceID.Parent)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("parse scope for ApplyDesire %s: %w", desireName, err))
	}
	crud, err := kubeApplierDBClient.ApplyDesiresFor(scope)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("get CRUD for ApplyDesire %s: %w", desireName, err))
	}

	return crud, nil
}

// readDesireCRUDFor resolves the Cosmos handle for a ReadDesire at the same
// scope as the given ApplyDesire (cluster- or nodepool-scoped).
func readDesireCRUDFor(
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
	applyDesire *kubeapplierapi.ApplyDesire,
) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	desireName := applyDesire.ResourceID.Name

	scope, err := kubeappliercosmosstorage.ParseDesireScope(applyDesire.ResourceID.Parent)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("parse scope for ReadDesire %s: %w", desireName, err))
	}
	crud, err := kubeApplierDBClient.ReadDesiresFor(scope)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("get CRUD for ReadDesire %s: %w", desireName, err))
	}

	return crud, nil
}

// sweepOrphanedReadDesires deletes any ReadDesires owned by this controller
// that no longer have a corresponding ApplyDesire. This catches orphans that
// arose from a transient ReadDesire deletion failure after the ApplyDesire was
// already purged (the normal paired cleanup in ensureMatchingApplyDesiresRemoved
// cannot retry once the ApplyDesire is gone).
func (c *clusterResourcesController) sweepOrphanedReadDesires(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	kubeApplierDBClient kubeappliercosmosstorage.KubeApplierDBClient,
) error {
	logger := utils.LoggerFromContext(ctx)

	// Build the set of ApplyDesire names we still own
	applyDesires, err := c.applyDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ApplyDesires for orphan ReadDesire sweep: %w", err))
	}
	ownedApplyDesireNames := make(map[string]bool)
	for _, desire := range applyDesires {
		if desire.Tags == nil || desire.Tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
			continue
		}
		ownedApplyDesireNames[strings.ToLower(desire.ResourceID.Name)] = true
	}

	// List ReadDesires and delete any owned by this controller that have no
	// corresponding ApplyDesire
	readDesires, err := c.readDesireLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list ReadDesires for orphan sweep: %w", err))
	}

	for _, readDesire := range readDesires {
		if readDesire.Tags == nil || readDesire.Tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
			continue
		}

		desireName := strings.ToLower(readDesire.ResourceID.Name)
		if ownedApplyDesireNames[desireName] {
			// ApplyDesire still exists; this ReadDesire is not orphaned
			continue
		}

		// Orphaned ReadDesire: ApplyDesire is gone but ReadDesire remains
		scope, err := kubeappliercosmosstorage.ParseDesireScope(readDesire.ResourceID.Parent)
		if err != nil {
			return utils.TrackError(fmt.Errorf("parse scope for orphaned ReadDesire %s: %w", desireName, err))
		}
		crud, err := kubeApplierDBClient.ReadDesiresFor(scope)
		if err != nil {
			return utils.TrackError(fmt.Errorf("get CRUD for orphaned ReadDesire %s: %w", desireName, err))
		}

		if err := crud.Delete(ctx, desireName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete orphaned ReadDesire %s: %w", desireName, err))
		}
		logger.Info("swept orphaned ReadDesire", "desireName", desireName)
	}

	return nil
}
