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

// Package directoryobjects permanently purges Microsoft Graph directory
// objects (service principals / application registrations) that have sat in
// Entra's deletedItems recycle bin long enough that a restore is implausible.
//
// This closes the gap the roleassignments package's SAFETY CONTRACT
// deliberately leaves open: that step never deletes a role assignment while
// its principal is still recoverable from deletedItems, so those assignments
// (and the directory quota they consume) only ever clear once Entra's own
// 30-day recycle-bin timer expires the object. This step reclaims that
// quota sooner, on a much shorter, still-safe grace period, and only for
// principals that already hold a role assignment in the target subscription -
// it never scans the tenant's directory blindly.
package directoryobjects

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphdirectoryobjects "github.com/microsoftgraph/msgraph-sdk-go/directoryobjects"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	graphodataerrors "github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

const (
	// ServicePrincipalResourceType is the runner.Target resource type reported
	// for a permanently purged deleted-items service principal.
	ServicePrincipalResourceType = "directory.deletedItems/servicePrincipal"
	// ApplicationResourceType is the runner.Target resource type reported for
	// a permanently purged deleted-items application registration.
	ApplicationResourceType = "directory.deletedItems/application"

	// DefaultMinAge is how long a directory object must have sat in
	// deletedItems before this step will purge it. Kept well short of Entra's
	// 30-day auto-expiry, so this step (not the tenant-wide timer) is what
	// reclaims role-assignment quota, while still leaving a real window to
	// notice and restore an accidental deletion.
	DefaultMinAge = 7 * 24 * time.Hour

	graphGetByIDsBatchSize = 1000
)

// PurgeAgedDeletedStepConfig configures aged-deleted-directory-object purge
// behavior.
type PurgeAgedDeletedStepConfig struct {
	RoleAssignmentsClient *armauthorization.RoleAssignmentsClient
	// GraphClient must be backed by a credential holding directory-write
	// permission (e.g. Application.ReadWrite.All / Directory.ReadWrite.All),
	// unlike the read-only credential the roleassignments step uses. Keep
	// these as two distinct client/credential pairs, mirroring how
	// RoleAssignmentsClient and the roleassignments package's GraphClient are
	// already split by permission scope.
	GraphClient    *msgraphsdk.GraphServiceClient
	SubscriptionID string

	// MinAge overrides DefaultMinAge when non-zero.
	MinAge time.Duration
	// Now overrides time.Now for tests.
	Now func() time.Time

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

type purgeAgedDeletedStep struct {
	cfg             PurgeAgedDeletedStepConfig
	name            string
	retries         int
	continueOnError bool
	verify          runner.VerifyFn
	minAge          time.Duration
	now             func() time.Time
}

var _ runner.Step = (*purgeAgedDeletedStep)(nil)

// NewPurgeAgedDeletedStep builds the aged-deleted-directory-object purge step.
func NewPurgeAgedDeletedStep(cfg PurgeAgedDeletedStepConfig) (runner.Step, error) {
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
		stepName = "Purge aged deleted directory objects"
	}

	minAge := cfg.MinAge
	if minAge <= 0 {
		minAge = DefaultMinAge
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &purgeAgedDeletedStep{
		cfg:             cfg,
		name:            stepName,
		retries:         cfg.Retries,
		continueOnError: cfg.ContinueOnError,
		verify:          cfg.Verify,
		minAge:          minAge,
		now:             now,
	}, nil
}

// MustNewPurgeAgedDeletedStep builds the step and panics on invalid config.
func MustNewPurgeAgedDeletedStep(cfg PurgeAgedDeletedStepConfig) runner.Step {
	step, err := NewPurgeAgedDeletedStep(cfg)
	if err != nil {
		panic(err)
	}
	return step
}

func (s *purgeAgedDeletedStep) Name() string {
	return s.name
}

func (s *purgeAgedDeletedStep) RetryLimit() int {
	if s.retries < runner.DefaultRetries {
		return runner.DefaultRetries
	}
	return s.retries
}

func (s *purgeAgedDeletedStep) ContinueOnError() bool {
	return s.continueOnError
}

func (s *purgeAgedDeletedStep) Verify(ctx context.Context) error {
	if s.verify == nil {
		return nil
	}
	return s.verify(ctx)
}

// deletedObjectRecord is a candidate directory object discovered in
// deletedItems along with the metadata needed to decide whether it is safe
// and old enough to purge.
type deletedObjectRecord struct {
	ID              string
	DisplayName     string
	ResourceType    string
	DeletedDateTime *time.Time
}

func (r deletedObjectRecord) ToTarget() runner.Target {
	name := r.DisplayName
	if name == "" {
		name = r.ID
	}
	return runner.Target{
		ID:   r.ID,
		Name: name,
		Type: r.ResourceType,
	}
}

func (s *purgeAgedDeletedStep) Discover(ctx context.Context) ([]runner.Target, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	skipReporter := common.NewDiscoverySkipReporter(s.Name())
	defer skipReporter.Flush(logger)

	// 1) Collect the distinct principal IDs holding a role assignment in this
	// subscription. This step never scans the tenant's directory blindly - it
	// only ever considers principals already tied to this subscription's own
	// role-assignment quota pressure.
	principalIDs, err := listRoleAssignmentPrincipalIDs(ctx, s.cfg.RoleAssignmentsClient, s.cfg.SubscriptionID, logger, skipReporter)
	if err != nil {
		return nil, fmt.Errorf("failed listing role assignment principal IDs: %w", err)
	}
	if principalIDs.Len() == 0 {
		return nil, nil
	}

	// 2) Resolve which of those principals are still active. Active
	// principals are never purge candidates.
	activePrincipalIDs, err := resolveActivePrincipalIDs(ctx, s.cfg.GraphClient, principalIDs)
	if err != nil {
		return nil, fmt.Errorf("failed resolving active principals with Microsoft Graph getByIds: %w", err)
	}
	candidatePrincipalIDs := principalIDs.Difference(activePrincipalIDs)

	// 3) For each inactive principal, look it up in deletedItems. Only a
	// principal Graph itself reports as soft-deleted, of a purge-eligible
	// type, and older than minAge becomes a purge target. Anything not found
	// in deletedItems either (already gone, or never a directory principal)
	// is left alone - there is nothing here for this step to purge.
	targets := make([]runner.Target, 0)
	for _, principalID := range sets.List(candidatePrincipalIDs) {
		record, found, err := lookupDeletedDirectoryObject(ctx, s.cfg.GraphClient, principalID)
		if err != nil {
			skipReporter.Record(logger, "deleted_item_lookup_failed", "principalID", principalID, "error", err)
			continue
		}
		if !found {
			continue
		}
		if record.DeletedDateTime == nil {
			skipReporter.Record(logger, "deleted_item_missing_deleted_date_time", "principalID", principalID)
			continue
		}
		age := s.now().Sub(*record.DeletedDateTime)
		if age < s.minAge {
			continue
		}
		targets = append(targets, record.ToTarget())
	}

	if len(targets) == 0 {
		logger.Info(
			"No aged deleted directory objects discovered",
			"principalsScanned", principalIDs.Len(),
			"minAge", s.minAge.String(),
		)
		return targets, nil
	}

	logger.Info(
		"Discovered aged deleted directory objects",
		"count", len(targets),
		"principalsScanned", principalIDs.Len(),
		"minAge", s.minAge.String(),
	)

	return targets, nil
}

func (s *purgeAgedDeletedStep) Delete(ctx context.Context, target runner.Target, _ bool) error {
	err := s.cfg.GraphClient.Directory().DeletedItems().ByDirectoryObjectId(target.ID).Delete(ctx, nil)
	if err != nil {
		if isGraphNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("failed to purge deleted directory object %q: %w", target.ID, err)
	}
	return nil
}

func listRoleAssignmentPrincipalIDs(
	ctx context.Context,
	roleAssignmentsClient *armauthorization.RoleAssignmentsClient,
	subscriptionID string,
	logger logr.Logger,
	skipReporter *common.DiscoverySkipReporter,
) (sets.Set[string], error) {
	pager := roleAssignmentsClient.NewListForSubscriptionPager(nil)
	principalIDs := sets.New[string]()

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing role assignments: %w", err)
		}
		for _, roleAssignment := range page.Value {
			if roleAssignment == nil || roleAssignment.Properties == nil || roleAssignment.Properties.PrincipalID == nil {
				skipReporter.Record(logger, "invalid_role_assignment_payload")
				continue
			}
			principalID := normalizeID(*roleAssignment.Properties.PrincipalID)
			if principalID == "" {
				skipReporter.Record(logger, "missing_principal_id")
				continue
			}
			principalIDs.Insert(principalID)
		}
	}

	return principalIDs, nil
}

