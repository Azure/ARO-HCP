package roleassignments

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

func TestEscapeODataString_EscapesSingleQuotes(t *testing.T) {
	t.Parallel()

	got := escapeODataString("O'Hara Team")
	if want := "O''Hara Team"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeID_TrimsAndLowercases(t *testing.T) {
	t.Parallel()

	got := normalizeID("  /SUBSCRIPTIONS/ABC  ")
	if want := "/subscriptions/abc"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestAssignmentWithinResourceGroupScope_UsesScopeWhenPresent(t *testing.T) {
	t.Parallel()

	role := &armauthorization.RoleAssignment{
		Properties: &armauthorization.RoleAssignmentProperties{
			Scope: strPtr("/subscriptions/abc/resourceGroups/rg-one/providers/Microsoft.Compute/virtualMachines/vm1"),
		},
	}

	if !assignmentWithinResourceGroupScope(role, "/subscriptions/abc/resourcegroups/") {
		t.Fatalf("expected assignment to be within resource-group scope")
	}
}

func TestAssignmentWithinResourceGroupScope_FallsBackToID(t *testing.T) {
	t.Parallel()

	role := &armauthorization.RoleAssignment{
		ID: strPtr("/subscriptions/abc/resourceGroups/rg-one/providers/Microsoft.Authorization/roleAssignments/ra1"),
	}

	if !assignmentWithinResourceGroupScope(role, "/subscriptions/abc/resourcegroups/") {
		t.Fatalf("expected assignment ID fallback to match resource-group scope")
	}
}

func TestAssignmentWithinResourceGroupScope_RejectsNonRGScope(t *testing.T) {
	t.Parallel()

	role := &armauthorization.RoleAssignment{
		Properties: &armauthorization.RoleAssignmentProperties{
			Scope: strPtr("/subscriptions/abc"),
		},
	}

	if assignmentWithinResourceGroupScope(role, "/subscriptions/abc/resourcegroups/") {
		t.Fatalf("expected non-resource-group scope to be rejected")
	}
}

func TestToRoleAssignmentRecord_ReturnsFalseWithoutID(t *testing.T) {
	t.Parallel()

	if _, ok := toRoleAssignmentRecord(&armauthorization.RoleAssignment{}); ok {
		t.Fatalf("expected conversion to fail without ID")
	}
}

func TestRoleAssignmentName_FallsBackToID(t *testing.T) {
	t.Parallel()

	role := &armauthorization.RoleAssignment{
		ID:   strPtr("/subscriptions/abc/resourceGroups/rg/providers/Microsoft.Authorization/roleAssignments/ra1"),
		Name: strPtr(""),
	}

	if got, want := roleAssignmentName(role, "fallback-id"), "fallback-id"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func strPtr(value string) *string { return &value }
