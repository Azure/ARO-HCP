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

package roleassignments

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphodataerrors "github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

func TestNewDeleteOrphanedStep_ExecutionOptions(t *testing.T) {
	t.Parallel()

	defaultStep, err := NewDeleteOrphanedStep(validDeleteOrphanedStepConfig())
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}
	if got := defaultStep.Name(); got != "Delete orphaned role assignments" {
		t.Fatalf("expected default step name %q, got %q", "Delete orphaned role assignments", got)
	}
	if got := defaultStep.RetryLimit(); got != 1 {
		t.Fatalf("expected default retry limit 1, got %d", got)
	}
	if got := defaultStep.ContinueOnError(); got {
		t.Fatalf("expected continueOnError false, got %t", got)
	}

	customCfg := validDeleteOrphanedStepConfig()
	customCfg.Name = "custom-name"
	customCfg.Retries = 3
	customCfg.ContinueOnTargetDeleteError = true
	customStep, err := NewDeleteOrphanedStep(customCfg)
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}
	if got := customStep.Name(); got != "custom-name" {
		t.Fatalf("expected step name %q, got %q", "custom-name", got)
	}
	if got := customStep.RetryLimit(); got != 3 {
		t.Fatalf("expected retry limit 3, got %d", got)
	}
	if got := customStep.ContinueOnError(); !got {
		t.Fatalf("expected continueOnError true, got %t", got)
	}
}

func TestNewDeleteOrphanedStep_ReturnsErrorWhenInvalid(t *testing.T) {
	t.Parallel()

	cfg := validDeleteOrphanedStepConfig()
	cfg.SubscriptionID = ""
	if _, err := NewDeleteOrphanedStep(cfg); err == nil {
		t.Fatalf("expected validation error for missing subscription ID")
	}
}

func TestMustNewDeleteOrphanedStep_PanicsWhenInvalid(t *testing.T) {
	t.Parallel()

	cfg := validDeleteOrphanedStepConfig()
	cfg.GraphClient = nil

	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for invalid config")
		}
	}()
	_ = MustNewDeleteOrphanedStep(cfg)
}

func TestEscapeODataString_EscapesSingleQuotes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		in   string
		want string
		fn   func(string) string
	}{
		{
			name: "escape OData string single quotes",
			in:   "O'Hara Team",
			want: "O''Hara Team",
			fn:   escapeODataString,
		},
		{
			name: "normalize ID trims and lowercases",
			in:   "  /SUBSCRIPTIONS/ABC  ",
			want: "/subscriptions/abc",
			fn:   normalizeID,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.fn(tc.in); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestAssignmentWithinSubscriptionScope(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		role *armauthorization.RoleAssignment
		want bool
	}{
		{
			name: "accepts subscription scope",
			role: &armauthorization.RoleAssignment{
				ID: strPtr("/subscriptions/abc/providers/Microsoft.Authorization/roleAssignments/ra1"),
			},
			want: true,
		},
		{
			name: "accepts resource group scope",
			role: &armauthorization.RoleAssignment{
				ID: strPtr("/subscriptions/abc/resourceGroups/rg-one/providers/Microsoft.Authorization/roleAssignments/ra1"),
			},
			want: true,
		},
		{
			name: "rejects management group scope",
			role: &armauthorization.RoleAssignment{
				ID: strPtr("/providers/Microsoft.Management/managementGroups/mg1/providers/Microsoft.Authorization/roleAssignments/ra1"),
			},
			want: false,
		},
		{
			name: "rejects different subscription with shared prefix",
			role: &armauthorization.RoleAssignment{
				ID: strPtr("/subscriptions/abc123/providers/Microsoft.Authorization/roleAssignments/ra1"),
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := assignmentWithinSubscriptionScope(tc.role, "/subscriptions/abc/")
			if got != tc.want {
				t.Fatalf("expected %t, got %t", tc.want, got)
			}
		})
	}
}

func TestToRoleAssignmentRecord_ReturnsFalseWithoutID(t *testing.T) {
	t.Parallel()

	if _, ok := toRoleAssignmentRecord(
		&armauthorization.RoleAssignment{},
		logr.Discard(),
		common.NewDiscoverySkipReporter("test"),
	); ok {
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

func TestResolveSoftDeletedPrincipalIDs(t *testing.T) {
	t.Parallel()

	lookedUp := sets.New[string]()
	got, err := resolveSoftDeletedPrincipalIDs(
		context.Background(),
		sets.New("soft-deleted", "permanently-absent"),
		func(_ context.Context, principalID string) (bool, error) {
			lookedUp.Insert(principalID)
			return principalID == "soft-deleted", nil
		},
	)
	if err != nil {
		t.Fatalf("expected lookup to succeed, got error: %v", err)
	}
	if !got.Equal(sets.New("soft-deleted")) {
		t.Fatalf("expected only soft-deleted principal to be resolved, got %v", sets.List(got))
	}
	if !lookedUp.Equal(sets.New("soft-deleted", "permanently-absent")) {
		t.Fatalf("expected each unique principal to be checked once, got %v", sets.List(lookedUp))
	}
}

func TestResolveSoftDeletedPrincipalIDs_FailsClosed(t *testing.T) {
	t.Parallel()

	lookupErr := errors.New("graph unavailable")
	got, err := resolveSoftDeletedPrincipalIDs(
		context.Background(),
		sets.New("principal"),
		func(context.Context, string) (bool, error) {
			return false, lookupErr
		},
	)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("expected lookup error to be returned, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected no resolved set after lookup failure, got %v", sets.List(got))
	}
}

func TestPrincipalRequiresRoleAssignmentRetention(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		activeResults []bool
		softDeleted   bool
		wantRetain    bool
		wantActive    int
		wantDeleted   int
	}{
		{
			name:          "active principal",
			activeResults: []bool{true},
			wantRetain:    true,
			wantActive:    1,
		},
		{
			name:          "soft-deleted principal",
			activeResults: []bool{false},
			softDeleted:   true,
			wantRetain:    true,
			wantActive:    1,
			wantDeleted:   1,
		},
		{
			name:          "principal restored during lookup",
			activeResults: []bool{false, true},
			wantRetain:    true,
			wantActive:    2,
			wantDeleted:   1,
		},
		{
			name:          "permanently absent principal",
			activeResults: []bool{false, false},
			wantRetain:    false,
			wantActive:    2,
			wantDeleted:   1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			activeCalls := 0
			deletedCalls := 0
			got, err := principalRequiresRoleAssignmentRetention(
				context.Background(),
				"principal",
				func(context.Context, string) (bool, error) {
					result := tc.activeResults[activeCalls]
					activeCalls++
					return result, nil
				},
				func(context.Context, string) (bool, error) {
					deletedCalls++
					return tc.softDeleted, nil
				},
			)
			if err != nil {
				t.Fatalf("expected lookup to succeed, got error: %v", err)
			}
			if got != tc.wantRetain {
				t.Fatalf("expected retain=%t, got %t", tc.wantRetain, got)
			}
			if activeCalls != tc.wantActive {
				t.Fatalf("expected %d active lookups, got %d", tc.wantActive, activeCalls)
			}
			if deletedCalls != tc.wantDeleted {
				t.Fatalf("expected %d deleted lookups, got %d", tc.wantDeleted, deletedCalls)
			}
		})
	}
}

