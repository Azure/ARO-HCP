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

	"github.com/go-logr/logr"
	kiotaauth "github.com/microsoft/kiota-authentication-azure-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphdirectoryobjects "github.com/microsoftgraph/msgraph-sdk-go/directoryobjects"
	graphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"
	graphodataerrors "github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

const (
	// ResourceType is the ARM resource type for role assignments.
	ResourceType              = "Microsoft.Authorization/roleAssignments"
	unknownObjectTypeValue    = "Unknown"
	graphScope                = "https://graph.microsoft.com/.default"
	graphGetByIDsBatchSize    = 1000
	preflightGroupDisplayName = "aro-hcp-engineering-App Developer"
	preflightFailureMessage   = "Refusing to run cleanup: directory visibility is insufficient. This tool must be run with directory read permissions (e.g. Directory.Read.All)."
)

// DeleteOrphanedStepConfig configures orphaned role-assignment cleanup.
type DeleteOrphanedStepConfig struct {
	RoleAssignmentsClient *armauthorization.RoleAssignmentsClient
	// GraphClient performs the Microsoft Graph directory reads (the
	// orphaned-principal preflight and getByIds resolution). It is built from a
	// credential that may differ from the one backing RoleAssignmentsClient, so
	// that directory read permissions (Directory.Read.All) can be supplied by a
	// separate identity. Build it with NewGraphClient.
	GraphClient    *msgraphsdk.GraphServiceClient
	SubscriptionID string

	Name                        string
	Retries                     int
	ContinueOnTargetDeleteError bool
	Verify                      runner.VerifyFn
}

type deleteOrphanedStep struct {
	cfg             DeleteOrphanedStepConfig
	name            string
	retries         int
	continueOnError bool
	verify          runner.VerifyFn
}

var _ runner.Step = (*deleteOrphanedStep)(nil)

// NewDeleteOrphanedStep builds the orphaned role-assignment deletion step.
func NewDeleteOrphanedStep(cfg DeleteOrphanedStepConfig) (runner.Step, error) {
	if cfg.RoleAssignmentsClient == nil {
		return nil, fmt.Errorf("role assignments client is required")
	}
	if cfg.GraphClient == nil {
		return nil, fmt.Errorf("graph client is required")
	}
	if strings.TrimSpace(cfg.SubscriptionID) == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}

	stepName := cfg.Name
	if strings.TrimSpace(stepName) == "" {
		stepName = "Delete orphaned role assignments"
	}

	return &deleteOrphanedStep{
		cfg:             cfg,
		name:            stepName,
		retries:         cfg.Retries,
		continueOnError: cfg.ContinueOnTargetDeleteError,
		verify:          cfg.Verify,
	}, nil
}

// MustNewDeleteOrphanedStep builds the step and panics on invalid config.
func MustNewDeleteOrphanedStep(cfg DeleteOrphanedStepConfig) runner.Step {
	step, err := NewDeleteOrphanedStep(cfg)
	if err != nil {
		panic(err)
	}
	return step
}

func (s *deleteOrphanedStep) Name() string {
	return s.name
}

func (s *deleteOrphanedStep) RetryLimit() int {
	if s.retries < runner.DefaultRetries {
		return runner.DefaultRetries
	}
	return s.retries
}

func (s *deleteOrphanedStep) ContinueOnError() bool {
	return s.continueOnError
}

func (s *deleteOrphanedStep) Verify(ctx context.Context) error {
	if s.verify == nil {
		return nil
	}
	return s.verify(ctx)
}

func (s *deleteOrphanedStep) Discover(ctx context.Context) ([]runner.Target, error) {
	return discoverOrphanedRoleAssignments(
		ctx,
		s.cfg.RoleAssignmentsClient,
		s.cfg.GraphClient,
		s.cfg.SubscriptionID,
	)
}

