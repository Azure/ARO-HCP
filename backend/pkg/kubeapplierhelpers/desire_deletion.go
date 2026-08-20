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

package kubeapplierhelpers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DeleteAllChildDesires tears down every ApplyDesire and ReadDesire under
// parent. Each ApplyDesire is flipped to Type=Delete so the kube-applier
// removes the applied object from the management cluster and, once the delete
// reports success, the desire document is removed; ReadDesires are deleted
// directly.
//
// It returns a slice of human-readable reasons describing what teardown is
// still waiting for — one entry per desire that has not finished deleting yet.
// An empty slice means teardown is complete.
func DeleteAllChildDesires(
	ctx context.Context,
	kubeApplierClient kubeappliercosmosstorage.KubeApplierDBClient,
	parent DesireParent,
	subscriptionID, resourceGroupName, hcpClusterName string,
) ([]string, error) {
	applyCRUD, err := parent.applyDesireCRUD(kubeApplierClient, subscriptionID, resourceGroupName, hcpClusterName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	readCRUD, err := parent.readDesireCRUD(kubeApplierClient, subscriptionID, resourceGroupName, hcpClusterName)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	var waitingFor []string

	// Step 1: flip each matching ApplyDesire to Type=Delete and, once the delete
	// succeeds, remove the desire document.
	applyIter, err := applyCRUD.List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("list ApplyDesires: %w", err))
	}
	var errs []error
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		removed, err := EnsureApplyDesireRemoved(ctx, desireName, applyCRUD)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}
		if !removed {
			waitingFor = append(waitingFor, fmt.Sprintf("ApplyDesire %q", desireName))
		}
	}
	if err := applyIter.GetError(); err != nil {
		errs = append(errs, utils.TrackError(fmt.Errorf("iterate ApplyDesires: %w", err)))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(waitingFor) > 0 {
		return waitingFor, nil
	}

	// Step 2: all ApplyDesires are gone — delete ReadDesires.
	readIter, err := readCRUD.List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, desire := range readIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if err := readCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			errs = append(errs, utils.TrackError(fmt.Errorf("delete ReadDesire %s: %w", desireName, err)))
			continue
		}
	}
	if err := readIter.GetError(); err != nil {
		errs = append(errs, utils.TrackError(fmt.Errorf("iterate ReadDesires: %w", err)))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return waitingFor, nil
}

// EnsureApplyDesireRemoved tears down a single ApplyDesire by converting it to a
// Type=Delete desire (so the kube-applier deletes spec.targetItem from the
// management cluster) and, once that delete reports success, removing the desire
// document. It returns true once the ApplyDesire is gone — either purged after a
// successful delete or already absent.
func EnsureApplyDesireRemoved(
	ctx context.Context,
	desireName string,
	applyCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
) (bool, error) {
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(desireName))
	if cosmosstorageutils.IsNotFoundError(err) {
		// Already gone.
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

	// If the desire is still a ServerSideApply, flip it to a Delete so the
	// kube-applier tears down the applied object. TargetItem already names what
	// to delete; the ServerSideApply payload is cleared.
	if applyDesire.Spec.Type != kubeapplierapi.ApplyDesireTypeDelete {
		applyDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		applyDesire.Spec.ServerSideApply = nil
		// Clear conditions so a later SuccessfullyDeleted (or, for an older
		// kube-applier, Successful) unambiguously reflects the delete rather than a
		// stale server-side-apply result.
		applyDesire.Status.Conditions = nil
		// A NotFound (the desire was deleted concurrently) or a PreconditionFailed
		// (a concurrent kube-applier status update bumped the etag) is a benign
		// race — the next reconcile retries.
		if _, err := applyCRUD.Replace(ctx, applyDesire, nil); err != nil {
			if cosmosstorageutils.IsNotFoundError(err) || cosmosstorageutils.IsPreconditionFailedError(err) {
				return false, nil
			}
			return false, utils.TrackError(fmt.Errorf("convert ApplyDesire %s to Delete: %w", desireName, err))
		}
		return false, nil
	}

	// The desire is a Delete — remove the document once the delete has succeeded.
	// Prefer the operation-specific SuccessfullyDeleted condition, falling back to
	// the legacy Successful for documents last written by an older kube-applier.
	if kubeapplierapihelpers.IsConditionTruePreferring(applyDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful) {
		if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
		}
		return true, nil
	}
	// Delete not yet successful; wait.
	return false, nil
}
