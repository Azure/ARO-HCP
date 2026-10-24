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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID is the resource ID
// of the built-in Azure Policy "Storage accounts should disable public network
// access" definition. Its "effect" parameter defaults to "Audit" (allowed values:
// Audit, Deny, Disabled) per the published definition, so an assignment without
// an explicit override does not actually block Storage Account creation. See:
// https://github.com/Azure/azure-policy/blob/main/built-in-policies/policyDefinitions/Storage/StoragePublicNetworkAccess_AuditDeny.json
// https://learn.microsoft.com/en-us/azure/governance/policy/samples/built-in-policies#storage
const knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID = "/providers/microsoft.authorization/policydefinitions/b2982f36-99f2-4db5-8eff-283140c09693"

// storageAccountPublicNetworkAccessDenyPolicyEffectParameterName is the parameter
// name used by the built-in policy (and conventionally by most custom policies
// modeled after it) to control the enforced effect (Deny/Audit/Disabled).
const storageAccountPublicNetworkAccessDenyPolicyEffectParameterName = "effect"

// StorageAccountNetworkAccessValidation detects Azure Policy assignments in the
// cluster's subscription that would deny creation of a Storage Account with
// public network access enabled.
//
// ARO-HCP's managed resource group hosts one or more Storage Accounts (used by
// the cluster's internal image registry and other HyperShift/Cluster Service
// managed storage) that are created with public network access enabled; ARO-HCP
// does not currently support provisioning them with public network access
// disabled. If a Deny-effect policy targeting Storage Account public network
// access is in effect for the subscription, cluster creation will eventually
// fail deep in Azure resource provisioning with an opaque error. This validation
// detects that conflict upfront and reports it with actionable guidance, instead
// of letting the customer discover it only after a failed/stuck cluster creation.
//
// This is a best-effort heuristic (Option A: "detect & report", as opposed to
// Option B: natively supporting private Storage Accounts, tracked separately).
// It inspects policy assignments effective at the subscription scope (which
// includes those inherited from parent management groups, and excludes
// assignments scoped only to a specific resource group other than the
// subscription itself, via the "atScope()" list filter); it skips an assignment
// whose NotScopes exclude the cluster's managed resource group. It does not
// evaluate ResourceSelectors in detail: since those can narrow applicability by
// resource properties this heuristic cannot inspect (location, type, etc.), an
// assignment with any ResourceSelectors is conservatively treated as not
// confidently applicable, to avoid a false conflict report.
type StorageAccountNetworkAccessValidation struct {
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

var _ ClusterValidation = (*StorageAccountNetworkAccessValidation)(nil)

func NewStorageAccountNetworkAccessValidation(
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) *StorageAccountNetworkAccessValidation {
	return &StorageAccountNetworkAccessValidation{
		azureFPAClientBuilder: azureFPAClientBuilder,
	}
}

func (v *StorageAccountNetworkAccessValidation) Name() string {
	return "StorageAccountNetworkAccessValidation"
}

func (v *StorageAccountNetworkAccessValidation) Validate(
	ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.Cluster,
) ValidationResult {
	if clusterSubscription.Properties == nil || clusterSubscription.Properties.TenantId == nil || *clusterSubscription.Properties.TenantId == "" {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Storage Account network access policy compliance.",
			"subscription is missing tenant ID",
			ControllerReportingPolicyTypeError,
		)
	}
	tenantID := *clusterSubscription.Properties.TenantId
	subscriptionID := cluster.ID.SubscriptionID
	managedResourceGroup := cluster.CustomerProperties.Platform.ManagedResourceGroup
	managedResourceGroupID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedResourceGroup)

	assignmentsClient, err := v.azureFPAClientBuilder.PolicyAssignmentsClient(tenantID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Storage Account network access policy compliance.",
			fmt.Sprintf("failed to get policy assignments client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	assignments, err := v.listSubscriptionPolicyAssignments(ctx, assignmentsClient)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Storage Account network access policy compliance.",
			err.Error(),
			ControllerReportingPolicyTypeError,
		)
	}

	definitionsClient, err := v.azureFPAClientBuilder.PolicyDefinitionsClient(tenantID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Storage Account network access policy compliance.",
			fmt.Sprintf("failed to get policy definitions client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	for _, assignment := range assignments {
		if !assignmentIsEnforced(assignment) {
			continue
		}
		if assignmentExcludesManagedResourceGroup(assignment, managedResourceGroupID) {
			continue
		}
		if assignmentHasPolicyEffectOverride(assignment) {
			continue
		}

		blocks, displayName, err := v.assignmentDeniesStorageAccountPublicNetworkAccess(ctx, definitionsClient, assignment)
		if err != nil {
			return UnknownValidation(
				"InternalError",
				"Unable to verify Storage Account network access policy compliance.",
				err.Error(),
				ControllerReportingPolicyTypeError,
			)
		}
		if blocks {
			// TODO: this we will implement in future when the armpolicy Go SDK
			// (currently v1.0.0, the latest published) gains an Exemptions
			// client. Azure Policy Exemptions (Microsoft.Authorization/
			// policyExemptions) are a separate resource type that can suppress
			// this assignment's effect for the subscription or the managed
			// resource group without appearing in NotScopes, so an assignment
			// exempted at either scope can still reach this branch and
			// incorrectly fail validation for a Storage Account Azure would
			// actually permit. Resolving this would mean listing applicable,
			// unexpired exemptions for the assignment/target scope (via the
			// policyExemptions REST API directly, since no typed SDK client
			// exists yet) before reporting a conflict.
			// See https://github.com/Azure/ARO-HCP/pull/7514#discussion_r4348166503.
			userMsg := fmt.Sprintf(
				"The Azure Policy assignment %q in this subscription denies Storage Accounts from having public network "+
					"access enabled. ARO-HCP clusters require a Storage Account with public network access enabled in the "+
					"cluster's managed resource group %q, so cluster creation will fail while this policy is enforced. To "+
					"proceed, exclude the managed resource group %q from the policy assignment (e.g. add it to the "+
					"assignment's excluded scopes), change the assignment's effect to Audit or Disabled, or set its "+
					"enforcement mode to DoNotEnforce.",
				displayName, managedResourceGroup, managedResourceGroup,
			)
			return FailedValidation("StorageAccountPublicNetworkAccessDenied", userMsg, userMsg)
		}
	}

	internalMsg := fmt.Sprintf(
		"No enforced Azure Policy assignment in subscription %q denies Storage Account public network access.",
		subscriptionID,
	)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalMsg, internalMsg)
}