func (s *deleteOrphanedStep) Delete(ctx context.Context, target runner.Target, _ bool) error {
	response, err := s.cfg.RoleAssignmentsClient.GetByID(ctx, target.ID, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("failed to re-read role assignment %q: %w", target.ID, err)
	}
	if response.Properties == nil ||
		response.Properties.PrincipalID == nil ||
		normalizeID(*response.Properties.PrincipalID) == "" {
		return fmt.Errorf("refusing to delete role assignment %q without a principal ID", target.ID)
	}

	principalID := normalizeID(*response.Properties.PrincipalID)
	retain, err := principalRequiresRoleAssignmentRetention(
		ctx,
		principalID,
		newGraphActivePrincipalLookup(s.cfg.GraphClient),
		newGraphDeletedPrincipalLookup(s.cfg.GraphClient),
	)
	if err != nil {
		return fmt.Errorf("failed revalidating principal %q for role assignment %q: %w", principalID, target.ID, err)
	}
	if retain {
		return fmt.Errorf("%w: principal %q exists in the active or deleted directory", runner.ErrTargetRetained, principalID)
	}

	_, err = s.cfg.RoleAssignmentsClient.DeleteByID(ctx, target.ID, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete role assignment %q: %w", target.ID, err)
	}
	return nil
}

// SAFETY CONTRACT:
// A role assignment is deletable only when its principal is absent from both
// the active directory and deletedItems. The active directory is checked again
// after deletedItems to avoid deleting assignments while a principal is being
// restored. An explicit Graph visibility preflight is also enforced and cannot
// be bypassed.
func discoverOrphanedRoleAssignments(
	ctx context.Context,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	graphClient *msgraphsdk.GraphServiceClient,
	subscriptionID string,
) ([]runner.Target, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	skipReporter := common.NewDiscoverySkipReporter("Delete orphaned role assignments")
	defer skipReporter.Flush(logger)

	if roleAssignmentsClient == nil {
		return nil, fmt.Errorf("role assignments client is required")
	}
	if graphClient == nil {
		return nil, fmt.Errorf("graph client is required")
	}
	subscriptionID = strings.TrimSpace(subscriptionID)
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}

	// Mandatory and unconditional preflight. If this fails, we fail closed
	// before listing role assignments, in both dry-run and delete modes.
	if err := runGraphVisibilityPreflight(ctx, graphClient); err != nil {
		return nil, fmt.Errorf("%s: %w", preflightFailureMessage, err)
	}

	// 1) List all role assignments within the subscription scope (ARM).
	assignments, err := listRoleAssignments(ctx, roleAssignmentsClient, subscriptionID, logger, skipReporter)
	if err != nil {
		return nil, err
	}

	// 2) Collect all unique principal IDs from the ARM assignments.
	principalIDs := collectPrincipalIDs(assignments, logger, skipReporter)

	// 3) Resolve active principals via Graph directoryObjects/getByIds.
	resolvedPrincipalIDs, err := resolvePrincipalIDsWithGraphGetByIDs(ctx, graphClient, principalIDs, logger, skipReporter)
	if err != nil {
		return nil, fmt.Errorf("failed resolving role assignment principals with Microsoft Graph getByIds: %w", err)
	}

	// 4) Protect principals that are still recoverable from Graph deletedItems.
	unresolvedPrincipalIDs := principalIDs.Difference(resolvedPrincipalIDs)
	softDeletedPrincipalIDs, err := resolveSoftDeletedPrincipalIDs(
		ctx,
		unresolvedPrincipalIDs,
		newGraphDeletedPrincipalLookup(graphClient),
	)
	if err != nil {
		return nil, fmt.Errorf("failed resolving soft-deleted role assignment principals with Microsoft Graph deletedItems: %w", err)
	}
	resolvedPrincipalIDs.Insert(sets.List(softDeletedPrincipalIDs)...)

	// 5) Recheck the active directory in case a principal was restored between
	// the initial active lookup and the deletedItems lookup.
	stillUnresolvedPrincipalIDs := principalIDs.Difference(resolvedPrincipalIDs)
	restoredPrincipalIDs, err := resolvePrincipalIDsWithGraphGetByIDs(
		ctx,
		graphClient,
		stillUnresolvedPrincipalIDs,
		logger,
		skipReporter,
	)
	if err != nil {
		return nil, fmt.Errorf("failed rechecking unresolved role assignment principals with Microsoft Graph getByIds: %w", err)
	}
	resolvedPrincipalIDs.Insert(sets.List(restoredPrincipalIDs)...)

	// 6) Keep an assignment only when its principal was absent from every
	// directory lookup. Missing principal IDs are retained rather than guessed.
	candidates := selectOrphanedRoleAssignments(assignments, resolvedPrincipalIDs)
	targets := make([]runner.Target, 0, len(candidates))
	for _, candidate := range candidates {
		targets = append(targets, candidate.ToTarget())
	}

	if len(targets) == 0 {
		logger.Info(
			"No orphaned role assignments discovered",
			"resourceType", ResourceType,
			"objectType", unknownObjectTypeValue,
			"strategy", "graph-getByIds-deletedItems-getById",
			"assignmentsScanned", len(assignments),
		)
		return targets, nil
	}

	logger.Info(
		"Discovered orphaned role assignments",
		"count", len(targets),
		"resourceType", ResourceType,
		"objectType", unknownObjectTypeValue,
		"strategy", "graph-getByIds-deletedItems-getById",
		"assignmentsScanned", len(assignments),
	)

	return targets, nil
}