func TestPrincipalRequiresRoleAssignmentRetention_FailsClosed(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		activeResults []bool
		activeErrAt   int
		deletedErr    error
	}{
		{
			name:        "initial active lookup fails",
			activeErrAt: 1,
		},
		{
			name:          "deleted lookup fails",
			activeResults: []bool{false},
			deletedErr:    errors.New("deleted lookup failed"),
		},
		{
			name:          "active recheck fails",
			activeResults: []bool{false},
			activeErrAt:   2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			activeCalls := 0
			_, err := principalRequiresRoleAssignmentRetention(
				context.Background(),
				"principal",
				func(context.Context, string) (bool, error) {
					activeCalls++
					if activeCalls == tc.activeErrAt {
						return false, errors.New("active lookup failed")
					}
					return tc.activeResults[activeCalls-1], nil
				},
				func(context.Context, string) (bool, error) {
					return false, tc.deletedErr
				},
			)
			if err == nil {
				t.Fatalf("expected lookup failure")
			}
		})
	}
}

func TestSelectOrphanedRoleAssignments(t *testing.T) {
	t.Parallel()

	assignments := []roleAssignmentRecord{
		{ID: "active-assignment", PrincipalID: "active-principal"},
		{ID: "soft-deleted-assignment", PrincipalID: "soft-deleted-principal"},
		{ID: "absent-assignment", PrincipalID: "absent-principal"},
		{ID: "ABSENT-ASSIGNMENT", PrincipalID: "absent-principal"},
		{ID: "missing-principal-assignment"},
	}
	resolvedPrincipalIDs := sets.New("active-principal", "soft-deleted-principal")

	got := selectOrphanedRoleAssignments(assignments, resolvedPrincipalIDs)
	if len(got) != 1 {
		t.Fatalf("expected one orphaned assignment, got %#v", got)
	}
	if got[0].ID != "absent-assignment" {
		t.Fatalf("expected permanently absent principal's assignment, got %q", got[0].ID)
	}
}

func TestIsGraphNotFoundError(t *testing.T) {
	t.Parallel()

	notFound := graphodataerrors.NewODataError()
	notFound.ResponseStatusCode = http.StatusNotFound
	if !isGraphNotFoundError(notFound) {
		t.Fatalf("expected Graph 404 to be recognized")
	}

	serverError := graphodataerrors.NewODataError()
	serverError.ResponseStatusCode = http.StatusInternalServerError
	if isGraphNotFoundError(serverError) {
		t.Fatalf("expected Graph 500 not to be recognized as not found")
	}
}

func strPtr(value string) *string { return &value }

func validDeleteOrphanedStepConfig() DeleteOrphanedStepConfig {
	return DeleteOrphanedStepConfig{
		RoleAssignmentsClient: &armauthorization.RoleAssignmentsClient{},
		GraphClient:           &msgraphsdk.GraphServiceClient{},
		SubscriptionID:        "00000000-0000-0000-0000-000000000000",
	}
}
