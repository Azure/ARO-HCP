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
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	kiotaauth "github.com/microsoft/kiota-authentication-azure-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphdirectoryobjects "github.com/microsoftgraph/msgraph-sdk-go/directoryobjects"
	graphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

const (
	ResourceType              = "Microsoft.Authorization/roleAssignments"
	unknownObjectTypeValue    = "Unknown"
	graphScope                = "https://graph.microsoft.com/.default"
	graphGetByIDsBatchSize    = 1000
	preflightGroupDisplayName = "aro-hcp-engineering-App Developer"
	preflightFailureMessage   = "Refusing to run cleanup: directory visibility is insufficient. This tool must be run with directory read permissions (e.g. Directory.Read.All)."
)

type DeleteOrphanedStep struct {
	runner.DeletionStep
}

type DeleteOrphanedStepConfig struct {
	RoleAssignmentsClient *armauthorization.RoleAssignmentsClient
	AzureCredential       azcore.TokenCredential
	SubscriptionID        string

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

var _ runner.StepOptionsProvider = DeleteOrphanedStepConfig{}

func (c DeleteOrphanedStepConfig) StepOptions() runner.StepOptions {
	return runner.StepOptions{
		Name:            c.Name,
		Retries:         c.Retries,
		ContinueOnError: c.ContinueOnError,
		Verify:          c.Verify,
	}
}

func NewDeleteOrphanedStep(cfg DeleteOrphanedStepConfig) *DeleteOrphanedStep {
	stepOptions := cfg.StepOptions()
	if stepOptions.Name == "" {
		stepOptions.Name = "Delete orphaned role assignments"
	}

	step := &DeleteOrphanedStep{
		DeletionStep: runner.DeletionStep{
			ResourceType: ResourceType,
			Options:      stepOptions,
		},
	}

	step.DiscoverFn = func(ctx context.Context, _ string) ([]runner.Target, error) {
		return discoverOrphanedRoleAssignments(
			ctx,
			cfg.RoleAssignmentsClient,
			cfg.AzureCredential,
			cfg.SubscriptionID,
		)
	}
	step.DeleteFn = func(ctx context.Context, target runner.Target, _ bool) error {
		_, err := cfg.RoleAssignmentsClient.DeleteByID(ctx, target.ID, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				return nil
			}
			return fmt.Errorf("failed to delete role assignment %q: %w", target.ID, err)
		}
		return nil
	}
	step.VerifyFn = func(ctx context.Context) error {
		if stepOptions.Verify == nil {
			return nil
		}
		return stepOptions.Verify(ctx)
	}

	return step
}