// listSubscriptionPolicyAssignments pages through policy assignments effective
// at the cluster's subscription scope: assignments directly on the subscription
// and those inherited from parent management groups. The "atScope()" filter
// excludes assignments scoped only to a resource group or resource within the
// subscription other than the subscription itself, which cannot apply beyond
// their own scope and so are irrelevant to this subscription-wide check.
func (v *StorageAccountNetworkAccessValidation) listSubscriptionPolicyAssignments(
	ctx context.Context, assignmentsClient azureclient.PolicyAssignmentsClient,
) ([]*armpolicy.Assignment, error) {
	var assignments []*armpolicy.Assignment

	pager := assignmentsClient.NewListPager(&armpolicy.AssignmentsClientListOptions{
		Filter: to.Ptr("atScope()"),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list policy assignments for subscription: %w", err))
		}
		assignments = append(assignments, page.Value...)
	}

	return assignments, nil
}

// assignmentIsEnforced reports whether a policy assignment's effect is actually
// enforced during resource creation. A nil EnforcementMode means the default
// ("Default", enforced) applies.
func assignmentIsEnforced(assignment *armpolicy.Assignment) bool {
	if assignment == nil || assignment.Properties == nil {
		return false
	}
	mode := assignment.Properties.EnforcementMode
	return mode == nil || *mode == armpolicy.EnforcementModeDefault
}

