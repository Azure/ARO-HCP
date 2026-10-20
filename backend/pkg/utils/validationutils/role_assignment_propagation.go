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
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DefaultRoleAssignmentPropagationWindow is how long after a role assignment is created that a
// NotAllowed CheckAccessV2 decision for its principal is attributed to Azure RBAC propagation
// delay rather than to a genuinely missing permission. Azure documents propagation of up to
// ~10 minutes in most cases, but propagation of 46 minutes and more has been observed in stage and
// prod, so the window is sized to cover that tail with margin.
//
// The window only selects which failure the customer is told about. It never extends how long an
// operation runs: a permission validation that keeps failing still fails the operation on the
// usual schedule whether or not propagation explains it.
const DefaultRoleAssignmentPropagationWindow = 60 * time.Minute

// roleAssignmentPropagationRetryAfter is how soon a permission validation attributed to
// propagation delay is re-checked. It is shorter than the default non-passed retry so that a
// cluster whose role assignments become effective before the validation failure times out
// recovers on its own, without the customer having to retry at all.
const roleAssignmentPropagationRetryAfter = 30 * time.Second

// missingPermissionsPendingPropagationReason is the Failed reason used when the missing
// permissions are explained by recently created role assignments. It is distinct from the plain
// MissingRequiredPermissions reason so that the two cases are separable in conditions, logs and
// support escalations, even though both are terminal failures on the same schedule.
const missingPermissionsPendingPropagationReason = "MissingRequiredPermissionsPendingPropagation"

var (
	// roleAssignmentPropagationDetections counts permission validation runs whose missing
	// permissions were all backed by a recently created role assignment, and which therefore
	// failed with propagation guidance instead of the plain missing-permissions message.
	roleAssignmentPropagationDetections = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_detections_total",
			Help: "Total number of permission validation failures attributed to recently created role assignments that may still be propagating.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationLookupErrors counts failures to look up role assignments (or, for
	// control plane identities, their principal IDs) while checking for propagation. The
	// validation still reports its plain missing-permissions failure; see
	// recordPropagationCheckFailure.
	roleAssignmentPropagationLookupErrors = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_lookup_errors_total",
			Help: "Total number of role assignment lookups that failed while checking for RBAC propagation delay; each reports the plain missing-permissions failure.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationLookupForbidden counts role assignment (or principal ID) lookups
	// that were rejected as unauthorized. It is tracked separately from other lookup failures
	// because it is an ARO-side gap — the first-party application is missing
	// Microsoft.Authorization/roleAssignments/read on a scope the permission validations check —
	// rather than a transient fault, and it silently disables the propagation guidance.
	roleAssignmentPropagationLookupForbidden = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_lookup_forbidden_total",
			Help: "Total number of role assignment lookups rejected as unauthorized while checking for RBAC propagation delay; each reports the plain missing-permissions failure.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationWindowExpired counts permission validation runs that reported Failed
	// even though every missing permission was covered by a role assignment created within twice
	// the propagation window, with at least one only older than the window. A sustained rate
	// suggests the window is too short for observed propagation delays. It is a tuning signal, not
	// proof: a cluster that is genuinely misconfigured can also have a recent, unrelated assignment.
	roleAssignmentPropagationWindowExpired = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_window_expired_total",
			Help: "Total number of permission validation runs that failed while every missing permission was covered by a role assignment created within twice the propagation window but at least one was older than the window.",
		},
		[]string{"validation"},
	)
)

