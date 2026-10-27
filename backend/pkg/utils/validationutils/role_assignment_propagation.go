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

package validationutils

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DefaultRoleAssignmentPropagationWindow limits how long a matching grant can be considered
// potential propagation evidence. It changes the failure guidance, not the operation deadline.
const DefaultRoleAssignmentPropagationWindow = 60 * time.Minute

// roleAssignmentPropagationRetryAfter is how soon a permission validation attributed to
// propagation delay is re-checked. It is shorter than the default non-passed retry so that a
// cluster whose role assignments become effective before the validation failure times out
// recovers on its own, without the customer having to retry at all.
const roleAssignmentPropagationRetryAfter = 30 * time.Second

const missingPermissionsPendingPropagationReason = "RoleAssignmentPropagationPending"

// propagationCheckFailedReason is the Unknown reason reported when the role-assignment
// lookup itself failed (throttling, connection resets, ARM 5xx) while checking for
// propagation delay, so propagation could be neither confirmed nor ruled out.
const propagationCheckFailedReason = "RoleAssignmentPropagationCheckFailed"

var (
	// roleAssignmentPropagationDetections counts runs where every missing action has a recent
	// matching role grant and the failure carries propagation guidance.
	roleAssignmentPropagationDetections = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_detections_total",
			Help: "Total number of permission validation runs with recent covering role grants that may still be propagating.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationLookupErrors counts failures to look up role assignments or
	// role definitions while checking for propagation guidance.
	roleAssignmentPropagationLookupErrors = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_lookup_errors_total",
			Help: "Total number of role assignment lookups that failed while checking for RBAC propagation delay; each reports the plain missing-permissions failure.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationLookupForbidden counts role-assignment or role-definition
	// lookups rejected as unauthorized, which disables propagation guidance.
	roleAssignmentPropagationLookupForbidden = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_lookup_forbidden_total",
			Help: "Total number of role assignment lookups rejected as unauthorized while checking for RBAC propagation delay; each reports the plain missing-permissions failure.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationWindowExpired counts checks with matching grants younger than twice
	// the window but at least one older than the window. This is a tuning signal, not proof of lag.
	roleAssignmentPropagationWindowExpired = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_window_expired_total",
			Help: "Total number of permission validation runs with covering role grants created within twice the propagation window but at least one older than the window.",
		},
		[]string{"validation"},
	)
)

type RoleAssignmentPropagationDetector struct {
	fpaClientBuilder azureclient.FirstPartyApplicationClientBuilder
	window           time.Duration
	clock            utilsclock.PassiveClock
}

func NewRoleAssignmentPropagationDetector(fpaClientBuilder azureclient.FirstPartyApplicationClientBuilder, window time.Duration, clock utilsclock.PassiveClock) *RoleAssignmentPropagationDetector {
	return &RoleAssignmentPropagationDetector{
		fpaClientBuilder: fpaClientBuilder,
		window:           window,
		clock:            clock,
	}
}

// roleAssignmentPropagationEvidence describes the recent role assignment that explains one
// missing (resource, identity) pair.
type roleAssignmentPropagationEvidence struct {
	Resource         string    `json:"resource"`
	Identity         string    `json:"identity"`
	RoleAssignmentID string    `json:"roleAssignmentId"`
	CreatedOn        time.Time `json:"createdOn"`
}

// propagationEvidenceResult is the outcome of checking missing permissions for propagation delay.
type propagationEvidenceResult struct {
	// Evidence holds one entry per missing permission when all of them can be attributed to
	// propagation, and is empty otherwise.
	Evidence []roleAssignmentPropagationEvidence
	// WindowExpired is true when Evidence is empty but every missing permission is covered by a
	// role assignment created within twice the window, at least one of them older than the window.
	WindowExpired bool
}

