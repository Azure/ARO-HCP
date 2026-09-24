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

import "testing"

const (
	// testVectorScope, testVectorPrincipalID and testVectorRoleDefinitionID are a
	// fixed set of inputs used as a golden test vector.
	testVectorScope            = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-managed-rg"
	testVectorPrincipalID      = "11111111-1111-1111-1111-111111111111"
	testVectorRoleDefinitionID = "/providers/Microsoft.Authorization/roleDefinitions/88366f10-ed47-4cc0-9fab-c8a06148393e"

	// testVectorExpectedName is the UUIDv5 that Cluster Service's algorithm produces
	// for the inputs above. It is hardcoded (not recomputed from the same code) so
	// that any change to the namespace UUID, the "$" input format, or the input
	// ordering is caught as a regression. If this value ever needs to change, the
	// generated role assignment names have changed and Cluster Service must change
	// in lockstep, or the cluster-create gate will hang forever.
	testVectorExpectedName = "c884371f-1ce2-537c-a54a-4e41467f6f81"
)

func TestGenerateManagedResourceGroupScopedRoleAssignmentName(t *testing.T) {
	t.Parallel()

	got := GenerateManagedResourceGroupScopedRoleAssignmentName(testVectorScope, testVectorPrincipalID, testVectorRoleDefinitionID)
	if got != testVectorExpectedName {
		t.Fatalf("role assignment name mismatch: got %q, want %q (Cluster Service name generation contract broken)", got, testVectorExpectedName)
	}
}

func TestGenerateManagedResourceGroupScopedRoleAssignmentNameIsDeterministic(t *testing.T) {
	t.Parallel()

	first := GenerateManagedResourceGroupScopedRoleAssignmentName(testVectorScope, testVectorPrincipalID, testVectorRoleDefinitionID)
	second := GenerateManagedResourceGroupScopedRoleAssignmentName(testVectorScope, testVectorPrincipalID, testVectorRoleDefinitionID)
	if first != second {
		t.Fatalf("name generation is not deterministic: %q != %q", first, second)
	}
}

func TestGenerateManagedResourceGroupScopedRoleAssignmentNameVariesByInput(t *testing.T) {
	t.Parallel()

	base := GenerateManagedResourceGroupScopedRoleAssignmentName(testVectorScope, testVectorPrincipalID, testVectorRoleDefinitionID)

	differentPrincipal := GenerateManagedResourceGroupScopedRoleAssignmentName(testVectorScope, "22222222-2222-2222-2222-222222222222", testVectorRoleDefinitionID)
	if base == differentPrincipal {
		t.Errorf("expected different name for a different principal ID, both were %q", base)
	}

	differentRoleDef := GenerateManagedResourceGroupScopedRoleAssignmentName(testVectorScope, testVectorPrincipalID, "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000")
	if base == differentRoleDef {
		t.Errorf("expected different name for a different role definition ID, both were %q", base)
	}

	differentScope := GenerateManagedResourceGroupScopedRoleAssignmentName("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/other-rg", testVectorPrincipalID, testVectorRoleDefinitionID)
	if base == differentScope {
		t.Errorf("expected different name for a different scope, both were %q", base)
	}
}

func TestManagedResourceGroupScopedRoleAssignmentResourceID(t *testing.T) {
	t.Parallel()

	got := ManagedResourceGroupScopedRoleAssignmentResourceID(testVectorScope, testVectorPrincipalID, testVectorRoleDefinitionID)
	want := testVectorScope + "/providers/Microsoft.Authorization/roleAssignments/" + testVectorExpectedName
	if got != want {
		t.Fatalf("role assignment resource ID mismatch: got %q, want %q", got, want)
	}
}

// TestGenerateManagedResourceGroupScopedRoleAssignmentNameMatchesClusterServiceCanonicalScope
// locks the cross-component naming contract with Cluster Service (ARO-29892).
//
// The backend feeds this generator a scope built from coreapi.ToResourceGroupResourceID,
// which lowercases the subscription ID and managed resource group name. Cluster Service
// must feed the identically cased scope through its own CanonicalManagedResourceGroupScope
// helper. Azure deduplicates role assignments by scope, principal and role definition
// rather than by name, so if the two sides hash scopes that differ only by casing (for
// example the "MC_" managed resource group prefix Azure returns versus the lowercased
// "mc_") they derive different names for the same assignment and the second writer wedges
// on RoleAssignmentExists.
//
// canonicalName is the same UUIDv5 Cluster Service pins for this canonical scope. If it
// changes here, Cluster Service must change in lockstep.
func TestGenerateManagedResourceGroupScopedRoleAssignmentNameMatchesClusterServiceCanonicalScope(t *testing.T) {
	t.Parallel()

	const (
		canonicalScope   = "/subscriptions/d8b8e5a0-1111-2222-3333-444455556666/resourceGroups/mc_ricohcp9232rg_ricohcp9232_canadacentral"
		principalID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		roleDefinitionID = "/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
		canonicalName    = "eed0283b-ecb0-5db2-90d8-0d15228b9f82"
	)

	got := GenerateManagedResourceGroupScopedRoleAssignmentName(canonicalScope, principalID, roleDefinitionID)
	if got != canonicalName {
		t.Fatalf("canonical role assignment name mismatch: got %q, want %q (Cluster Service contract broken)", got, canonicalName)
	}

	// A managed resource group scope that still carries Azure's upper-case "MC_" prefix
	// hashes to a different name, which is exactly why the scope must be canonicalized to
	// lower case before it reaches this generator.
	uncanonicalScope := "/subscriptions/d8b8e5a0-1111-2222-3333-444455556666/resourceGroups/MC_ricohcp9232rg_ricohcp9232_canadacentral"
	if GenerateManagedResourceGroupScopedRoleAssignmentName(uncanonicalScope, principalID, roleDefinitionID) == canonicalName {
		t.Fatal("expected a differently cased managed resource group scope to derive a different name")
	}
}