// RoleAssignmentPropagationDetector decides whether missing permissions reported by CheckAccessV2
// can be explained by role assignments that were created recently and have not yet propagated.
//
// Azure RBAC is eventually consistent: a role assignment is visible through the ARM API before
// authorization decisions honour it. Customers commonly create the cluster's role assignments
// immediately before creating the cluster, so a permission validation that runs in that gap
// reports a MissingRequiredPermissions failure naming permissions the customer has in fact
// already granted — leaving them nothing to act on.
//
// The detector does not change whether such a validation fails, only what the failure says. When
// it attributes the missing permissions to propagation, the customer is told that the grant is
// in flight and that retrying is sufficient, instead of being told to fix permissions that are
// already correct.
//
// The detector is deliberately conservative. It only reports propagation as the likely cause
// when every missing (resource, identity) pair:
//   - has only NotAllowed decisions (an explicit Denied comes from a deny assignment and is never
//     a propagation artifact), and
//   - has at least one role assignment for the identity's principal, at or above the resource
//     scope, created within the propagation window.
//
// Any role definition is accepted as evidence because customers may grant equivalent built-in or
// custom roles; the propagation window bounds how long an assignment counts as evidence.
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
// principalIDs maps the lowercased identity resource ID to its Entra principal ID. A nil detector
// never finds evidence.
func (d *RoleAssignmentPropagationDetector) findPropagationEvidence(ctx context.Context, tenantID string, missing []*identityResourceMissingPermissions, principalIDs map[string]string) (propagationEvidenceResult, error) {
	if d == nil || len(missing) == 0 {
		return propagationEvidenceResult{}, nil
	}
	for _, entry := range missing {
		if !onlyNotAllowedDecisions(entry.Decisions) {
			return propagationEvidenceResult{}, nil
		}
		if _, ok := principalIDs[strings.ToLower(entry.Identity.String())]; !ok {
			return propagationEvidenceResult{}, nil
		}
	}

	clients := map[string]azureclient.RoleAssignmentsClient{}
	now := d.clock.Now()
	evidence := make([]roleAssignmentPropagationEvidence, 0, len(missing))
	windowExpired := false
	for _, entry := range missing {
		subscriptionID := entry.Resource.SubscriptionID
		client, ok := clients[subscriptionID]
		if !ok {
			var err error
			client, err = d.fpaClientBuilder.RoleAssignmentsClient(tenantID, subscriptionID)
			if err != nil {
				return propagationEvidenceResult{}, utils.TrackError(fmt.Errorf("failed to build role assignments client for subscription %q: %w", subscriptionID, err))
			}
			clients[subscriptionID] = client
		}

		principalID := principalIDs[strings.ToLower(entry.Identity.String())]
		found, expired, err := d.mostRecentAssignment(ctx, client, entry.Resource.String(), principalID, now)
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

// mostRecentAssignment returns the most recently created role assignment for principalID at or
// above scope that falls within the propagation window, or nil if there is none. When there is
// none, expired reports whether a covering assignment was created within twice the window.
func (d *RoleAssignmentPropagationDetector) mostRecentAssignment(ctx context.Context, client azureclient.RoleAssignmentsClient, scope string, principalID string, now time.Time) (*armauthorization.RoleAssignment, bool, error) {
	filter := fmt.Sprintf("principalId eq '%s'", principalID)
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: &filter})

	var mostRecent *armauthorization.RoleAssignment
	expired := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, false, utils.TrackError(fmt.Errorf("failed to list role assignments for scope %q: %w", scope, err))
		}
		for _, assignment := range page.Value {
			if !isRecentCoveringAssignment(assignment, scope, principalID, now, 2*d.window) {
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
	}
	return mostRecent, mostRecent == nil && expired, nil
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

// scopeCovers reports whether a role assignment at assignmentScope applies to resourceScope.
// ListForScope without atScope() also returns assignments on child resources, which do not apply
// to the resource itself and are excluded. Assignments above the subscription (management groups,
// root) are returned by ARM only when they are ancestors, so they are accepted as-is.
func scopeCovers(assignmentScope string, resourceScope string) bool {
	assignment := strings.TrimSuffix(strings.ToLower(assignmentScope), "/")
	resource := strings.TrimSuffix(strings.ToLower(resourceScope), "/")
	if assignment == resource || assignment == "" {
		return true
	}
	if strings.HasPrefix(assignment, resource+"/") {
		return false
	}
	if strings.HasPrefix(resource, assignment+"/") {
		return true
	}
	return strings.HasPrefix(assignment, "/providers/microsoft.management/managementgroups/")
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

// failureWithPropagationGuidance restates a missing-permissions finding as a Failed result that
// tells the customer the permissions they already granted have not taken effect yet and that
// retrying is all that is needed. It returns false when the caller should report its plain
// missing-permissions failure: when there is no detector, when the evidence does not attribute
// every missing permission to propagation, or when the lookup itself failed (see
// recordPropagationCheckFailure).
//
// The outcome is Failed either way. Attributing the failure to propagation changes what the
// customer is told and how soon the validation is re-checked, never how long the operation is
// allowed to run: an operation blocked on permissions still fails on the usual schedule, which is
// the behaviour we want because retrying a failed create is cheap relative to waiting out an
// unbounded propagation tail.
func failureWithPropagationGuidance(ctx context.Context, detector *RoleAssignmentPropagationDetector, validationName string, tenantID string, missing []*identityResourceMissingPermissions, principalIDs map[string]string, missingPermissionsMessage string) (ValidationResult, bool) {
	if detector == nil {
		return ValidationResult{}, false
	}
	found, err := detector.findPropagationEvidence(ctx, tenantID, missing, principalIDs)
	if err != nil {
		recordPropagationCheckFailure(ctx, validationName, err)
		return ValidationResult{}, false
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

	userMessage := fmt.Sprintf("%s. Role assignments granting these permissions were created recently (most recently at %s) and have not taken effect yet. Azure applies role assignment changes asynchronously, usually within a few minutes but sometimes considerably longer. No configuration change is required: wait for the role assignments to take effect, then retry the operation.",
		missingPermissionsMessage, newestEvidenceTime(evidence).UTC().Format(time.RFC3339))
	internalMessage := fmt.Sprintf("%s; covered by role assignments created within the last %s that may still be propagating: %s", missingPermissionsMessage, detector.window, formatPropagationEvidence(evidence))

	result := FailedValidation(
		missingPermissionsPendingPropagationReason,
		userMessage,
		internalMessage,
	)
	// Re-check sooner than a plain failure would. The operation still fails on the usual
	// schedule, but a propagation tail that clears inside that budget then resolves on its own.
	result.EarliestRetryAfter = ptr.To(roleAssignmentPropagationRetryAfter)
	return result, true
}

// recordPropagationCheckFailure records that the missing permissions could not be checked for
// propagation delay. The caller always reports its plain missing-permissions failure: without the
// lookup there is no evidence to attribute the failure to propagation, and a validation that is
// already failing should not be held open on the chance that it might have been attributable.
//
// Authorization errors (HTTP 403) are counted separately. They mean the first-party application
// lacks Microsoft.Authorization/roleAssignments/read on a checked scope, which disables the
// propagation guidance entirely rather than intermittently, so they need to be distinguishable
// from transient ARM faults in the metric.
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
