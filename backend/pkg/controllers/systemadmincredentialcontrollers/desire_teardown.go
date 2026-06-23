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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// desireSuccessfulConditionType is the well-known Condition Type a
// kube-applier sets on Apply/Read/Delete desires once their work is
// confirmed complete on the MC. See internal/api/kubeapplier/types_*.go.
const desireSuccessfulConditionType = "Successful"

// teardownCredentialOutstandingDesires is the shared engine used by
// controllers #5 (revoke poll Phase R-2), #6 (cluster-deletion gate)
// and #7 (post-issuance cleanup). For every entry in
// credential.Status.OutstandingDesires it ensures a matching
// DeleteDesire exists (for Apply refs), waits for Successful=True, then
// drops the Apply/Delete/Read Cosmos docs and prunes the corresponding
// refs.
//
// The caller already owns "what list of desires belongs to this
// credential"; this function only sweeps the existing list. It never
// adds new credential-scope work outside the tear-down domain.
//
// Returns the number of OutstandingDesires entries that are still alive
// after the sweep. When zero, the credential has no live MC content of
// its own.
//
// Inputs:
//   - kaClient: the kube-applier DB client for the MC that owns the
//     credential's desires. The caller must resolve this from the
//     parent cluster's ServiceProviderCluster placement before calling.
//   - resourcesDBClient: needed only so the credential doc itself can
//     be patched (the credential lives in the resources container, not
//     the kube-applier container).
//   - credential: the doc whose OutstandingDesires we are sweeping. Its
//     resource ID identifies the cluster scope, so the function can
//     look up Apply/Read/Delete CRUDs for it.
func teardownCredentialOutstandingDesires(
	ctx context.Context,
	kaClient database.KubeApplierDBClient,
	resourcesDBClient database.ResourcesDBClient,
	credential *api.SystemAdminCredential,
) (remaining int, err error) {
	logger := utils.LoggerFromContext(ctx)

	rid := credential.GetResourceID()
	if rid == nil {
		return 0, fmt.Errorf("credential is missing CosmosMetadata.ResourceID")
	}
	parentClusterRID := rid.Parent
	if parentClusterRID == nil {
		return 0, fmt.Errorf("credential resource ID has no parent cluster: %s", rid.String())
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

	// Index the credential's outstanding entries by Kind so we can
	// detect "Apply present but no Delete yet" and "Apply + Delete both
	// present, check Successful".
	applyByName := map[string]struct{}{}
	readByName := map[string]struct{}{}
	deleteByName := map[string]struct{}{}
	for _, ref := range credential.Status.OutstandingDesires {
		switch ref.Kind {
		case api.SystemAdminCredentialDesireKindApply:
			applyByName[ref.Name] = struct{}{}
		case api.SystemAdminCredentialDesireKindRead:
			readByName[ref.Name] = struct{}{}
		case api.SystemAdminCredentialDesireKindDelete:
			deleteByName[ref.Name] = struct{}{}
		}
	}

	// Step 1: for each ApplyDesire without a corresponding DeleteDesire,
	// fetch the ApplyDesire to read its TargetItem, build a DeleteDesire
	// with the same TargetItem, and Create it. Append the new ref onto
	// OutstandingDesires.
	for applyName := range applyByName {
		if _, ok := deleteByName[applyName]; ok {
			continue
		}
		apply, err := applyCRUD.Get(ctx, applyName)
		if database.IsNotFoundError(err) {
			// ApplyDesire already gone — prune the ref directly.
			pruneOutstandingDesire(credential, api.SystemAdminCredentialDesireKindApply, applyName)
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("get ApplyDesire %q: %w", applyName, err)
		}
		// Build a DeleteDesire pointing at the same TargetItem. We name
		// it after the ApplyDesire so the two are obviously paired in
		// Cosmos listings; the kube-applier doesn't otherwise care.
		delete := &kubeapplier.DeleteDesire{
			CosmosMetadata: buildScopedDesireMetadata(parentClusterRID, applyName, kubeapplier.DeleteDesireResourceTypeName, apply.Spec.ManagementCluster),
			Spec: kubeapplier.DeleteDesireSpec{
				ManagementCluster: apply.Spec.ManagementCluster,
				TargetItem:        apply.Spec.TargetItem,
			},
		}
		if _, err := deleteCRUD.Create(ctx, delete, nil); err != nil && !database.IsConflictError(err) {
			return 0, fmt.Errorf("create DeleteDesire %q: %w", applyName, err)
		}
		credential.Status.OutstandingDesires = append(credential.Status.OutstandingDesires, api.SystemAdminCredentialDesireRef{
			Kind: api.SystemAdminCredentialDesireKindDelete,
			Name: applyName,
		})
		deleteByName[applyName] = struct{}{}
		logger.Info("issued DeleteDesire for ApplyDesire", "name", applyName)
	}

	// Step 2: for each DeleteDesire, check Successful. If True, delete
	// Apply + Delete Cosmos docs and prune their refs.
	for deleteName := range deleteByName {
		dd, err := deleteCRUD.Get(ctx, deleteName)
		if database.IsNotFoundError(err) {
			// DeleteDesire already gone. Treat as success and prune.
			pruneOutstandingDesire(credential, api.SystemAdminCredentialDesireKindDelete, deleteName)
			if _, ok := applyByName[deleteName]; ok {
				if err := applyCRUD.Delete(ctx, deleteName); err != nil && !database.IsNotFoundError(err) {
					return 0, fmt.Errorf("delete ApplyDesire %q: %w", deleteName, err)
				}
				pruneOutstandingDesire(credential, api.SystemAdminCredentialDesireKindApply, deleteName)
			}
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("get DeleteDesire %q: %w", deleteName, err)
		}
		if !isDesireConditionTrue(dd.Status.Conditions, desireSuccessfulConditionType) {
			// Still draining — leave both Apply and Delete refs in place.
			continue
		}
		// Successful. Drop ApplyDesire + DeleteDesire Cosmos docs and
		// prune their refs.
		if _, ok := applyByName[deleteName]; ok {
			if err := applyCRUD.Delete(ctx, deleteName); err != nil && !database.IsNotFoundError(err) {
				return 0, fmt.Errorf("delete ApplyDesire %q: %w", deleteName, err)
			}
			pruneOutstandingDesire(credential, api.SystemAdminCredentialDesireKindApply, deleteName)
		}
		if err := deleteCRUD.Delete(ctx, deleteName); err != nil && !database.IsNotFoundError(err) {
			return 0, fmt.Errorf("delete DeleteDesire %q: %w", deleteName, err)
		}
		pruneOutstandingDesire(credential, api.SystemAdminCredentialDesireKindDelete, deleteName)
		logger.Info("teardown complete for desire", "name", deleteName)
	}

	// Step 3: ReadDesires never delivered MC content; the kube-applier
	// only mirrors. Delete the Cosmos doc directly and prune the ref.
	for readName := range readByName {
		if err := readCRUD.Delete(ctx, readName); err != nil && !database.IsNotFoundError(err) {
			return 0, fmt.Errorf("delete ReadDesire %q: %w", readName, err)
		}
		pruneOutstandingDesire(credential, api.SystemAdminCredentialDesireKindRead, readName)
	}

	return len(credential.Status.OutstandingDesires), nil
}

// pruneOutstandingDesire removes the first matching ref from the
// credential's OutstandingDesires slice. Idempotent: pruning a ref that
// is no longer present is a no-op.
func pruneOutstandingDesire(credential *api.SystemAdminCredential, kind api.SystemAdminCredentialDesireKind, name string) {
	out := credential.Status.OutstandingDesires[:0]
	dropped := false
	for _, ref := range credential.Status.OutstandingDesires {
		if !dropped && ref.Kind == kind && ref.Name == name {
			dropped = true
			continue
		}
		out = append(out, ref)
	}
	credential.Status.OutstandingDesires = out
}

// isDesireConditionTrue reports whether the given condition type is
// present with Status=True. False otherwise — including the absence of
// the condition entirely (i.e. the kube-applier has not yet decided).
func isDesireConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	c := apimeta.FindStatusCondition(conditions, conditionType)
	if c == nil {
		return false
	}
	return c.Status == metav1.ConditionTrue
}

// buildScopedDesireMetadata is a small construction helper. The
// kube-applier informer + lister key off the desire's full resource ID,
// derived from the parent cluster + the desire-kind segment + the
// desire's name. mcRID is the management-cluster resource ID used as
// the Cosmos partition key.
func buildScopedDesireMetadata(parentClusterRID *azcorearm.ResourceID, name, desireTypeSegment string, mcRID *azcorearm.ResourceID) api.CosmosMetadata {
	// e.g. .../clusters/<cluster>/applyDesires/<name>
	rid, _ := azcorearm.ParseResourceID(parentClusterRID.String() + "/" + desireTypeSegment + "/" + name)
	return api.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(mcRID.String())}
}