// findPropagationEvidence checks whether every entry in missing can be attributed to propagation.
// principalIDs maps the lowercased identity resource ID to its Entra principal ID.
func (d *RoleAssignmentPropagationDetector) findPropagationEvidence(ctx context.Context, tenantID string, missing []*identityResourceMissingPermissions, principalIDs map[string]string) (propagationEvidenceResult, error) {
	if len(missing) == 0 {
		return propagationEvidenceResult{}, nil
	}
	for _, entry := range missing {
		if !onlyNotAllowedDecisions(entry.Decisions) {
			return propagationEvidenceResult{}, nil
		}
		if principalIDs[strings.ToLower(entry.Identity.String())] == "" {
			return propagationEvidenceResult{}, nil
		}
	}

	clients := map[string]azureclient.RoleAssignmentsClient{}
	roleDefinitions := map[string]armauthorization.RoleDefinition{}
	assignmentsByPrincipal := map[string][]*armauthorization.RoleAssignment{}
	now := d.clock.Now()
	evidence := make([]roleAssignmentPropagationEvidence, 0, len(missing))
	windowExpired := false
	for _, entry := range missing {
		subscriptionID := entry.Resource.SubscriptionID
		principalID := principalIDs[strings.ToLower(entry.Identity.String())]
		cacheKey := strings.ToLower(subscriptionID + "/" + principalID)
		assignments, ok := assignmentsByPrincipal[cacheKey]
		if !ok {
			client, exists := clients[subscriptionID]
			if !exists {
				var err error
				client, err = d.fpaClientBuilder.RoleAssignmentsClient(tenantID, subscriptionID)
				if err != nil {
					return propagationEvidenceResult{}, utils.TrackError(fmt.Errorf("failed to build role assignments client for subscription %q: %w", subscriptionID, err))
				}
				clients[subscriptionID] = client
			}
			var err error
			assignments, err = d.listAssignments(ctx, client, "/subscriptions/"+subscriptionID, principalID)
			if err != nil {
				return propagationEvidenceResult{}, err
			}
			assignmentsByPrincipal[cacheKey] = assignments
		}

		found, expired, err := d.mostRecentAssignment(ctx, tenantID, assignments, entry, principalID, now, roleDefinitions)
		if err != nil {
			return propagationEvidenceResult{}, err
		}
		if found == nil {
			if !expired {
				return propagationEvidenceResult{}, nil
			}
			windowExpired = true
			continue
		}
		evidence = append(evidence, roleAssignmentPropagationEvidence{
			Resource:         entry.Resource.String(),
			Identity:         entry.Identity.String(),
			RoleAssignmentID: ptr.Deref(found.ID, ""),
			CreatedOn:        *found.Properties.CreatedOn,
		})
	}
	if windowExpired {
		return propagationEvidenceResult{WindowExpired: true}, nil
	}
	return propagationEvidenceResult{Evidence: evidence}, nil
}

func (d *RoleAssignmentPropagationDetector) listAssignments(ctx context.Context, client azureclient.RoleAssignmentsClient, scope string, principalID string) ([]*armauthorization.RoleAssignment, error) {
	filter := url.QueryEscape(fmt.Sprintf("principalId eq '%s'", principalID))
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: &filter})

	var assignments []*armauthorization.RoleAssignment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list role assignments for scope %q: %w", scope, err))
		}
		assignments = append(assignments, page.Value...)
	}
	return assignments, nil
}

