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

package coreapihelpers

import (
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// IsValidHostedClusterControlPlaneSize reports whether s names a known tier.
func IsValidHostedClusterControlPlaneSize(s string) bool {
	switch coreapi.HostedClusterControlPlaneSize(s) {
	case coreapi.HostedClusterControlPlaneSizeSmall,
		coreapi.HostedClusterControlPlaneSizeMedium,
		coreapi.HostedClusterControlPlaneSizeLarge,
		coreapi.HostedClusterControlPlaneSizeXlarge,
		coreapi.HostedClusterControlPlaneSizeXXlarge:
		return true
	}
	return false
}

// RoleAssignmentStatusManaged reports whether status's reserved
// ReservedAzureResourceID is currently present and "managed" per desire's
// last-observed Azure state. A draining assignment (DeconfigureTimestamp set)
// is not managed for this purpose even if Azure still has it, a nil
// ReservedAzureResourceID (never reserved yet) is not managed, and a nil desire or
// desire.Status.Stack (never published or never observed) is not managed.
func RoleAssignmentStatusManaged(status *coreapi.RoleAssignmentStatus, desire *coreapi.StackDesire) bool {
	if status.DeconfigureTimestamp != nil || status.ReservedAzureResourceID == nil {
		return false
	}
	if desire == nil || desire.Status.Stack == nil || desire.Status.Stack.Properties == nil {
		return false
	}
	for _, resource := range desire.Status.Stack.Properties.Resources {
		if resource == nil || resource.ID == nil || resource.Status == nil {
			continue
		}
		if strings.EqualFold(*resource.ID, status.ReservedAzureResourceID.String()) {
			return *resource.Status == armdeploymentstacks.ResourceStatusModeManaged
		}
	}
	return false
}

// RoleAssignmentRemovedAndReconciled reports whether a role assignment that has
// left the desired set (its DeconfigureTimestamp is stamped) has actually been
// removed from Azure by a successful, current reconciliation of desire. It is the
// drop criterion for reconciliation-based deconfigure (no time-based cooldown):
// the new desired set must be published in the stack and that stack must have
// reconciled successfully with it.
//
//   - A status whose ReservedAzureResourceID is nil was never materialized into a
//     stack, so there is nothing in Azure to remove: removed.
//   - Otherwise desire must exist, its observed outcome must correspond to the
//     currently published spec (ObservedSpecHash equals StackDesireSpecHash of
//     desire.Spec), and that outcome must be a success: a confirmed Delete, or an
//     Apply after which the reserved assignment is no longer managed by the stack
//     (dropped from the template and deleted via ActionOnUnmanage).
func RoleAssignmentRemovedAndReconciled(status *coreapi.RoleAssignmentStatus, desire *coreapi.StackDesire) bool {
	if status == nil {
		return false
	}
	if status.ReservedAzureResourceID == nil {
		return true
	}
	if desire == nil {
		return false
	}
	hash, err := coreapi.StackDesireSpecHash(desire.Spec)
	if err != nil || desire.Status.ObservedSpecHash != hash {
		return false
	}
	if desire.Spec.Action == coreapi.StackDesireActionDelete {
		return meta.IsStatusConditionTrue(desire.Status.Conditions, coreapi.StackDesireConditionSuccessfullyDeleted)
	}
	if !meta.IsStatusConditionTrue(desire.Status.Conditions, coreapi.StackDesireConditionSuccessfullyApplied) {
		return false
	}
	if desire.Status.Stack == nil || desire.Status.Stack.Properties == nil {
		return true
	}
	for _, resource := range desire.Status.Stack.Properties.Resources {
		if resource == nil || resource.ID == nil || resource.Status == nil {
			continue
		}
		if strings.EqualFold(*resource.ID, status.ReservedAzureResourceID.String()) {
			// Still tracked by the stack: removed only if no longer managed.
			return *resource.Status != armdeploymentstacks.ResourceStatusModeManaged
		}
	}
	return true
}

// MockFirstPartyApplicationRoleAssignmentsConfigured reports whether every mock
// FPA role assignment over the managed resource group is live in Azure per a
// current, successful reconciliation of the mock FPA StackDesire. It gates the
// FPA-authenticated operator role assignments stack, which can only create
// assignments over the managed resource group once the mock FPA principal holds
// Owner there.
//
// It returns true only when all of the following hold, which is what distinguishes
// "the stack reconciled the current desired set" from "the map is not filled yet"
// or "the observation is stale":
//   - the map is non-empty (the mock intent has recorded what it wants), and
//   - the mock StackDesire's observed outcome corresponds to its currently
//     published spec (ObservedSpecHash equals StackDesireSpecHash of desire.Spec)
//     and is a successful Apply, so "managed" cannot reflect a stale reconcile or
//     a spec the publisher has not reconciled yet, and
//   - every entry is managed and not draining.
func MockFirstPartyApplicationRoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus, desire *coreapi.StackDesire) bool {
	if status == nil || len(status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup) == 0 {
		return false
	}
	if desire == nil {
		return false
	}
	hash, err := coreapi.StackDesireSpecHash(desire.Spec)
	if err != nil || desire.Status.ObservedSpecHash != hash {
		return false
	}
	if !meta.IsStatusConditionTrue(desire.Status.Conditions, coreapi.StackDesireConditionSuccessfullyApplied) {
		return false
	}
	for _, assignment := range status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup {
		if assignment == nil || !RoleAssignmentStatusManaged(assignment, desire) {
			return false
		}
	}
	return true
}

// ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured reports
// whether every one of principalID's expectedRoleDefinitionResourceIDs is
// RoleAssignmentStatusManaged against desire. expectedRoleDefinitionResourceIDs
// must come from the same source ClusterRoleAssignmentIntent uses
// (ClusterScopedIdentitiesConfig) rather than being inferred from whatever is
// already present on status: status.RoleAssignmentsOverManagedResourceGroup
// lags ClusterScopedIdentitiesConfig by however long ClusterRoleAssignmentIntent
// takes to reconcile a config change, so a check that only scanned status
// would report "configured" for a role definition that was never desired yet
// because nothing had added it to status at all. False if
// expectedRoleDefinitionResourceIDs is empty, or if any expected role
// definition has no entry, is draining (DeconfigureTimestamp set), or is not
// yet managed.
func ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus, desire *coreapi.StackDesire, principalID string, expectedRoleDefinitionResourceIDs []*azcorearm.ResourceID) bool {
	if len(expectedRoleDefinitionResourceIDs) == 0 {
		return false
	}
	for _, roleDefinitionResourceID := range expectedRoleDefinitionResourceIDs {
		key := coreapi.RoleAssignmentKey{
			PrincipalID:              principalID,
			RoleDefinitionResourceID: roleDefinitionResourceID.String(),
		}
		assignment, ok := status.RoleAssignmentsOverManagedResourceGroup[key]
		if !ok || assignment.DeconfigureTimestamp != nil || !RoleAssignmentStatusManaged(assignment, desire) {
			return false
		}
	}
	return true
}
