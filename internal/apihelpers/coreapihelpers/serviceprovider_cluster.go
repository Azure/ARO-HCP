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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

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

// ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured reports
// whether every currently desired (non-draining) role assignment on status
// for identity resourceID's principalID is RoleAssignmentStatusManaged
// against desire. False when no key currently desires the principal, or when
// any desired key is not yet managed.
func ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(status *coreapi.ServiceProviderClusterStatus, desire *coreapi.StackDesire, identityResourceID *azcorearm.ResourceID, principalID string) bool {
	foundDesired := false
	for key, assignment := range status.RoleAssignmentsOverManagedResourceGroup {
		if !controllerutils.ResourceIDsEqual(assignment.TargetIdentity.ResourceID, identityResourceID) || key.PrincipalID != principalID {
			continue
		}
		if assignment.DeconfigureTimestamp != nil {
			continue
		}
		foundDesired = true
		if !RoleAssignmentStatusManaged(assignment, desire) {
			return false
		}
	}
	return foundDesired
}
