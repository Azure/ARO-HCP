// Copyright 2025 Microsoft Corporation
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

package systemadmincredential

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func testOwner(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	ownerID, err := azcorearm.ParseResourceID("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1")
	if err != nil {
		t.Fatalf("ParseResourceID() error = %v", err)
	}
	return ownerID
}

func TestBuildRBACGiveCSRPerm(t *testing.T) {
	owner := testOwner(t)
	objs := BuildRBACGiveCSRPerm(owner, "abcdef1234567890")

	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}

	cr, ok := objs[0].(*rbacv1.ClusterRole)
	if !ok {
		t.Fatalf("first object is not ClusterRole: %T", objs[0])
	}
	crb, ok := objs[1].(*rbacv1.ClusterRoleBinding)
	if !ok {
		t.Fatalf("second object is not ClusterRoleBinding: %T", objs[1])
	}

	expectedName := "system-admin-credential-give-csr-perm-abcdef1234567890"
	if cr.Name != expectedName {
		t.Errorf("ClusterRole name = %q, want %q", cr.Name, expectedName)
	}
	if crb.Name != expectedName {
		t.Errorf("ClusterRoleBinding name = %q, want %q", crb.Name, expectedName)
	}

	// Verify owner annotation on both
	if cr.Annotations[OwnerAnnotationKey] == "" {
		t.Error("ClusterRole missing owner annotation")
	}
	if crb.Annotations[OwnerAnnotationKey] == "" {
		t.Error("ClusterRoleBinding missing owner annotation")
	}

	// Verify RBAC rules
	if len(cr.Rules) == 0 {
		t.Error("ClusterRole has no rules")
	}
}

func TestBuildRBACCSRA(t *testing.T) {
	owner := testOwner(t)
	objs := BuildRBACCSRA(owner, "abcdef1234567890", "clusters-cluster1")

	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}

	role, ok := objs[0].(*rbacv1.Role)
	if !ok {
		t.Fatalf("first object is not Role: %T", objs[0])
	}
	rb, ok := objs[1].(*rbacv1.RoleBinding)
	if !ok {
		t.Fatalf("second object is not RoleBinding: %T", objs[1])
	}

	expectedName := "system-admin-credential-csra-perm-abcdef1234567890"
	if role.Name != expectedName {
		t.Errorf("Role name = %q, want %q", role.Name, expectedName)
	}
	if role.Namespace != "clusters-cluster1" {
		t.Errorf("Role namespace = %q, want %q", role.Namespace, "clusters-cluster1")
	}
	if rb.Name != expectedName {
		t.Errorf("RoleBinding name = %q, want %q", rb.Name, expectedName)
	}
	if rb.Namespace != "clusters-cluster1" {
		t.Errorf("RoleBinding namespace = %q, want %q", rb.Namespace, "clusters-cluster1")
	}
}

func TestBuildRBACRevocation(t *testing.T) {
	owner := testOwner(t)
	objs := BuildRBACRevocation(owner, "abcdef1234567890", "clusters-cluster1")

	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}

	role, ok := objs[0].(*rbacv1.Role)
	if !ok {
		t.Fatalf("first object is not Role: %T", objs[0])
	}

	expectedName := "system-admin-credential-revocation-perm-abcdef1234567890"
	if role.Name != expectedName {
		t.Errorf("Role name = %q, want %q", role.Name, expectedName)
	}
}

func TestBuildRBACNilOwnerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("BuildRBACGiveCSRPerm with nil owner should panic")
		}
	}()
	BuildRBACGiveCSRPerm(nil, "cred")
}
