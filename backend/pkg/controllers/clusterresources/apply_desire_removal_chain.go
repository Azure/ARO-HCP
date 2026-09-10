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
// nothing and unblocks something. It drops documents without deleting objects,
// so the inert configuration the HostedCluster teardown still reads stays right
// where it is; all that stops is the reconcile loop. Running it up front matters
// for NodePool: Cluster Service deletes the node pool ManifestWorks with
// foreground propagation early in its own removal chain, and the work-agent
// cannot retire a ManifestWork until the CR it applied stays deleted. As long as
// the NodePool desire is live the kube-applier keeps re-applying that CR, so
// Cluster Service parks on the ManifestWork step for exactly as long as this step
// is queued behind the waited-on ones.
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
