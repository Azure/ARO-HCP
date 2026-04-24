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
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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
	customCfg.ContinueOnError = true
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
	cfg.AzureCredential = nil

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

func TestAssignmentWithinResourceGroupScope_UsesScopeWhenPresent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		role *armauthorization.RoleAssignment
		want bool
	}{
		{
			name: "uses scope when present",
			role: &armauthorization.RoleAssignment{
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope: strPtr("/subscriptions/abc/resourceGroups/rg-one/providers/Microsoft.Compute/virtualMachines/vm1"),
				},
			},
			want: true,
		},
		{
			name: "falls back to ID",
			role: &armauthorization.RoleAssignment{
				ID: strPtr("/subscriptions/abc/resourceGroups/rg-one/providers/Microsoft.Authorization/roleAssignments/ra1"),
			},
			want: true,
		},
		{
			name: "rejects non-RG scope",
			role: &armauthorization.RoleAssignment{
				Properties: &armauthorization.RoleAssignmentProperties{
					Scope: strPtr("/subscriptions/abc"),
				},
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := assignmentWithinResourceGroupScope(tc.role, "/subscriptions/abc/resourcegroups/")
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

func strPtr(value string) *string { return &value }

func validDeleteOrphanedStepConfig() DeleteOrphanedStepConfig {
	return DeleteOrphanedStepConfig{
		RoleAssignmentsClient: &armauthorization.RoleAssignmentsClient{},
		AzureCredential:       roleAssignmentsTestCredential{},
		SubscriptionID:        "00000000-0000-0000-0000-000000000000",
	}
}

type roleAssignmentsTestCredential struct{}

func (roleAssignmentsTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}