// NewGraphClient builds a Microsoft Graph client from the given credential. It
// is used by the workflow to construct the Graph client once and pass it to the
// orphaned role-assignment step, keeping client construction alongside the ARM
// clients rather than inside the step.
func NewGraphClient(azureCredential azcore.TokenCredential) (*msgraphsdk.GraphServiceClient, error) {
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
	logger logr.Logger,
	skipReporter *common.DiscoverySkipReporter,
) ([]roleAssignmentRecord, error) {
	pager := roleAssignmentsClient.NewListForSubscriptionPager(nil)
	assignments := make([]roleAssignmentRecord, 0)
	subscriptionScopePrefix := "/subscriptions/" + normalizeID(subscriptionID) + "/"

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing role assignments: %w", err)
		}
		for _, roleAssignment := range page.Value {
			if !assignmentWithinSubscriptionScope(roleAssignment, subscriptionScopePrefix) {
				continue
			}
			record, ok := toRoleAssignmentRecord(roleAssignment, logger, skipReporter)
			if !ok {
				continue
			}
			assignments = append(assignments, record)
		}
	}

	return assignments, nil
}

func toRoleAssignmentRecord(
	roleAssignment *armauthorization.RoleAssignment,
	logger logr.Logger,
	skipReporter *common.DiscoverySkipReporter,
) (roleAssignmentRecord, bool) {
	id, ok := roleAssignmentID(roleAssignment)
	if !ok {
		skipReporter.Record(
			logger,
			"invalid_role_assignment_payload",
		)
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

func collectPrincipalIDs(
	assignments []roleAssignmentRecord,
	logger logr.Logger,
	skipReporter *common.DiscoverySkipReporter,
) sets.Set[string] {
	uniquePrincipalIDs := sets.New[string]()
	for _, assignment := range assignments {
		normalizedPrincipalID := normalizeID(assignment.PrincipalID)
		if normalizedPrincipalID == "" {
			skipReporter.Record(
				logger,
				"missing_principal_id",
				"assignmentID", assignment.ID,
			)
			continue
		}
		uniquePrincipalIDs.Insert(normalizedPrincipalID)
	}
	return uniquePrincipalIDs
}

func resolvePrincipalIDsWithGraphGetByIDs(
	ctx context.Context,
	graphClient *msgraphsdk.GraphServiceClient,
	principalIDs sets.Set[string],
	logger logr.Logger,
	skipReporter *common.DiscoverySkipReporter,
) (sets.Set[string], error) {
	if principalIDs.Len() == 0 {
		return sets.New[string](), nil
	}

	resolvedPrincipalIDs := sets.New[string]()
	ids := sets.List(principalIDs)
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
				skipReporter.Record(
					logger,
					"invalid_graph_object_payload",
				)
				continue
			}
			normalizedID := normalizeID(*object.GetId())
			if normalizedID == "" {
				skipReporter.Record(
					logger,
					"empty_graph_object_id",
				)
				continue
			}
			resolvedPrincipalIDs.Insert(normalizedID)
		}
	}

	return resolvedPrincipalIDs, nil
}

type deletedPrincipalLookup func(context.Context, string) (bool, error)

type activePrincipalLookup func(context.Context, string) (bool, error)

