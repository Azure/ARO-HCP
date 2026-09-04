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

package directoryobjects

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/go-logr/logr"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphodataerrors "github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

func validPurgeAgedDeletedStepConfig() PurgeAgedDeletedStepConfig {
	return PurgeAgedDeletedStepConfig{
		RoleAssignmentsClient: &armauthorization.RoleAssignmentsClient{},
		GraphClient:           &msgraphsdk.GraphServiceClient{},
		SubscriptionID:        "00000000-0000-0000-0000-000000000000",
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

func TestNewPurgeAgedDeletedStep_ExecutionOptions(t *testing.T) {
	t.Parallel()

	defaultStep, err := NewPurgeAgedDeletedStep(validPurgeAgedDeletedStepConfig())
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}
	if got := defaultStep.Name(); got != "Purge aged deleted directory objects" {
		t.Fatalf("expected default step name %q, got %q", "Purge aged deleted directory objects", got)
	}
	if got := defaultStep.RetryLimit(); got != 1 {
		t.Fatalf("expected default retry limit 1, got %d", got)
	}
	if got := defaultStep.ContinueOnError(); got {
		t.Fatalf("expected continueOnError false, got %t", got)
	}

	customCfg := validPurgeAgedDeletedStepConfig()
	customCfg.Name = "custom-name"
	customCfg.Retries = 3
	customCfg.ContinueOnError = true
	customStep, err := NewPurgeAgedDeletedStep(customCfg)
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

func TestNewPurgeAgedDeletedStep_ReturnsErrorWhenInvalid(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		mutate func(*PurgeAgedDeletedStepConfig)
	}{
		{
			name: "missing role assignments client",
			mutate: func(cfg *PurgeAgedDeletedStepConfig) {
				cfg.RoleAssignmentsClient = nil
			},
		},
		{
			name: "missing graph client",
			mutate: func(cfg *PurgeAgedDeletedStepConfig) {
				cfg.GraphClient = nil
			},
		},
		{
			name: "missing subscription ID",
			mutate: func(cfg *PurgeAgedDeletedStepConfig) {
				cfg.SubscriptionID = ""
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validPurgeAgedDeletedStepConfig()
			tc.mutate(&cfg)
			if _, err := NewPurgeAgedDeletedStep(cfg); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestMustNewPurgeAgedDeletedStep_PanicsWhenInvalid(t *testing.T) {
	t.Parallel()

	cfg := validPurgeAgedDeletedStepConfig()
	cfg.GraphClient = nil

	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for invalid config")
		}
	}()
	_ = MustNewPurgeAgedDeletedStep(cfg)
}

func TestNewPurgeAgedDeletedStep_DefaultsMinAge(t *testing.T) {
	t.Parallel()

	cfg := validPurgeAgedDeletedStepConfig()
	step, err := NewPurgeAgedDeletedStep(cfg)
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}
	concrete, ok := step.(*purgeAgedDeletedStep)
	if !ok {
		t.Fatalf("expected *purgeAgedDeletedStep, got %T", step)
	}
	if concrete.minAge != DefaultMinAge {
		t.Fatalf("expected default min age %v, got %v", DefaultMinAge, concrete.minAge)
	}

	cfg.MinAge = 3 * 24 * time.Hour
	step, err = NewPurgeAgedDeletedStep(cfg)
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}
	concrete, ok = step.(*purgeAgedDeletedStep)
	if !ok {
		t.Fatalf("expected *purgeAgedDeletedStep, got %T", step)
	}
	if concrete.minAge != cfg.MinAge {
		t.Fatalf("expected overridden min age %v, got %v", cfg.MinAge, concrete.minAge)
	}
}

func TestDeletedObjectRecord_ToTarget(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		record deletedObjectRecord
		want   string
	}{
		{
			name:   "uses display name when present",
			record: deletedObjectRecord{ID: "id-1", DisplayName: "aro-hcp-e2e-sp", ResourceType: ServicePrincipalResourceType},
			want:   "aro-hcp-e2e-sp",
		},
		{
			name:   "falls back to ID when display name is empty",
			record: deletedObjectRecord{ID: "id-2", ResourceType: ApplicationResourceType},
			want:   "id-2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := tc.record.ToTarget()
			if target.Name != tc.want {
				t.Fatalf("expected name %q, got %q", tc.want, target.Name)
			}
			if target.ID != tc.record.ID {
				t.Fatalf("expected ID %q, got %q", tc.record.ID, target.ID)
			}
			if target.Type != tc.record.ResourceType {
				t.Fatalf("expected type %q, got %q", tc.record.ResourceType, target.Type)
			}
		})
	}
}

func TestNormalizeID(t *testing.T) {
	t.Parallel()

	if got := normalizeID("  /SUBSCRIPTIONS/ABC  "); got != "/subscriptions/abc" {
		t.Fatalf("expected normalized ID, got %q", got)
	}
}

func TestAssignmentWithinSubscriptionScope(t *testing.T) {
	t.Parallel()

	strPtr := func(s string) *string { return &s }

	testCases := []struct {
		name string
		role *armauthorization.RoleAssignment
		want bool
	}{
		{
			name: "accepts nested scope within subscription",
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

func TestPurgeAgedDeletedStepDiscover_SkipsWhenRoleAssignmentListingFails(t *testing.T) {
	t.Parallel()

	step, err := NewPurgeAgedDeletedStep(validPurgeAgedDeletedStepConfig())
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}

	concrete := step.(*purgeAgedDeletedStep)
	concrete.listRoleAssignmentPrincipalIDs = func(context.Context, *armauthorization.RoleAssignmentsClient, string, logr.Logger, *common.DiscoverySkipReporter) (sets.Set[string], error) {
		return nil, errors.New("arm unavailable")
	}

	ctx := logr.NewContext(context.Background(), logr.Discard())
	targets, err := concrete.Discover(ctx)
	if err != nil {
		t.Fatalf("expected discover to fail open, got error: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected no targets when listing role assignments fails, got %d", len(targets))
	}
}

func TestPurgeAgedDeletedStepDiscover_SkipsWhenActivePrincipalResolutionFails(t *testing.T) {
	t.Parallel()

	step, err := NewPurgeAgedDeletedStep(validPurgeAgedDeletedStepConfig())
	if err != nil {
		t.Fatalf("expected constructor to succeed, got error: %v", err)
	}

	concrete := step.(*purgeAgedDeletedStep)
	concrete.listRoleAssignmentPrincipalIDs = func(context.Context, *armauthorization.RoleAssignmentsClient, string, logr.Logger, *common.DiscoverySkipReporter) (sets.Set[string], error) {
		return sets.New("/principals/a"), nil
	}
	concrete.resolveActivePrincipalIDs = func(context.Context, *msgraphsdk.GraphServiceClient, sets.Set[string]) (sets.Set[string], error) {
		return nil, errors.New("graph unavailable")
	}

	ctx := logr.NewContext(context.Background(), logr.Discard())
	targets, err := concrete.Discover(ctx)
	if err != nil {
		t.Fatalf("expected discover to fail open, got error: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected no targets when resolving active principals fails, got %d", len(targets))
	}
}
