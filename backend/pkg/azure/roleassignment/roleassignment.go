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

package roleassignment

import (
	"fmt"

	"github.com/google/uuid"
)

// roleAssignmentsNamespaceUUID is the fixed UUIDv5 namespace Cluster Service uses
// to derive managed-resource-group-scoped role assignment names. It must never
// change: it is part of the contract that makes the names deterministic across
// Cluster Service and the backend.
const roleAssignmentsNamespaceUUID = "c14decbc-0526-4a6c-be15-9fc305cefe6b"

// roleAssignmentsResourceIDInfix is the ARM resource-type segment that sits
// between a scope and a role assignment name in a role assignment's resource ID.
const roleAssignmentsResourceIDInfix = "/providers/Microsoft.Authorization/roleAssignments/"

// GenerateManagedResourceGroupScopedRoleAssignmentName returns the deterministic
// role assignment name Cluster Service assigns to the role assignment created for
// principalId + roleDefinitionResourceId at the given managed-resource-group
// scope. It is UUIDv5(roleAssignmentsNamespaceUUID, "{scope}${principalId}${roleDefinitionResourceId}").
//
// scope must be the managed resource group scope
// ("/subscriptions/{subscriptionId}/resourceGroups/{managedResourceGroupName}"),
// principalId the identity's Azure principal ID, and roleDefinitionResourceId the
// tenant-level role definition resource ID
// ("/providers/Microsoft.Authorization/roleDefinitions/{guid}"). See the package
// comment: these inputs must match Cluster Service exactly.
func GenerateManagedResourceGroupScopedRoleAssignmentName(scope, principalID, roleDefinitionResourceID string) string {
	input := fmt.Sprintf("%s$%s$%s", scope, principalID, roleDefinitionResourceID)
	return uuid.NewSHA1(uuid.MustParse(roleAssignmentsNamespaceUUID), []byte(input)).String()
}

// ManagedResourceGroupScopedRoleAssignmentResourceID returns the full ARM
// resource ID of the managed-resource-group-scoped role assignment for
// principalId + roleDefinitionResourceId, i.e.
// "{scope}/providers/Microsoft.Authorization/roleAssignments/{name}" where name
// is GenerateManagedResourceGroupScopedRoleAssignmentName(scope, principalId, roleDefinitionResourceId).
func ManagedResourceGroupScopedRoleAssignmentResourceID(scope, principalID, roleDefinitionResourceID string) string {
	name := GenerateManagedResourceGroupScopedRoleAssignmentName(scope, principalID, roleDefinitionResourceID)
	return scope + roleAssignmentsResourceIDInfix + name
}