// assignmentExcludesManagedResourceGroup reports whether a policy assignment's
// exclusions mean it should not be treated as applying to resourceGroupID (the
// cluster's managed resource group):
//
//   - NotScopes: true if any entry is resourceGroupID itself, or an ancestor
//     scope of it (e.g. the subscription or a containing management group),
//     since excluding an ancestor scope excludes everything beneath it. A
//     NotScopes entry scoped to a sibling or a specific resource within the
//     managed resource group does not exclude the resource group as a whole,
//     so it is not treated as an exclusion here.
//   - ResourceSelectors: true if any are present, since they filter by
//     resource properties (location, type, etc.) this heuristic cannot
//     evaluate without per-resource data. Treating their presence as "cannot
//     confirm applicability" avoids reporting a false Storage Account policy
//     conflict.
func assignmentExcludesManagedResourceGroup(assignment *armpolicy.Assignment, resourceGroupID string) bool {
	if assignment == nil || assignment.Properties == nil {
		return false
	}

	lowerResourceGroupID := strings.ToLower(resourceGroupID)
	for _, notScope := range assignment.Properties.NotScopes {
		if notScope == nil || *notScope == "" {
			continue
		}
		lowerNotScope := strings.ToLower(*notScope)
		if lowerNotScope == lowerResourceGroupID || strings.HasPrefix(lowerResourceGroupID, lowerNotScope+"/") {
			return true
		}
	}

	return len(assignment.Properties.ResourceSelectors) > 0
}

// assignmentHasPolicyEffectOverride reports whether the assignment has an
// Overrides entry of kind "policyEffect". Overrides are a mechanism separate
// from Parameters for changing an assignment's effective policy effect (e.g. a
// Deny effect from a parameter or the definition default can be overridden to
// Audit or Disabled); like ResourceSelectors, an Override can itself be scoped
// by Selectors (resource type, location, etc.) that this heuristic cannot
// evaluate without per-resource data. Any policyEffect override is
// conservatively treated as "cannot confirm the assignment's effective
// effect", to avoid reporting a false Storage Account policy conflict when an
// override has silently changed Deny to a non-blocking effect.
func assignmentHasPolicyEffectOverride(assignment *armpolicy.Assignment) bool {
	if assignment == nil || assignment.Properties == nil {
		return false
	}
	for _, override := range assignment.Properties.Overrides {
		if override == nil || override.Kind == nil {
			continue
		}
		if *override.Kind == armpolicy.OverrideKindPolicyEffect {
			return true
		}
	}
	return false
}

// assignmentDeniesStorageAccountPublicNetworkAccess reports whether the given
// policy assignment, as enforced, denies Storage Accounts from having public
// network access enabled. It returns a human-readable display name/identifier
// for the assignment for use in user-facing messages.
func (v *StorageAccountNetworkAccessValidation) assignmentDeniesStorageAccountPublicNetworkAccess(
	ctx context.Context, definitionsClient azureclient.PolicyDefinitionsClient, assignment *armpolicy.Assignment,
) (bool, string, error) {
	displayName := assignmentDisplayName(assignment)

	if assignment.Properties == nil || assignment.Properties.PolicyDefinitionID == nil {
		return false, displayName, nil
	}
	definitionID := *assignment.Properties.PolicyDefinitionID

	assignedEffect := assignmentEffectParameterValue(assignment)

	if strings.EqualFold(definitionID, knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID) {
		effect := assignedEffect
		if effect == "" {
			// The built-in policy's "effect" parameter defaults to "Audit" when not
			// overridden, which does not block resource creation.
			effect = "Audit"
		}
		return strings.EqualFold(effect, "Deny"), displayName, nil
	}

	definition, err := v.getPolicyDefinition(ctx, definitionsClient, definitionID)
	if err != nil {
		return false, displayName, fmt.Errorf("failed to get policy definition %q for assignment %q: %w", definitionID, displayName, err)
	}
	if definition == nil || definition.Properties == nil {
		return false, displayName, nil
	}

	blocks := policyRuleTargetsStorageAccountPublicNetworkAccessDeny(definition.Properties, assignedEffect)
	return blocks, displayName, nil
}

