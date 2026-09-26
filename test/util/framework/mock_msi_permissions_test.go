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

package framework

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

func TestResolveLeasedMockMSIPrincipalID(t *testing.T) {
	t.Parallel()

	const (
		jobALease     = "aro-hcp-msi-mock-cs-sp-dev-0"
		jobAPrincipal = "db27175c-5bd0-48b4-929a-41de9a53ffbf"
		jobBLease     = "aro-hcp-msi-mock-cs-sp-dev-1"
		jobBPrincipal = "cd39c606-1f6a-4062-a5b9-497cd04c39fc"
	)

	catalog := msiMockPoolCatalogFile{
		MIMockPool: map[string]msiMockPoolEntry{
			jobALease: {PrincipalID: jobAPrincipal},
			jobBLease: {PrincipalID: jobBPrincipal},
		},
	}

	tests := []struct {
		name                string
		explicitPrincipalID string
		leasedSP            string
		wantPrincipal       string
		wantErr             error
	}{
		{
			name:          "lease name maps to pool principal",
			leasedSP:      jobALease,
			wantPrincipal: jobAPrincipal,
		},
		{
			name:                "explicit pooled principal without a lease is accepted",
			explicitPrincipalID: jobAPrincipal,
			wantPrincipal:       jobAPrincipal,
		},
		{
			name:                "explicit principal matching the lease is accepted",
			explicitPrincipalID: jobAPrincipal,
			leasedSP:            jobALease,
			wantPrincipal:       jobAPrincipal,
		},
		{
			name:                "job A lease plus stale job B explicit principal is refused",
			explicitPrincipalID: jobBPrincipal,
			leasedSP:            jobALease,
			wantErr:             ErrMockMSIPrincipalMismatch,
		},
		{
			name:    "missing lease and principal",
			wantErr: ErrMockMSIPrincipalUnresolved,
		},
		{
			name:     "unknown lease name",
			leasedSP: "not-a-pool-member",
			wantErr:  ErrMockMSIPrincipalUnresolved,
		},
		{
			name:                "unknown lease is refused even when an explicit pooled principal is set",
			explicitPrincipalID: jobBPrincipal,
			leasedSP:            "not-a-pool-member",
			wantErr:             ErrMockMSIPrincipalUnresolved,
		},
		{
			name:                "shared personal-dev principal is refused",
			explicitPrincipalID: sharedMockMSIPrincipalID,
			wantErr:             ErrSharedMockMSIPrincipal,
		},
		{
			name:                "non-pool principal is refused",
			explicitPrincipalID: "22222222-2222-2222-2222-222222222222",
			wantErr:             ErrMockMSIPrincipalNotPooled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveLeasedMockMSIPrincipalID(tt.explicitPrincipalID, tt.leasedSP, catalog)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantPrincipal {
				t.Fatalf("principal = %q, want %q", got, tt.wantPrincipal)
			}
		})
	}
}

func TestIsStrippableMockMSIRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		roleDefinitionID string
		roleName         string
		want             bool
	}{
		{
			name:             "key vault crypto user by guid",
			roleDefinitionID: "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/12338af0-0e69-4776-bea7-57ae8d297424",
			want:             true,
		},
		{
			name:             "suffixed custom mock role",
			roleDefinitionID: "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/abcd",
			roleName:         "dev-msi-mock-00000000-0000-0000-0000-000000000000",
			want:             true,
		},
		{
			name:             "contributor is left alone",
			roleDefinitionID: "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c",
			roleName:         "Contributor",
			want:             false,
		},
		{
			name:             "e2e custom role prefix is left alone",
			roleDefinitionID: "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/ffff",
			roleName:         E2ECustomRolePrefix + "cluster-api-azure",
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isStrippableMockMSIRole(tt.roleDefinitionID, tt.roleName); got != tt.want {
				t.Fatalf("isStrippableMockMSIRole() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMockMSIRoleAssignmentListFilter(t *testing.T) {
	t.Parallel()

	const principalID = "12eafc4e-f869-4b62-8198-816b7d6d0876"
	got := mockMSIRoleAssignmentListFilter(principalID)
	want := "assignedTo('12eafc4e-f869-4b62-8198-816b7d6d0876')"
	if got != want {
		t.Fatalf("filter = %q, want %q", got, want)
	}
	// ARM 400 UnsupportedQuery: "The filter 'principalId' is not supported"
	// when the Go SDK sends principalId eq '{id}'.
	if strings.Contains(got, "principalId") {
		t.Fatalf("filter %q must not use principalId eq; ARM returns UnsupportedQuery", got)
	}
}

func TestRoleAssignmentNameFromID(t *testing.T) {
	t.Parallel()

	got := roleAssignmentNameFromID("/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("name = %q", got)
	}
	if roleAssignmentNameFromID("") != "" {
		t.Fatal("expected empty name for empty id")
	}
}

func TestInterpretRestoreCreateDoesNotTreatGeneric409AsSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want restoreCreateResult
	}{
		{
			name: "nil create error is success",
			want: restoreCreateOK,
		},
		{
			name: "RoleAssignmentExists is success",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: roleAssignmentExistsErrorCode},
			want: restoreCreateOK,
		},
		{
			name: "HTTP 409 Conflict is a tombstone, not success",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"},
			want: restoreCreateRetryTombstone,
		},
		{
			name: "HTTP 409 with empty ErrorCode is a tombstone, not success",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict},
			want: restoreCreateRetryTombstone,
		},
		{
			name: "HTTP 500 is a hard failure",
			err:  &azcore.ResponseError{StatusCode: http.StatusInternalServerError, ErrorCode: "InternalServerError"},
			want: restoreCreateFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := interpretRestoreCreate(tt.err); got != tt.want {
				t.Fatalf("interpretRestoreCreate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRestoreOneAssignmentDoesNotTreatGeneric409AsSuccess(t *testing.T) {
	t.Parallel()

	snapshot := MockMSIRoleAssignmentSnapshot{AssignmentID: "/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/aaaa"}
	createErr := &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"}
	getErr := &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "RoleAssignmentNotFound"}

	err := restoreOneAssignmentWithWait(context.Background(), snapshot,
		func() error { return createErr },
		func() error { return getErr },
		func(time.Duration) {},
	)
	if err == nil {
		t.Fatal("generic HTTP 409 Conflict with GET 404 must not be treated as restore success")
	}
}

func TestRestoreOneAssignmentRoleAssignmentExistsIsSuccess(t *testing.T) {
	t.Parallel()

	snapshot := MockMSIRoleAssignmentSnapshot{AssignmentID: "/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/aaaa"}
	gets := 0
	err := restoreOneAssignmentWithWait(context.Background(), snapshot,
		func() error {
			return &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: roleAssignmentExistsErrorCode}
		},
		func() error {
			gets++
			t.Fatal("GET must not run when Create returns RoleAssignmentExists")
			return nil
		},
		func(time.Duration) {},
	)
	if err != nil {
		t.Fatalf("RoleAssignmentExists must be restore success, got %v", err)
	}
	if gets != 0 {
		t.Fatalf("GET ran %d times", gets)
	}
}

func TestRestoreOneAssignmentRetriesWhenGETIs404ThenCreateSucceeds(t *testing.T) {
	t.Parallel()

	snapshot := MockMSIRoleAssignmentSnapshot{AssignmentID: "/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/aaaa"}
	creates := 0
	err := restoreOneAssignmentWithWait(context.Background(), snapshot,
		func() error {
			creates++
			if creates == 1 {
				return &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"}
			}
			return nil
		},
		func() error {
			return &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "RoleAssignmentNotFound"}
		},
		func(time.Duration) {},
	)
	if err != nil {
		t.Fatalf("tombstone 409 then successful Create must restore, got %v", err)
	}
	if creates != 2 {
		t.Fatalf("creates = %d, want 2", creates)
	}
}

