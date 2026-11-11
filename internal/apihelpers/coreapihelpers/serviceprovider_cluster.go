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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
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
//
// This is a low-level "last-observed managed" primitive. It deliberately does NOT
// consult desire.Spec.Action or the stack's success condition:
//   - The mock FPA stack is always an Apply (the role-assignments controller never
//     publishes a Delete - the "no keys" case publishes an empty-resources Apply that
//     unmanages via ActionOnUnmanage), so there is no Delete action to distinguish here.
//   - "Managed per the current, successful reconcile" (as opposed to a stale or in-flight
//     observation) is a caller concern: callers that need it gate on ObservedSpecHash and
//     the SuccessfullyApplied condition before/around this check (see
//     MockFPARoleAssignmentsConfigured and the intent controller's
//     roleAssignmentSuccessfullyConfigured). The SuccessfullyApplied condition is itself
//     Apply-specific, so it also excludes any Delete desire.
//
// If this is ever reused for a stack that can be deleted, the caller must gate on the
// action/condition (or this function must start checking desire.Spec.Action).
func MockFPARoleAssignmentStatusManaged(status *coreapi.MockFPARoleAssignmentStatus, desire *azureresourcesapi.DeploymentStackDesire) bool {
	// Even if a role assignment is still present in Azure, if it has been deconfigured we consider it as not managed.
	if status.DeconfigureTimestamp != nil {
		return false
	}

	// If the role assignment has not been reserved yet it is not managed as we need to have a resource ID to specify it in the deployment stack desire.
	if status.ReservedAzureResourceID == nil {
		return false
	}

	// If the deployment stack desire has not been published yet it is not managed as we need to have a stack to specify the role assignment in.
	if desire == nil || desire.Status.Stack == nil || desire.Status.Stack.Properties == nil {
		return false
	}

	for _, resource := range desire.Status.Stack.Properties.Resources {
		// If the resource is nil or does not have an ID or status we skip it.
		if resource == nil || resource.ID == nil || resource.Status == nil {
			continue
		}
		if strings.EqualFold(*resource.ID, status.ReservedAzureResourceID.String()) {
			return *resource.Status == armdeploymentstacks.ResourceStatusModeManaged
		}
	}

	return false
}

// MockFPARoleAssignmentsConfigured reports whether every mock
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
//
// desire may be nil: the caller passes nil when the mock FPA DeploymentStackDesire has
// not been published yet. With no published/observed stack a stamped assignment cannot
// have been reconciled, so a nil desire returns (false, nil).
//
// It returns an error only for an internal failure (hashing the spec), which the caller
// should surface rather than treat as "not configured".
func MockFPARoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus, desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	// If the status has no role assignments we return false.
	if len(status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup) == 0 {
		return false, nil
	}

	if desire == nil {
		return false, nil
	}

	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if err != nil {
		return false, fmt.Errorf("failed to hash mock FPA role assignments DeploymentStackDesire spec: %w", err)
	}

	// The observed spec belongs to an older spec than the one currently published, so
	// it says nothing about whether the current desired set has reconciled: do not consider as managed until a fresh observation lands.
	if desire.Status.ObservedSpecHash != hash {
		return false, nil
	}

	// If the apply has not succeeded we do not consider the role assignments as managed.
	if !meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
		return false, nil
	}

	for _, assignment := range status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup {
		if !MockFPARoleAssignmentStatusManaged(assignment, desire) {
			return false, nil
		}
	}
	return true, nil
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
// because nothing had added it to status at all. True (vacuously configured) if
// expectedRoleDefinitionResourceIDs is empty, since there is nothing to configure.
// False if any expected role definition has no entry, is draining
// (DeconfigureTimestamp set), or is not yet managed.
func ServiceProviderClusterStatusMockFPARoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus, desire *azureresourcesapi.DeploymentStackDesire, principalID string, expectedRoleDefinitionResourceIDs []string) bool {
	for _, roleDefinitionResourceID := range expectedRoleDefinitionResourceIDs {
		key := coreapi.RoleAssignmentKey{
			PrincipalID:              principalID,
			RoleDefinitionResourceID: roleDefinitionResourceID,
		}
		assignment, ok := status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[key]
		if !ok || assignment.DeconfigureTimestamp != nil || !MockFPARoleAssignmentStatusManaged(assignment, desire) {
			return false
		}
	}
	return true
}