// SAFETY CONTRACT:
// This tool assumes that "principal not returned by Graph" means "safe to delete".
// To prevent accidental deletion when run with insufficient permissions,
// an explicit Graph preflight check is enforced and cannot be bypassed.
func discoverOrphanedRoleAssignments(
	ctx context.Context,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	azureCredential azcore.TokenCredential,
	subscriptionID string,
) ([]runner.Target, error) {
	logger := runner.LoggerFromContext(ctx)

	if roleAssignmentsClient == nil {
		return nil, fmt.Errorf("role assignments client is required")
	}
	if azureCredential == nil {
		return nil, fmt.Errorf("azure credential is required")
	}
	subscriptionID = strings.TrimSpace(subscriptionID)
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}

	graphClient, err := newGraphClient(azureCredential)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", preflightFailureMessage, err)
	}

	// Mandatory and unconditional preflight. If this fails, we fail closed
	// before listing role assignments, in both dry-run and delete modes.
	if err := runGraphVisibilityPreflight(ctx, graphClient); err != nil {
		return nil, fmt.Errorf("%s: %w", preflightFailureMessage, err)
	}

	// 1) List all role assignments at resource-group scope (ARM).
	assignments, err := listRoleAssignments(ctx, roleAssignmentsClient, subscriptionID)
	if err != nil {
		return nil, err
	}

	// 2-4) Resolve all unique principal IDs via Graph directoryObjects/getByIds.
	resolvedPrincipalIDs, err := resolvePrincipalIDsWithGraphGetByIDs(ctx, graphClient, assignments)
	if err != nil {
		return nil, fmt.Errorf("failed resolving role assignment principals with Microsoft Graph getByIds: %w", err)
	}

	// 5) Keep assignment iff principalId is not in resolved set.
	candidateIDs := sets.New[string]()
	candidates := make([]roleAssignmentRecord, 0, len(assignments))
	for _, assignment := range assignments {
		if _, resolved := resolvedPrincipalIDs[normalizeID(assignment.PrincipalID)]; resolved {
			continue
		}
		if candidateIDs.Has(assignment.ID) {
			continue
		}
		candidateIDs.Insert(assignment.ID)
		candidates = append(candidates, assignment)
	}

	targets := make([]runner.Target, 0, len(candidates))
	for _, candidate := range candidates {
		targets = append(targets, candidate.ToTarget())
	}

	if len(targets) == 0 {
		logger.Info(
			"No orphaned role assignments discovered",
			"resourceType", ResourceType,
			"objectType", unknownObjectTypeValue,
			"strategy", "graph-getByIds",
			"assignmentsScanned", len(assignments),
		)
		return targets, nil
	}

	logger.Info(
		"Discovered orphaned role assignments",
		"count", len(targets),
		"resourceType", ResourceType,
		"objectType", unknownObjectTypeValue,
		"strategy", "graph-getByIds",
		"assignmentsScanned", len(assignments),
	)

	return targets, nil
}

func newGraphClient(azureCredential azcore.TokenCredential) (*msgraphsdk.GraphServiceClient, error) {
	authProvider, err := kiotaauth.NewAzureIdentityAuthenticationProviderWithScopes(
		azureCredential,
		[]string{graphScope},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Graph authentication provider: %w", err)
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(authProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Graph request adapter: %w", err)
	}

	return msgraphsdk.NewGraphServiceClient(adapter), nil
}

func runGraphVisibilityPreflight(ctx context.Context, graphClient *msgraphsdk.GraphServiceClient) error {
	filter := fmt.Sprintf("displayName eq '%s'", escapeODataString(preflightGroupDisplayName))
	selectFields := []string{"id"}
	top := int32(1)

	response, err := graphClient.Groups().Get(ctx, &graphgroups.GroupsRequestBuilderGetRequestConfiguration{
		QueryParameters: &graphgroups.GroupsRequestBuilderGetQueryParameters{
			Filter: &filter,
			Select: selectFields,
			Top:    &top,
		},
	})
	if err != nil {
		return err
	}
	if response == nil || len(response.GetValue()) == 0 {
		return fmt.Errorf("known principal %q was not returned by Graph", preflightGroupDisplayName)
	}
	first := response.GetValue()[0]
	if first == nil || first.GetId() == nil || strings.TrimSpace(*first.GetId()) == "" {
		return fmt.Errorf("known principal %q was returned without a valid ID", preflightGroupDisplayName)
	}

	return nil
}

type roleAssignmentRecord struct {
	ID          string
	Name        string
	Type        string
	PrincipalID string
}

func listRoleAssignments(
	ctx context.Context,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	subscriptionID string,
) ([]roleAssignmentRecord, error) {
	pager := roleAssignmentsClient.NewListForSubscriptionPager(nil)
	assignments := make([]roleAssignmentRecord, 0)
	resourceGroupScopePrefix := "/subscriptions/" + normalizeID(subscriptionID) + "/resourcegroups/"

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing role assignments: %w", err)
		}
		for _, roleAssignment := range page.Value {
			if !assignmentWithinResourceGroupScope(roleAssignment, resourceGroupScopePrefix) {
				continue
			}
			record, ok := toRoleAssignmentRecord(roleAssignment)
			if !ok {
				continue
			}
			assignments = append(assignments, record)
		}
	}

	return assignments, nil
}