func TestErrIfNoStrippableMockMSIAssignments(t *testing.T) {
	t.Parallel()

	if err := errIfNoStrippableMockMSIAssignments(nil, "principal", "/subscriptions/sub"); err == nil {
		t.Fatal("empty snapshot list must fail closed")
	}
	snapshots := []MockMSIRoleAssignmentSnapshot{{AssignmentID: "id"}}
	if err := errIfNoStrippableMockMSIAssignments(snapshots, "principal", "/subscriptions/sub"); err != nil {
		t.Fatalf("non-empty list must succeed, got %v", err)
	}
}

func TestSnapshotIfStrippableMockMSIAssignment(t *testing.T) {
	t.Parallel()

	const (
		subscriptionScope = "/subscriptions/sub"
		principalID       = "db27175c-5bd0-48b4-929a-41de9a53ffbf"
		otherPrincipal    = "cd39c606-1f6a-4062-a5b9-497cd04c39fc"
		kvRoleID          = "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/12338af0-0e69-4776-bea7-57ae8d297424"
		customRoleID      = "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/abcd"
		contributorRoleID = "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
		assignmentID      = "/subscriptions/sub/providers/Microsoft.Authorization/roleAssignments/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	)
	roleNamesByID := map[string]string{
		strings.ToLower(customRoleID):      "dev-msi-mock-00000000-0000-0000-0000-000000000000",
		strings.ToLower(contributorRoleID): "Contributor",
	}

	tests := []struct {
		name    string
		ra      *armauthorization.RoleAssignment
		wantOK  bool
		wantErr bool
		wantPID string
	}{
		{
			name: "key vault crypto user at subscription scope",
			ra: &armauthorization.RoleAssignment{
				ID: to.Ptr(assignmentID),
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope:            to.Ptr(subscriptionScope),
					PrincipalID:      to.Ptr(principalID),
					RoleDefinitionID: to.Ptr(kvRoleID),
				},
			},
			wantOK:  true,
			wantPID: principalID,
		},
		{
			name: "custom mock role at subscription scope",
			ra: &armauthorization.RoleAssignment{
				ID: to.Ptr(assignmentID),
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope:            to.Ptr(subscriptionScope),
					PrincipalID:      to.Ptr(principalID),
					RoleDefinitionID: to.Ptr(customRoleID),
				},
			},
			wantOK:  true,
			wantPID: principalID,
		},
		{
			name: "contributor is skipped",
			ra: &armauthorization.RoleAssignment{
				ID: to.Ptr(assignmentID),
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope:            to.Ptr(subscriptionScope),
					PrincipalID:      to.Ptr(principalID),
					RoleDefinitionID: to.Ptr(contributorRoleID),
				},
			},
		},
		{
			name: "resource-group scope is skipped",
			ra: &armauthorization.RoleAssignment{
				ID: to.Ptr(assignmentID),
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope:            to.Ptr(subscriptionScope + "/resourceGroups/rg"),
					PrincipalID:      to.Ptr(principalID),
					RoleDefinitionID: to.Ptr(kvRoleID),
				},
			},
		},
		{
			name: "different principal is skipped",
			ra: &armauthorization.RoleAssignment{
				ID: to.Ptr(assignmentID),
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope:            to.Ptr(subscriptionScope),
					PrincipalID:      to.Ptr(otherPrincipal),
					RoleDefinitionID: to.Ptr(kvRoleID),
				},
			},
		},
		{
			name: "missing assignment name is an error",
			ra: &armauthorization.RoleAssignment{
				ID: to.Ptr("not-an-id"),
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope:            to.Ptr(subscriptionScope),
					PrincipalID:      to.Ptr(principalID),
					RoleDefinitionID: to.Ptr(kvRoleID),
				},
			},
			wantErr: true,
		},
		{
			name: "nil assignment is skipped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := snapshotIfStrippableMockMSIAssignment(tt.ra, subscriptionScope, principalID, roleNamesByID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.PrincipalID != tt.wantPID {
				t.Fatalf("principalID = %q, want %q", got.PrincipalID, tt.wantPID)
			}
			if got.AssignmentID != assignmentID {
				t.Fatalf("assignmentID = %q", got.AssignmentID)
			}
		})
	}
}
