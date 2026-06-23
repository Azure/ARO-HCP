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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// teardownRevocationOutstandingDesires mirrors teardownCredentialOutstandingDesires
// for SystemAdminRevocation. The shape is identical — for every ApplyDesire
// entry it issues a DeleteDesire, waits for Successful=True, then drops the
// Apply/Delete Cosmos docs and prunes the corresponding refs. ReadDesires
// are simply deleted.
//
// Returns the number of OutstandingDesires entries that are still alive
// after the sweep. When zero, the revocation has no live MC content of
// its own.
func teardownRevocationOutstandingDesires(
	ctx context.Context,
	kaClient database.KubeApplierDBClient,
	revocation *api.SystemAdminRevocation,
) (remaining int, err error) {
	logger := utils.LoggerFromContext(ctx)

	rid := revocation.GetResourceID()
	if rid == nil {
		return 0, fmt.Errorf("revocation is missing CosmosMetadata.ResourceID")
	}
	parentClusterRID := rid.Parent
	if parentClusterRID == nil {
		return 0, fmt.Errorf("revocation resource ID has no parent cluster: %s", rid.String())
	}
	sub := parentClusterRID.SubscriptionID
	rg := parentClusterRID.ResourceGroupName
	clusterName := parentClusterRID.Name

	applyCRUD, err := kaClient.ApplyDesiresForCluster(sub, rg, clusterName)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve ApplyDesires CRUD: %w", err)
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(sub, rg, clusterName)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve ReadDesires CRUD: %w", err)
	}
	deleteCRUD, err := kaClient.DeleteDesiresForCluster(sub, rg, clusterName)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve DeleteDesires CRUD: %w", err)
	}

	applyByName := map[string]struct{}{}
	readByName := map[string]struct{}{}
	deleteByName := map[string]struct{}{}
	for _, ref := range revocation.Status.OutstandingDesires {
		switch ref.Kind {
		case api.SystemAdminRevocationDesireKindApply:
			applyByName[ref.Name] = struct{}{}
		case api.SystemAdminRevocationDesireKindRead:
			readByName[ref.Name] = struct{}{}
		case api.SystemAdminRevocationDesireKindDelete:
			deleteByName[ref.Name] = struct{}{}
		}
	}

	// Step 1: ensure a DeleteDesire exists for every ApplyDesire.
	for applyName := range applyByName {
		if _, ok := deleteByName[applyName]; ok {
			continue
		}
		apply, err := applyCRUD.Get(ctx, applyName)
		if database.IsNotFoundError(err) {
			pruneOutstandingRevocationDesire(revocation, api.SystemAdminRevocationDesireKindApply, applyName)
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("get ApplyDesire %q: %w", applyName, err)
		}
		dd := &kubeapplier.DeleteDesire{
			CosmosMetadata: buildScopedDesireMetadata(parentClusterRID, applyName, kubeapplier.DeleteDesireResourceTypeName),
			Spec: kubeapplier.DeleteDesireSpec{
				ManagementCluster: apply.Spec.ManagementCluster,
				TargetItem:        apply.Spec.TargetItem,
			},
		}
		if _, err := deleteCRUD.Create(ctx, dd, nil); err != nil && !database.IsConflictError(err) {
			return 0, fmt.Errorf("create DeleteDesire %q: %w", applyName, err)
		}
		revocation.Status.OutstandingDesires = append(revocation.Status.OutstandingDesires, api.SystemAdminRevocationDesireRef{
			Kind: api.SystemAdminRevocationDesireKindDelete,
			Name: applyName,
		})
		deleteByName[applyName] = struct{}{}
		logger.Info("issued DeleteDesire for ApplyDesire", "name", applyName)
	}

	// Step 2: for each DeleteDesire, check Successful. If True, drop both
	// Apply + Delete Cosmos docs and prune the refs.
	for deleteName := range deleteByName {
		dd, err := deleteCRUD.Get(ctx, deleteName)
		if database.IsNotFoundError(err) {
			pruneOutstandingRevocationDesire(revocation, api.SystemAdminRevocationDesireKindDelete, deleteName)
			if _, ok := applyByName[deleteName]; ok {
				if err := applyCRUD.Delete(ctx, deleteName); err != nil && !database.IsNotFoundError(err) {
					return 0, fmt.Errorf("delete ApplyDesire %q: %w", deleteName, err)
				}
				pruneOutstandingRevocationDesire(revocation, api.SystemAdminRevocationDesireKindApply, deleteName)
			}
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("get DeleteDesire %q: %w", deleteName, err)
		}
		if !isDesireConditionTrue(dd.Status.Conditions, desireSuccessfulConditionType) {
			continue
		}
		if _, ok := applyByName[deleteName]; ok {
			if err := applyCRUD.Delete(ctx, deleteName); err != nil && !database.IsNotFoundError(err) {
				return 0, fmt.Errorf("delete ApplyDesire %q: %w", deleteName, err)
			}
			pruneOutstandingRevocationDesire(revocation, api.SystemAdminRevocationDesireKindApply, deleteName)
		}
		if err := deleteCRUD.Delete(ctx, deleteName); err != nil && !database.IsNotFoundError(err) {
			return 0, fmt.Errorf("delete DeleteDesire %q: %w", deleteName, err)
		}
		pruneOutstandingRevocationDesire(revocation, api.SystemAdminRevocationDesireKindDelete, deleteName)
		logger.Info("teardown complete for desire", "name", deleteName)
	}

	// Step 3: ReadDesires never delivered MC content. Delete + prune.
	for readName := range readByName {
		if err := readCRUD.Delete(ctx, readName); err != nil && !database.IsNotFoundError(err) {
			return 0, fmt.Errorf("delete ReadDesire %q: %w", readName, err)
		}
		pruneOutstandingRevocationDesire(revocation, api.SystemAdminRevocationDesireKindRead, readName)
	}

	return len(revocation.Status.OutstandingDesires), nil
}

func pruneOutstandingRevocationDesire(revocation *api.SystemAdminRevocation, kind api.SystemAdminRevocationDesireKind, name string) {
	out := revocation.Status.OutstandingDesires[:0]
	dropped := false
	for _, ref := range revocation.Status.OutstandingDesires {
		if !dropped && ref.Kind == kind && ref.Name == name {
			dropped = true
			continue
		}
		out = append(out, ref)
	}
	revocation.Status.OutstandingDesires = out
}