func toRoleAssignmentRecord(roleAssignment *armauthorization.RoleAssignment) (roleAssignmentRecord, bool) {
	id, ok := roleAssignmentID(roleAssignment)
	if !ok {
		return roleAssignmentRecord{}, false
	}

	record := roleAssignmentRecord{
		ID:   id,
		Name: roleAssignmentName(roleAssignment, id),
		Type: roleAssignmentType(roleAssignment),
	}

	if roleAssignment != nil && roleAssignment.Properties != nil && roleAssignment.Properties.PrincipalID != nil {
		record.PrincipalID = strings.TrimSpace(*roleAssignment.Properties.PrincipalID)
	}

	return record, true
}

func (r roleAssignmentRecord) ToTarget() runner.Target {
	return runner.Target{
		ID:   r.ID,
		Name: r.Name,
		Type: r.Type,
	}
}

func resolvePrincipalIDsWithGraphGetByIDs(
	ctx context.Context,
	graphClient *msgraphsdk.GraphServiceClient,
	assignments []roleAssignmentRecord,
) (sets.Set[string], error) {
	uniquePrincipalIDs := sets.New[string]()
	for _, assignment := range assignments {
		normalizedPrincipalID := normalizeID(assignment.PrincipalID)
		if normalizedPrincipalID == "" {
			continue
		}
		uniquePrincipalIDs.Insert(normalizedPrincipalID)
	}
	if uniquePrincipalIDs.Len() == 0 {
		return sets.New[string](), nil
	}

	resolvedPrincipalIDs := sets.New[string]()
	ids := sets.List(uniquePrincipalIDs)
	for start := 0; start < len(ids); start += graphGetByIDsBatchSize {
		end := min(start+graphGetByIDsBatchSize, len(ids))
		body := graphdirectoryobjects.NewGetByIdsPostRequestBody()
		body.SetIds(ids[start:end])

		response, err := graphClient.DirectoryObjects().GetByIds().PostAsGetByIdsPostResponse(ctx, body, nil)
		if err != nil {
			return nil, err
		}
		if response == nil {
			continue
		}
		for _, object := range response.GetValue() {
			if object == nil || object.GetId() == nil {
				continue
			}
			normalizedID := normalizeID(*object.GetId())
			if normalizedID == "" {
				continue
			}
			resolvedPrincipalIDs.Insert(normalizedID)
		}
	}

	return resolvedPrincipalIDs, nil
}

func escapeODataString(raw string) string {
	return strings.ReplaceAll(strings.TrimSpace(raw), "'", "''")
}

func normalizeID(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func roleAssignmentID(roleAssignment *armauthorization.RoleAssignment) (string, bool) {
	if roleAssignment == nil || roleAssignment.ID == nil {
		return "", false
	}
	id := strings.TrimSpace(*roleAssignment.ID)
	return id, id != ""
}

func roleAssignmentName(roleAssignment *armauthorization.RoleAssignment, fallback string) string {
	if roleAssignment != nil && roleAssignment.Name != nil {
		name := strings.TrimSpace(*roleAssignment.Name)
		if name != "" {
			return name
		}
	}
	return fallback
}

func roleAssignmentType(roleAssignment *armauthorization.RoleAssignment) string {
	if roleAssignment != nil && roleAssignment.Type != nil {
		resourceType := strings.TrimSpace(*roleAssignment.Type)
		if resourceType != "" {
			return resourceType
		}
	}
	return ResourceType
}

func assignmentWithinResourceGroupScope(
	roleAssignment *armauthorization.RoleAssignment,
	resourceGroupScopePrefix string,
) bool {
	scope := roleAssignmentScope(roleAssignment)
	if scope != "" {
		return strings.HasPrefix(normalizeID(scope), resourceGroupScopePrefix)
	}

	id, ok := roleAssignmentID(roleAssignment)
	if !ok {
		return false
	}
	return strings.HasPrefix(normalizeID(id), resourceGroupScopePrefix)
}

func roleAssignmentScope(roleAssignment *armauthorization.RoleAssignment) string {
	if roleAssignment != nil && roleAssignment.Properties != nil && roleAssignment.Properties.Scope != nil {
		return strings.TrimSpace(*roleAssignment.Properties.Scope)
	}
	return ""
}