func newGraphActivePrincipalLookup(graphClient *msgraphsdk.GraphServiceClient) activePrincipalLookup {
	return func(ctx context.Context, principalID string) (bool, error) {
		body := graphdirectoryobjects.NewGetByIdsPostRequestBody()
		body.SetIds([]string{principalID})

		response, err := graphClient.DirectoryObjects().GetByIds().PostAsGetByIdsPostResponse(ctx, body, nil)
		if err != nil {
			return false, err
		}
		if response == nil || len(response.GetValue()) == 0 {
			return false, nil
		}
		if len(response.GetValue()) != 1 ||
			response.GetValue()[0] == nil ||
			response.GetValue()[0].GetId() == nil {
			return false, fmt.Errorf("active principal lookup for %q returned an invalid response", principalID)
		}
		resolvedID := normalizeID(*response.GetValue()[0].GetId())
		if resolvedID == "" || resolvedID != normalizeID(principalID) {
			return false, fmt.Errorf(
				"active principal lookup for %q returned unexpected ID %q",
				principalID,
				resolvedID,
			)
		}
		return true, nil
	}
}

func newGraphDeletedPrincipalLookup(graphClient *msgraphsdk.GraphServiceClient) deletedPrincipalLookup {
	return func(ctx context.Context, principalID string) (bool, error) {
		object, err := graphClient.Directory().DeletedItems().ByDirectoryObjectId(principalID).Get(ctx, nil)
		if err != nil {
			if isGraphNotFoundError(err) {
				return false, nil
			}
			return false, err
		}
		if object == nil || object.GetId() == nil {
			return false, fmt.Errorf("deleted principal %q was returned without a valid ID", principalID)
		}
		resolvedID := normalizeID(*object.GetId())
		if resolvedID == "" || resolvedID != normalizeID(principalID) {
			return false, fmt.Errorf(
				"deleted principal lookup for %q returned unexpected ID %q",
				principalID,
				resolvedID,
			)
		}
		return true, nil
	}
}

func principalRequiresRoleAssignmentRetention(
	ctx context.Context,
	principalID string,
	activeLookup activePrincipalLookup,
	deletedLookup deletedPrincipalLookup,
) (bool, error) {
	active, err := activeLookup(ctx, principalID)
	if err != nil {
		return false, fmt.Errorf("failed checking active principal: %w", err)
	}
	if active {
		return true, nil
	}

	softDeleted, err := deletedLookup(ctx, principalID)
	if err != nil {
		return false, fmt.Errorf("failed checking soft-deleted principal: %w", err)
	}
	if softDeleted {
		return true, nil
	}

	active, err = activeLookup(ctx, principalID)
	if err != nil {
		return false, fmt.Errorf("failed rechecking active principal: %w", err)
	}
	return active, nil
}

func resolveSoftDeletedPrincipalIDs(
	ctx context.Context,
	principalIDs sets.Set[string],
	lookup deletedPrincipalLookup,
) (sets.Set[string], error) {
	resolvedPrincipalIDs := sets.New[string]()
	for _, principalID := range sets.List(principalIDs) {
		softDeleted, err := lookup(ctx, principalID)
		if err != nil {
			return nil, fmt.Errorf("failed checking deleted principal %q: %w", principalID, err)
		}
		if softDeleted {
			resolvedPrincipalIDs.Insert(principalID)
		}
	}
	return resolvedPrincipalIDs, nil
}

func selectOrphanedRoleAssignments(
	assignments []roleAssignmentRecord,
	resolvedPrincipalIDs sets.Set[string],
) []roleAssignmentRecord {
	candidateIDs := sets.New[string]()
	candidates := make([]roleAssignmentRecord, 0, len(assignments))
	for _, assignment := range assignments {
		principalID := normalizeID(assignment.PrincipalID)
		if principalID == "" || resolvedPrincipalIDs.Has(principalID) {
			continue
		}
		normalizedAssignmentID := normalizeID(assignment.ID)
		if candidateIDs.Has(normalizedAssignmentID) {
			continue
		}
		candidateIDs.Insert(normalizedAssignmentID)
		candidates = append(candidates, assignment)
	}
	return candidates
}

func isGraphNotFoundError(err error) bool {
	var odataErr *graphodataerrors.ODataError
	return errors.As(err, &odataErr) && odataErr.ResponseStatusCode == http.StatusNotFound
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

func assignmentWithinSubscriptionScope(
	roleAssignment *armauthorization.RoleAssignment,
	subscriptionScopePrefix string,
) bool {
	id, ok := roleAssignmentID(roleAssignment)
	if !ok {
		return false
	}
	return strings.HasPrefix(normalizeID(id), subscriptionScopePrefix)
}