// getPolicyDefinition resolves a policy definition from its full resource ID,
// using the built-in lookup for the "/providers/Microsoft.Authorization/..." form
// and the subscription-scoped lookup for the "/subscriptions/{id}/providers/..."
// form. Management-group-scoped custom policy definitions are not resolved; such
// assignments are treated as non-blocking (reported as Unknown would be noisy for
// a best-effort detection heuristic).
func (v *StorageAccountNetworkAccessValidation) getPolicyDefinition(
	ctx context.Context, definitionsClient azureclient.PolicyDefinitionsClient, definitionID string,
) (*armpolicy.Definition, error) {
	name := policyDefinitionNameFromID(definitionID)
	if name == "" {
		return nil, nil
	}

	lowerID := strings.ToLower(definitionID)
	switch {
	case strings.HasPrefix(lowerID, "/providers/microsoft.authorization/policydefinitions/"):
		resp, err := definitionsClient.GetBuiltIn(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return &resp.Definition, nil
	case strings.Contains(lowerID, "/subscriptions/") && strings.Contains(lowerID, "/providers/microsoft.authorization/policydefinitions/"):
		resp, err := definitionsClient.Get(ctx, name, nil)
		if err != nil {
			return nil, err
		}
		return &resp.Definition, nil
	default:
		// Management-group-scoped (or otherwise unrecognized) definition ID; not resolved by this heuristic.
		//
		// TODO: this we will implement in future when we add support for
		// resolving policy set definitions (initiatives). An assignment's
		// PolicyDefinitionID can reference a "/policySetDefinitions/" ID
		// instead of a single policy definition; that also falls through to
		// this branch today, so the caller treats it as non-blocking even
		// when the initiative includes the known storage policy with an
		// effective Deny. Fixing this means fetching the set definition,
		// resolving its member policy definitions and their effective
		// per-member parameter mappings (policyDefinitionReferenceId), and
		// evaluating each member the same way a directly-assigned policy is
		// evaluated above.
		// See https://github.com/Azure/ARO-HCP/pull/7514#discussion_r4347875100.
		return nil, nil
	}
}

// policyDefinitionNameFromID extracts the trailing policy definition name segment
// from a full policy definition resource ID.
func policyDefinitionNameFromID(definitionID string) string {
	parts := strings.Split(definitionID, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// assignmentDisplayName returns a human-readable identifier for a policy
// assignment for use in user-facing messages: DisplayName if set, else Name, else
// ID, else PolicyDefinitionID, else a generic placeholder.
func assignmentDisplayName(assignment *armpolicy.Assignment) string {
	if assignment == nil {
		return "unknown policy assignment"
	}
	if assignment.Properties != nil && assignment.Properties.DisplayName != nil && *assignment.Properties.DisplayName != "" {
		return *assignment.Properties.DisplayName
	}
	if assignment.Name != nil && *assignment.Name != "" {
		return *assignment.Name
	}
	if assignment.ID != nil && *assignment.ID != "" {
		return *assignment.ID
	}
	if assignment.Properties != nil && assignment.Properties.PolicyDefinitionID != nil {
		return *assignment.Properties.PolicyDefinitionID
	}
	return "unknown policy assignment"
}

// assignmentEffectParameterValue returns the string value the assignment
// provides, if any, for a parameter conventionally named "effect" (case
// insensitive), which is how both the built-in policy and most custom policies
// modeled after it make their enforced effect (Deny/Audit/Disabled) overridable.
func assignmentEffectParameterValue(assignment *armpolicy.Assignment) string {
	if assignment.Properties == nil {
		return ""
	}
	for name, value := range assignment.Properties.Parameters {
		if !strings.EqualFold(name, storageAccountPublicNetworkAccessDenyPolicyEffectParameterName) {
			continue
		}
		if value == nil {
			continue
		}
		if s, ok := value.Value.(string); ok {
			return s
		}
	}
	return ""
}

// policyRuleTargetsStorageAccountPublicNetworkAccessDeny is a best-effort
// heuristic that inspects a custom policy definition's rule for whether it
// denies Storage Account public network access. It looks for the policy rule
// referencing both the Storage Account publicNetworkAccess field and a Deny
// effect, either hardcoded or driven by the assignment's "effect" parameter
// override (or, absent an override, the policy definition's own default value
// for that parameter).
//
// TODO: this we will implement in future when we replace this flat
// string-match with either (a) a real policy rule condition evaluator, or
// (b) switching this whole validation to call
// armpolicyinsights.PolicyRestrictionsClient.CheckAtResourceGroupScope with a
// hypothetical Storage Account resource (publicNetworkAccess: "Enabled")
// against the managed resource group, letting Azure's own policy engine
// decide instead of reimplementing it. Today, this function cannot
// distinguish a rule that denies publicNetworkAccess=="Enabled" (actually
// blocks ARO-HCP) from one that denies publicNetworkAccess=="Disabled" (would
// not block us at all), and ignores sibling conditions (tags, location,
// resource type qualifiers) that may narrow when the rule applies. Option (b)
// above would also naturally resolve policy-set membership and honor
// exemptions/overrides, closing several of this file's other open gaps at
// once; it is the preferred direction, pending an architecture decision.
// See https://github.com/Azure/ARO-HCP/pull/7514#discussion_r4347875042.
func policyRuleTargetsStorageAccountPublicNetworkAccessDeny(definitionProperties *armpolicy.DefinitionProperties, assignedEffect string) bool {
	if definitionProperties == nil || definitionProperties.PolicyRule == nil {
		return false
	}

	ruleJSON, err := json.Marshal(definitionProperties.PolicyRule)
	if err != nil {
		return false
	}
	rule := strings.ToLower(string(ruleJSON))

	targetsStorageAccountPublicNetworkAccess := strings.Contains(rule, "microsoft.storage/storageaccounts") &&
		strings.Contains(rule, "publicnetworkaccess")
	if !targetsStorageAccountPublicNetworkAccess {
		return false
	}

	// Resolve the rule's actual "then.effect" value, rather than assuming any
	// rule that isn't a literal "deny" must be parameterized on our "effect"
	// parameter: it could just as well be a literal "Audit"/"Disabled", or an
	// expression this heuristic does not understand (e.g. a conditional
	// policy function), neither of which this validation can resolve.
	thenEffect, ok := policyRuleThenEffect(definitionProperties.PolicyRule)
	if !ok {
		return false
	}
	if strings.EqualFold(thenEffect, "Deny") {
		return true
	}

	// then.effect is not a literal "Deny". Only continue trying to resolve it
	// if it is specifically an ARM template parameter reference to our
	// "effect" parameter (e.g. "[parameters('effect')]"); any other literal
	// (Audit/Disabled) or unsupported expression is conservatively treated as
	// non-blocking, since this heuristic does not evaluate general ARM
	// template functions.
	referencedParam, isParamRef := policyRuleEffectParameterReference(thenEffect)
	if !isParamRef || !strings.EqualFold(referencedParam, storageAccountPublicNetworkAccessDenyPolicyEffectParameterName) {
		return false
	}

	// Resolve the referenced parameter's value from the assignment's
	// override, falling back to the definition's default.
	effect := assignedEffect
	if effect == "" {
		effect = policyDefinitionDefaultEffect(definitionProperties)
	}
	return strings.EqualFold(effect, "Deny")
}

// policyRuleThenEffect extracts a policy rule's "then.effect" value as a
// string. Returns ok=false if the rule does not have that shape (e.g. "then"
// or "then.effect" are missing, or "effect" isn't a plain string), in which
// case the rule's effect cannot be confirmed.
func policyRuleThenEffect(policyRule any) (effect string, ok bool) {
	ruleMap, ok := policyRule.(map[string]any)
	if !ok {
		return "", false
	}
	thenMap, ok := ruleMap["then"].(map[string]any)
	if !ok {
		return "", false
	}
	effect, ok = thenMap["effect"].(string)
	return effect, ok
}

// policyRuleEffectParameterReferencePattern matches an ARM template parameter
// reference of the form "[parameters('name')]" (the "parameters" function
// name is matched case-insensitively, consistent with ARM template function
// name semantics).
var policyRuleEffectParameterReferencePattern = regexp.MustCompile(`(?i)^\[parameters\('([^']+)'\)\]$`)

// policyRuleEffectParameterReference reports whether effectValue is an ARM
// template parameter reference (e.g. "[parameters('effect')]"), and if so,
// returns the referenced parameter name.
func policyRuleEffectParameterReference(effectValue string) (paramName string, isParamRef bool) {
	matches := policyRuleEffectParameterReferencePattern.FindStringSubmatch(strings.TrimSpace(effectValue))
	if matches == nil {
		return "", false
	}
	return matches[1], true
}

// policyDefinitionDefaultEffect returns the default value of a policy
// definition's "effect" parameter (case-insensitive name match), if any.
func policyDefinitionDefaultEffect(definitionProperties *armpolicy.DefinitionProperties) string {
	for name, param := range definitionProperties.Parameters {
		if !strings.EqualFold(name, storageAccountPublicNetworkAccessDenyPolicyEffectParameterName) {
			continue
		}
		if param == nil {
			continue
		}
		if s, ok := param.DefaultValue.(string); ok {
			return s
		}
	}
	return ""
}