func resolveActivePrincipalIDs(
	ctx context.Context,
	graphClient *msgraphsdk.GraphServiceClient,
	principalIDs sets.Set[string],
) (sets.Set[string], error) {
	resolved := sets.New[string]()
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
				continue
			}
			resolved.Insert(normalizeID(*object.GetId()))
		}
	}
	return resolved, nil
}

// lookupDeletedDirectoryObject fetches a single deletedItems entry and
// classifies it into a purge-eligible resource type. Only service principals
// and applications are purge-eligible; any other recovered type (for example
// a soft-deleted user or group) is reported as not found so the caller never
// purges it.
func lookupDeletedDirectoryObject(
	ctx context.Context,
	graphClient *msgraphsdk.GraphServiceClient,
	principalID string,
) (deletedObjectRecord, bool, error) {
	object, err := graphClient.Directory().DeletedItems().ByDirectoryObjectId(principalID).Get(ctx, nil)
	if err != nil {
		if isGraphNotFoundError(err) {
			return deletedObjectRecord{}, false, nil
		}
		return deletedObjectRecord{}, false, err
	}
	if object == nil || object.GetId() == nil {
		return deletedObjectRecord{}, false, fmt.Errorf("deleted item %q was returned without a valid ID", principalID)
	}

	var resourceType string
	var displayName string
	switch v := object.(type) {
	case models.ServicePrincipalable:
		resourceType = ServicePrincipalResourceType
		if v.GetDisplayName() != nil {
			displayName = *v.GetDisplayName()
		}
	case models.Applicationable:
		resourceType = ApplicationResourceType
		if v.GetDisplayName() != nil {
			displayName = *v.GetDisplayName()
		}
	default:
		// Not a type this step is willing to purge (e.g. a soft-deleted user
		// or group holding a stale role assignment).
		return deletedObjectRecord{}, false, nil
	}

	record := deletedObjectRecord{
		ID:              normalizeID(*object.GetId()),
		DisplayName:     displayName,
		ResourceType:    resourceType,
		DeletedDateTime: object.GetDeletedDateTime(),
	}
	return record, true, nil
}

func isGraphNotFoundError(err error) bool {
	var odataErr *graphodataerrors.ODataError
	return errors.As(err, &odataErr) && odataErr.ResponseStatusCode == http.StatusNotFound
}

func normalizeID(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
