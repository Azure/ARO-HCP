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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// deleteDesires tears down every desire the matches predicate selects: each
// ApplyDesire is flipped to Type=Delete so the kube-applier removes the applied
// object from the management cluster and, once the delete reports success, the
// desire document is removed; the matching ReadDesires are then deleted directly.
//
// It returns a slice of human-readable reasons describing what teardown is still
// waiting for — one entry per desire that has not finished deleting yet. An empty
// slice means teardown is complete. Callers join the reasons into a single wait
// message. This helper is shared by the cluster-deletion-cleanup,
// post-issuance-cleanup, and revocation-deletion controllers.
func deleteDesires(
	ctx context.Context,
	kubeApplierClient database.KubeApplierDBClient,
	parent desireParent,
	subscriptionID, resourceGroupName, hcpClusterName string,
	matches func(desireName string) bool,
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
	for _, desire := range applyIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !matches(desireName) {
			continue
		}
		removed, err := removeApplyDesireForDeletion(ctx, desireName, applyCRUD)
		if err != nil {
			return nil, err
		}
		if !removed {
			waitingFor = append(waitingFor, fmt.Sprintf("ApplyDesire %q", desireName))
		}
	}
	if err := applyIter.GetError(); err != nil {
		return nil, utils.TrackError(fmt.Errorf("iterate ApplyDesires: %w", err))
	}

	// Step 2: delete each matching ReadDesire directly.
	readIter, err := readCRUD.List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("list ReadDesires: %w", err))
	}
	for _, desire := range readIter.Items(ctx) {
		desireName := desire.ResourceID.Name
		if !matches(desireName) {
			continue
		}
		if err := readCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
			return nil, utils.TrackError(fmt.Errorf("delete ReadDesire %s: %w", desireName, err))
		}
	}
	if err := readIter.GetError(); err != nil {
		return nil, utils.TrackError(fmt.Errorf("iterate ReadDesires: %w", err))
	}

	return waitingFor, nil
}

// removeApplyDesireForDeletion tears down a single ApplyDesire by converting it
// to a Type=Delete desire (so the kube-applier deletes spec.targetItem from the
// management cluster) and, once that delete reports success, removing the desire
// document. It returns true once the ApplyDesire is gone.
func removeApplyDesireForDeletion(
	ctx context.Context,
	desireName string,
	applyCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
) (bool, error) {
	applyDesire, err := applyCRUD.Get(ctx, strings.ToLower(desireName))
	if database.IsNotFoundError(err) {
		// Already gone.
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

	// If the desire is still a ServerSideApply, flip it to a Delete so the
	// kube-applier tears down the applied object. TargetItem already names what
	// to delete; the ServerSideApply payload is cleared.
	if applyDesire.Spec.Type != kubeapplier.ApplyDesireTypeDelete {
		applyDesire.Spec.Type = kubeapplier.ApplyDesireTypeDelete
		applyDesire.Spec.ServerSideApply = nil
		if _, err := applyCRUD.Replace(ctx, applyDesire, nil); err != nil && !database.IsNotFoundError(err) {
			return false, utils.TrackError(fmt.Errorf("convert ApplyDesire %s to Delete: %w", desireName, err))
		}
		return false, nil
	}

	// The desire is a Delete — remove the document once the delete has succeeded.
	for _, cond := range applyDesire.Status.Conditions {
		if cond.Type == kubeapplier.ConditionTypeSuccessful && cond.Status == "True" {
			if err := applyCRUD.Delete(ctx, strings.ToLower(desireName)); err != nil && !database.IsNotFoundError(err) {
				return false, utils.TrackError(fmt.Errorf("delete ApplyDesire %s: %w", desireName, err))
			}
			return true, nil
		}
	}
	// Delete not yet successful; wait.
	return false, nil
}
