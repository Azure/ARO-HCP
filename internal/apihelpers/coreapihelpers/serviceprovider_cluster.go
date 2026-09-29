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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
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

// RoleAssignmentStatusConfigured reports whether this role assignment exists
// on the managed resource group. A draining assignment is not configured.
func RoleAssignmentStatusConfigured(status *coreapi.RoleAssignmentStatus) bool {
	return status.DeconfigureTimestamp == nil && status.AzureResource != nil
}

// ServiceProviderClusterStatusRoleAssignmentConfigured reports whether the
// given identity and role definition have an ensured managed-resource-group
// role assignment on status. A draining row is not configured. No matching
// key at all is also considered not configured.
//
// roleDefinitionResourceID is matched exactly, not case-insensitively like
// resourceID: it must match RoleAssignmentKey.RoleDefinitionResourceID
// byte-for-byte, since that value seeds the deterministic Azure role
// assignment name (see RoleAssignmentKey.RoleDefinitionResourceID).
func ServiceProviderClusterStatusRoleAssignmentConfigured(status *coreapi.ServiceProviderClusterStatus, resourceID *azcorearm.ResourceID, principalID string, roleDefinitionResourceID string) bool {
	assignment, ok := status.RoleAssignmentsOverManagedResourceGroup[coreapi.RoleAssignmentKey{
		PrincipalID:              principalID,
		RoleDefinitionResourceID: roleDefinitionResourceID,
	}]
	if !ok {
		return false
	}

	if !controllerutils.ResourceIDsEqual(assignment.TargetIdentity.ResourceID, resourceID) {
		return false
	}
	return RoleAssignmentStatusConfigured(assignment)
}

// ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured reports
// whether every currently desired role assignment on status for identity
// resourceID's principalID is configured. Keys with DeconfigureTimestamp set
// are ignored. False when no key currently desires the principal, or when
// any desired key has not yet been applied.
func ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus, identityResourceID *azcorearm.ResourceID, principalID string) bool {
	foundDesired := false
	for key, assignment := range status.RoleAssignmentsOverManagedResourceGroup {
		if !controllerutils.ResourceIDsEqual(assignment.TargetIdentity.ResourceID, identityResourceID) || key.PrincipalID != principalID {
			continue
		}
		if assignment.DeconfigureTimestamp != nil {
			continue
		}
		foundDesired = true
		if !RoleAssignmentStatusConfigured(assignment) {
			return false
		}
	}
	return foundDesired
}

// ServiceProviderClusterStatusDesiredRoleAssignmentsConfigured reports
// whether every currently desired role assignment on status.RoleAssignmentsOverManagedResourceGroup is configured.
// Keys with DeconfigureTimestamp set are ignored. False when no key
// currently desires an assignment, or when any desired key has not yet been
// applied.
func ServiceProviderClusterStatusDesiredRoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus) bool {
	foundDesired := false
	for _, assignment := range status.RoleAssignmentsOverManagedResourceGroup {
		if assignment.DeconfigureTimestamp != nil {
			continue
		}
		foundDesired = true
		if !RoleAssignmentStatusConfigured(assignment) {
			return false
		}
	}
	return foundDesired
}
