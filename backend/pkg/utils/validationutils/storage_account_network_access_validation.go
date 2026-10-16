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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID is the resource ID
// of the built-in Azure Policy "Storage accounts should disable public network
// access" definition. Its "effect" parameter defaults to "Deny" (allowed values:
// Audit, Deny, Disabled). See:
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
// includes those inherited from parent management groups); it does not evaluate
// assignments scoped only to a specific resource group other than the
// subscription itself, nor NotScopes/ResourceSelectors exclusions in detail.
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

// listSubscriptionPolicyAssignments pages through all policy assignments effective
// within the cluster's subscription, including those inherited from parent
// management groups.
func (v *StorageAccountNetworkAccessValidation) listSubscriptionPolicyAssignments(
	ctx context.Context, assignmentsClient azureclient.PolicyAssignmentsClient,
) ([]*armpolicy.Assignment, error) {
	var assignments []*armpolicy.Assignment

	pager := assignmentsClient.NewListPager(nil)
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
			// The built-in policy's "effect" parameter defaults to "Deny" when not overridden.
			effect = "Deny"
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

	if strings.Contains(rule, `"effect":"deny"`) || strings.Contains(rule, `"effect": "deny"`) {
		return true
	}

	// The rule's effect is likely parameterized (e.g. "effect": "[parameters('effect')]").
	// Resolve it from the assignment's override, falling back to the definition's default.
	effect := assignedEffect
	if effect == "" {
		effect = policyDefinitionDefaultEffect(definitionProperties)
	}
	return strings.EqualFold(effect, "Deny")
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