func (d *RoleAssignmentPropagationDetector) mostRecentAssignment(ctx context.Context, tenantID string, assignments []*armauthorization.RoleAssignment, entry *identityResourceMissingPermissions, principalID string, now time.Time, roleDefinitions map[string]armauthorization.RoleDefinition) (*armauthorization.RoleAssignment, bool, error) {
	var mostRecent *armauthorization.RoleAssignment
	expired := false
	for _, assignment := range assignments {
		if !isRecentCoveringAssignment(assignment, entry.Resource.String(), principalID, now, 2*d.window) {
			continue
		}
		if assignment.Properties.RoleDefinitionID == nil || assignment.Properties.Condition != nil || assignment.Properties.DelegatedManagedIdentityResourceID != nil {
			continue
		}
		roleID := *assignment.Properties.RoleDefinitionID
		parsed, err := azcorearm.ParseResourceID(roleID)
		if err != nil || !strings.EqualFold(parsed.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") ||
			(parsed.SubscriptionID != "" && !strings.EqualFold(parsed.SubscriptionID, entry.Resource.SubscriptionID)) {
			continue
		}
		role, ok := roleDefinitions[strings.ToLower(roleID)]
		if !ok {
			client, err := d.fpaClientBuilder.RoleDefinitionsClient(tenantID)
			if err != nil {
				return nil, false, utils.TrackError(fmt.Errorf("failed to build role definitions client: %w", err))
			}
			response, err := client.GetByID(ctx, roleID, nil)
			if err != nil {
				return nil, false, utils.TrackError(fmt.Errorf("failed to get role definition %q: %w", roleID, err))
			}
			role = response.RoleDefinition
			roleDefinitions[strings.ToLower(roleID)] = role
		}
		if !roleGrantsAllMissingActions(role, entry.Decisions) {
			continue
		}
		if now.Sub(*assignment.Properties.CreatedOn) > d.window {
			expired = true
			continue
		}
		if mostRecent == nil || assignment.Properties.CreatedOn.After(*mostRecent.Properties.CreatedOn) {
			mostRecent = assignment
		}
	}
	return mostRecent, mostRecent == nil && expired, nil
}

func roleGrantsAllMissingActions(role armauthorization.RoleDefinition, decisions []*checkaccessv2AuthorizationDecisionData) bool {
	if role.Properties == nil || len(role.Properties.Permissions) == 0 {
		return false
	}
	for _, decision := range decisions {
		if decision == nil || decision.ActionID == "" {
			return false
		}
		covered := false
		for _, permission := range role.Properties.Permissions {
			if permission == nil {
				continue
			}
			if decision.IsDataAction && actionGrantedByPermission(decision.ActionID, permission.DataActions, permission.NotDataActions) ||
				!decision.IsDataAction && actionGrantedByPermission(decision.ActionID, permission.Actions, permission.NotActions) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func actionGrantedByPermission(action string, allowed, excluded []*string) bool {
	for _, pattern := range excluded {
		if pattern != nil && matchesRoleAction(*pattern, action) {
			return false
		}
	}
	for _, pattern := range allowed {
		if pattern != nil && matchesRoleAction(*pattern, action) {
			return true
		}
	}
	return false
}

func matchesRoleAction(pattern, action string) bool {
	pattern, action = strings.ToLower(pattern), strings.ToLower(action)
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(action, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == action
}

// isRecentCoveringAssignment reports whether assignment grants principalID a role that is
// inherited by scope and was created within window before now.
func isRecentCoveringAssignment(assignment *armauthorization.RoleAssignment, scope string, principalID string, now time.Time, window time.Duration) bool {
	if assignment == nil || assignment.Properties == nil {
		return false
	}
	props := assignment.Properties
	if props.PrincipalID == nil || !strings.EqualFold(*props.PrincipalID, principalID) {
		return false
	}
	if props.Scope == nil || !scopeCovers(*props.Scope, scope) {
		return false
	}
	if props.CreatedOn == nil {
		return false
	}
	age := now.Sub(*props.CreatedOn)
	return age >= -time.Minute && age <= window
}

func scopeCovers(assignmentScope string, resourceScope string) bool {
	assignment := strings.TrimSuffix(strings.ToLower(assignmentScope), "/")
	resource := strings.TrimSuffix(strings.ToLower(resourceScope), "/")
	return assignment != "" && (assignment == resource || strings.HasPrefix(resource, assignment+"/"))
}

func onlyNotAllowedDecisions(decisions []*checkaccessv2AuthorizationDecisionData) bool {
	if len(decisions) == 0 {
		return false
	}
	for _, decision := range decisions {
		if decision == nil || decision.AccessDecision != azurecheckaccessv2client.NotAllowed {
			return false
		}
	}
	return true
}

// onlyNotAllowedMissingPermissions reports whether every missing (resource, identity) pair has
// only NotAllowed decisions, i.e. whether propagation delay is a plausible cause for all of them.
func onlyNotAllowedMissingPermissions(missing []*identityResourceMissingPermissions) bool {
	if len(missing) == 0 {
		return false
	}
	for _, entry := range missing {
		if !onlyNotAllowedDecisions(entry.Decisions) {
			return false
		}
	}
	return true
}

func failureWithPropagationGuidance(ctx context.Context, detector *RoleAssignmentPropagationDetector, validationName string, tenantID string, missing []*identityResourceMissingPermissions, principalIDs map[string]string, missingPermissionsMessage string) (ValidationResult, bool) {
	if detector == nil {
		return ValidationResult{}, false
	}
	found, err := detector.findPropagationEvidence(ctx, tenantID, missing, principalIDs)
	if err != nil {
		recordPropagationCheckFailure(ctx, validationName, err)
		if azureclient.IsForbiddenErr(err) {
			// 403 means the first-party application cannot read role assignments at all, so
			// there is never propagation evidence; the plain Failed is the honest result.
			return ValidationResult{}, false
		}
		// Non-403 errors (throttling, connection resets, ARM 5xx) are transient: we cannot
		// determine whether propagation explains the failure, so report Unknown rather
		// than potentially failing an operation whose only problem is a slow ARM call.
		return UnknownValidation(
			propagationCheckFailedReason,
			fmt.Sprintf("%s. Unable to verify whether recently created role assignments explain the missing permissions (lookup error). The operation will be retried.", missingPermissionsMessage),
			fmt.Sprintf("%s; role assignment lookup failed: %v", missingPermissionsMessage, err),
			ControllerReportingPolicyTypeLogOnly,
		), true
	}
	if found.WindowExpired {
		roleAssignmentPropagationWindowExpired.WithLabelValues(validationName).Inc()
		utils.LoggerFromContext(ctx).Info("missing permissions are covered by role assignments older than the RBAC propagation window; reporting the plain missing-permissions failure", "validation", validationName, "window", detector.window.String())
	}
	evidence := found.Evidence
	if len(evidence) == 0 {
		return ValidationResult{}, false
	}

	roleAssignmentPropagationDetections.WithLabelValues(validationName).Inc()
	utils.LoggerFromContext(ctx).Info("missing permissions are covered by recently created role assignments that may still be propagating", "validation", validationName, "window", detector.window.String(), "evidence", formatPropagationEvidence(evidence))

	userMessage := fmt.Sprintf("%s. Role assignments that include these permissions were created recently (most recently at %s), but access checks still report them missing. Azure may still be applying the grants. If the role assignments are correct, wait for them to take effect and retry the operation; if this persists, verify the assignments and any deny rules.",
		missingPermissionsMessage, newestEvidenceTime(evidence).UTC().Format(time.RFC3339))
	internalMessage := fmt.Sprintf("%s; covered by role assignments created within the last %s that may still be propagating: %s", missingPermissionsMessage, detector.window, formatPropagationEvidence(evidence))

	result := FailedValidation(missingPermissionsPendingPropagationReason, userMessage, internalMessage)
	// Re-check sooner than a plain failure would. A propagation tail that clears inside the
	// operation's overall budget resolves on its own without customer intervention.
	result.EarliestRetryAfter = ptr.To(roleAssignmentPropagationRetryAfter)
	return result, true
}

func recordPropagationCheckFailure(ctx context.Context, validationName string, err error) {
	if azureclient.IsForbiddenErr(err) {
		roleAssignmentPropagationLookupForbidden.WithLabelValues(validationName).Inc()
		utils.LoggerFromContext(ctx).Info("not authorized to check role assignments for RBAC propagation; reporting the plain missing-permissions failure", "validation", validationName, "error", err.Error())
		return
	}
	roleAssignmentPropagationLookupErrors.WithLabelValues(validationName).Inc()
	utils.LoggerFromContext(ctx).Info("unable to check role assignments for RBAC propagation; reporting the plain missing-permissions failure", "validation", validationName, "error", err.Error())
}

// newestEvidenceTime returns the most recent creation time across the evidence. It is the point
// from which the customer's remaining wait should be measured: every missing permission is
// covered by an assignment created at or before it.
func newestEvidenceTime(evidence []roleAssignmentPropagationEvidence) time.Time {
	newest := evidence[0].CreatedOn
	for _, e := range evidence[1:] {
		if e.CreatedOn.After(newest) {
			newest = e.CreatedOn
		}
	}
	return newest
}

func formatPropagationEvidence(evidence []roleAssignmentPropagationEvidence) string {
	parts := make([]string, 0, len(evidence))
	for _, e := range evidence {
		parts = append(parts, fmt.Sprintf("%s on %s via %s (created %s)", e.Identity, e.Resource, e.RoleAssignmentID, e.CreatedOn.UTC().Format(time.RFC3339)))
	}
	return strings.Join(parts, "; ")
}
