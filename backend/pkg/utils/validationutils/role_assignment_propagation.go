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
// prod, so the window is sized to cover that tail with margin. It matches the default create and
// update operation completion deadlines: a deferral can never outlast those deadlines anyway, and
// an operation that hits its deadline while deferred still reports the pending-propagation message.
const DefaultRoleAssignmentPropagationWindow = 60 * time.Minute

// roleAssignmentPropagationRetryAfter is how soon a validation deferred for propagation is
// retried. It is shorter than the default non-passed retry so a cluster is unblocked promptly
// once the assignment becomes effective.
const roleAssignmentPropagationRetryAfter = 30 * time.Second

const roleAssignmentPropagationPendingReason = "RoleAssignmentPropagationPending"

const roleAssignmentPropagationCheckFailedReason = "RoleAssignmentPropagationCheckFailed"

var (
	// roleAssignmentPropagationDeferrals counts permission validation runs that found missing
	// permissions but reported Unknown instead of Failed because every missing permission was
	// backed by a recently created role assignment.
	roleAssignmentPropagationDeferrals = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_deferrals_total",
			Help: "Total number of permission validation runs deferred because recently created role assignments may still be propagating.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationLookupErrors counts failures to look up role assignments (or, for
	// control plane identities, their principal IDs) while checking for propagation. A lookup
	// failure reports Unknown rather than Failed; see propagationCheckFailed.
	roleAssignmentPropagationLookupErrors = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_lookup_errors_total",
			Help: "Total number of role assignment lookups that failed while checking for RBAC propagation delay; each reports the validation as Unknown.",
		},
		[]string{"validation"},
	)

	// roleAssignmentPropagationLookupForbidden counts role assignment (or principal ID) lookups that
	// were rejected as unauthorized. Unlike other lookup failures these keep the original Failed
	// result: a missing read permission is persistent, so reporting Unknown would hold every
	// genuinely misconfigured cluster until the operation completion deadline.
	roleAssignmentPropagationLookupForbidden = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_rbac_propagation_lookup_forbidden_total",
			Help: "Total number of role assignment lookups rejected as unauthorized while checking for RBAC propagation delay; each keeps the original Failed result.",
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
// would otherwise report a terminal MissingRequiredPermissions failure for a correctly
// configured cluster.
//
// The detector is deliberately conservative. It only reports propagation as the likely cause
// when every missing (resource, identity) pair:
//   - has only NotAllowed decisions (an explicit Denied comes from a deny assignment and is never
//     a propagation artifact), and
//   - has at least one role assignment for the identity's principal, at or above the resource
//     scope, created within the propagation window.
//
// Any role definition is accepted as evidence because customers may grant equivalent built-in or
// custom roles; the propagation window bounds how long that evidence can defer a failure.
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

// deferForPropagationIfApplicable converts a missing-permissions finding into an Unknown result
// when the detector attributes every missing permission to RBAC propagation delay, or when the
// role assignment lookup itself fails with anything other than an authorization error (see
// propagationCheckFailed). It returns false when the caller should report its original Failed
// result.
func deferForPropagationIfApplicable(ctx context.Context, detector *RoleAssignmentPropagationDetector, validationName string, tenantID string, missing []*identityResourceMissingPermissions, principalIDs map[string]string, userMessage string) (ValidationResult, bool) {
	if detector == nil {
		return ValidationResult{}, false
	}
	found, err := detector.findPropagationEvidence(ctx, tenantID, missing, principalIDs)
	if err != nil {
		return propagationCheckFailed(ctx, validationName, err)
	}
	if found.WindowExpired {
		roleAssignmentPropagationWindowExpired.WithLabelValues(validationName).Inc()
		utils.LoggerFromContext(ctx).Info("missing permissions are covered by role assignments older than the RBAC propagation window; reporting the validation as Failed", "validation", validationName, "window", detector.window.String())
	}
	evidence := found.Evidence
	if len(evidence) == 0 {
		return ValidationResult{}, false
	}

	roleAssignmentPropagationDeferrals.WithLabelValues(validationName).Inc()
	internalMessage := fmt.Sprintf("missing permissions are covered by role assignments created within the last %s and may still be propagating: %s", detector.window, formatPropagationEvidence(evidence))
	result := UnknownValidation(
		roleAssignmentPropagationPendingReason,
		userMessage,
		internalMessage,
		ControllerReportingPolicyTypeLogOnly,
	)
	retryAfter := roleAssignmentPropagationRetryAfter
	result.EarliestRetryAfter = &retryAfter
	return result, true
}

// propagationCheckFailed decides the result when the missing permissions could not be checked for
// propagation delay.
//
// An authorization error (HTTP 403) returns false so the caller keeps its original Failed result.
// Missing read access is a persistent configuration gap, not a transient fault; reporting Unknown
// would hold every genuinely misconfigured cluster until the operation completion deadline instead
// of failing it promptly with an actionable message. Such errors are counted by the lookup
// forbidden metric.
//
// Any other error reports Unknown. The lookup only runs once every missing permission is
// NotAllowed (never explicitly denied) and every identity's principal is known, i.e. exactly when
// propagation is a plausible cause, so a lookup failure leaves the cause undetermined rather than
// confirming a missing permission. Reporting Failed instead would let transient ARM errors during a
// propagation deferral record fresh failures, and intermittent errors could keep the condition
// False long enough to fail the operation terminally. A persistent lookup failure is surfaced
// through the lookup error metric and the controller error reporting, and is bounded by the
// operation completion deadlines.
func propagationCheckFailed(ctx context.Context, validationName string, err error) (ValidationResult, bool) {
	if azureclient.IsForbiddenErr(err) {
		roleAssignmentPropagationLookupForbidden.WithLabelValues(validationName).Inc()
		utils.LoggerFromContext(ctx).Info("not authorized to check role assignments for RBAC propagation; reporting the validation as Failed", "validation", validationName, "error", err.Error())
		return ValidationResult{}, false
	}
	roleAssignmentPropagationLookupErrors.WithLabelValues(validationName).Inc()
	utils.LoggerFromContext(ctx).Info("unable to check role assignments for RBAC propagation; reporting the validation as Unknown", "validation", validationName, "error", err.Error())
	return UnknownValidation(
		roleAssignmentPropagationCheckFailedReason,
		"Unable to confirm whether recently created role assignments are still taking effect; the permission check will be retried.",
		fmt.Sprintf("missing permissions could not be checked for RBAC propagation delay: %v", err),
		ControllerReportingPolicyTypeError,
	), true
}

func formatPropagationEvidence(evidence []roleAssignmentPropagationEvidence) string {
	parts := make([]string, 0, len(evidence))
	for _, e := range evidence {
		parts = append(parts, fmt.Sprintf("%s on %s via %s (created %s)", e.Identity, e.Resource, e.RoleAssignmentID, e.CreatedOn.UTC().Format(time.RFC3339)))
	}
	return strings.Join(parts, "; ")
}
