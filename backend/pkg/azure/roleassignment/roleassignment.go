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

// Package roleassignment replicates Cluster Service's deterministic role
// assignment name generation so the backend can compute the exact names Cluster
// Service (CS) uses when it creates the managed-resource-group-scoped role
// assignments for a cluster's control-plane and data-plane managed identities.
//
// The algorithm and the fixed namespace UUID are copied verbatim from Cluster
// Service (openshift-online/aro-hcp-clusters-service,
// pkg/azure/roleassignment/roleassignment.go):
//
//	const roleAssignmentsNamespaceUuid = "c14decbc-0526-4a6c-be15-9fc305cefe6b"
//	func GenerateManagedResourceGroupScopedRoleAssignmentName(
//	    managedResourceGroupResourceId, principalId, roleDefinitionResourceId string) string {
//	    input := fmt.Sprintf("%s$%s$%s", managedResourceGroupResourceId, principalId, roleDefinitionResourceId)
//	    return uuid.NewSHA1(uuid.MustParse(roleAssignmentsNamespaceUuid), []byte(input)).String()
//	}
//
// The names are UUIDv5 (SHA-1) values, so they MUST be generated identically on
// both sides: any divergence in the namespace UUID, the "$" input format, or the
// exact string form of any input yields a different name, and the backend would
// then wait forever for a role assignment Cluster Service never created (the
// cluster-create gate would hang). Do NOT "improve" or reformat this logic.
//
// TODO(role-assignments): confirm with the Cluster Service team that the
// roleDefinitionResourceId string passed here byte-for-byte matches what CS
// passes. Both sides source the operator role definitions from the same
// cluster-scoped identities config, where the role definition resource IDs are
// tenant-level ("/providers/Microsoft.Authorization/roleDefinitions/{guid}"),
// so they are expected to match; this replication should be kept in lockstep
// with CS (ideally promoted to a shared module) rather than diverging.
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
