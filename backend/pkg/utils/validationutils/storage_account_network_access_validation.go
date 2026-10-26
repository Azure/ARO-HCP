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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// This built-in defaults to Audit, not Deny.
const knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID = "/providers/microsoft.authorization/policydefinitions/b2982f36-99f2-4db5-8eff-283140c09693"

// StorageAccountNetworkAccessValidation detects confirmed policy conflicts with
// creating the Enabled image registry's public-network-enabled Storage Account.
// It does not validate other storage or implement private endpoint support.
// Unsupported expressions, resource selectors and effect overrides are deliberate
// conservative gaps: an inconclusive policy must not prevent cluster creation.
type StorageAccountNetworkAccessValidation struct {
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

var _ ClusterValidation = (*StorageAccountNetworkAccessValidation)(nil)

func NewStorageAccountNetworkAccessValidation(builder azureclient.FirstPartyApplicationClientBuilder) *StorageAccountNetworkAccessValidation {
	return &StorageAccountNetworkAccessValidation{azureFPAClientBuilder: builder}
}

// StorageAccountNetworkAccessValidationName is shared with creation preconditions.
const StorageAccountNetworkAccessValidationName = "StorageAccountNetworkAccessValidation"

func (v *StorageAccountNetworkAccessValidation) Name() string {
	return StorageAccountNetworkAccessValidationName
}

func (v *StorageAccountNetworkAccessValidation) Validate(ctx context.Context, subscription *coreapi.Subscription, cluster *coreapi.Cluster) ValidationResult {
	if cluster.CustomerProperties.ClusterImageRegistry.State == metadataapi.ClusterImageRegistryStateDisabled || cluster.ServiceProviderProperties.ClusterServiceID != nil {
		return SkippedValidation("NotApplicable", "Storage Account network access validation is only required when creating a cluster with its image registry enabled.", "Registry disabled or cluster already created; skipping creation-only validation.")
	}
	unknown := func(err error) ValidationResult {
		return UnknownValidation("InternalError", "Unable to verify Storage Account network access policy compliance.", err.Error(), ControllerReportingPolicyTypeError)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || *subscription.Properties.TenantId == "" {
		return unknown(fmt.Errorf("subscription is missing tenant ID"))
	}
	tenant, sub := *subscription.Properties.TenantId, cluster.ID.SubscriptionID
	rg := cluster.CustomerProperties.Platform.ManagedResourceGroup
	target := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", sub, rg)
	assignmentsClient, err := v.azureFPAClientBuilder.PolicyAssignmentsClient(tenant, sub)
	if err != nil {
		return unknown(fmt.Errorf("failed to get policy assignments client: %w", err))
	}
	var assignments []*armpolicy.Assignment
	// atScope at the target RG includes inherited AND RG-local assignments,
	// excluding assignments on individual resources or sibling groups.
	pager := assignmentsClient.NewListForResourceGroupPager(rg, &armpolicy.AssignmentsClientListForResourceGroupOptions{Filter: to.Ptr("atScope()")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return unknown(fmt.Errorf("failed to list policy assignments for managed resource group: %w", err))
		}
		assignments = append(assignments, page.Value...)
	}
	var exemptions []*azureclient.PolicyExemption
	exemptionsLoaded := false
	// Only a confirmed candidate needs exemptions. Cache the list, not the
	// applicability decision: each candidate has its own assignment/member.
	isExempt := func(assignment *armpolicy.Assignment, reference string) (bool, error) {
		if !exemptionsLoaded {
			client, err := v.azureFPAClientBuilder.PolicyExemptionsClient(tenant, sub)
			if err != nil {
				return false, fmt.Errorf("failed to get policy exemptions client: %w", err)
			}
			pager := client.NewListForResourceGroupPager(rg)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return false, fmt.Errorf("failed to list policy exemptions: %w", err)
				}
				exemptions = append(exemptions, page.Value...)
			}
			exemptionsLoaded = true
		}
		return policyAssignmentExempt(exemptions, assignment, reference, target, time.Now()), nil
	}
	definitions, err := v.azureFPAClientBuilder.PolicyDefinitionsClient(tenant, sub)
	if err != nil {
		return unknown(fmt.Errorf("failed to get policy definitions client: %w", err))
	}
	for _, assignment := range assignments {
		if !assignmentIsEnforced(assignment) || assignmentExcludesManagedResourceGroup(assignment, target) || assignmentHasPolicyEffectOverride(assignment) {
			continue
		}
		if assignment.Properties.PolicyDefinitionID == nil {
			continue
		}
		id := *assignment.Properties.PolicyDefinitionID
		kind, scope, name := storagePolicyDefinitionID(id)
		blocks := false
		if kind == "policysetdefinitions" {
			sets, err := v.azureFPAClientBuilder.PolicySetDefinitionsClient(tenant, sub)
			if err != nil {
				return unknown(fmt.Errorf("failed to get policy set definitions client: %w", err))
			}
			set, err := getStoragePolicySet(ctx, sets, scope, name, sub)
			if err != nil {
				return unknown(fmt.Errorf("failed to get policy set %q: %w", id, err))
			}
			if set == nil || set.Properties == nil {
				continue
			}
			parameters := storagePolicyParameters(set.Properties.Parameters, assignment.Properties.Parameters, nil)
			// Nested/unknown references are ignored instead of recursively
			// interpreting arbitrary definitions.
			for _, member := range set.Properties.PolicyDefinitions {
				if member == nil || member.PolicyDefinitionID == nil {
					continue
				}
				ref := ""
				if member.PolicyDefinitionReferenceID != nil {
					ref = *member.PolicyDefinitionReferenceID
				}
				blocks, err = storagePolicyDenies(ctx, definitions, *member.PolicyDefinitionID, sub, member.Parameters, parameters, cluster.Location)
				if err != nil {
					return unknown(err)
				}
				if blocks {
					exempt, err := isExempt(assignment, ref)
					if err != nil {
						return unknown(err)
					}
					blocks = !exempt
				}
				if blocks {
					break
				}
			}
		} else {
			blocks, err = storagePolicyDenies(ctx, definitions, id, sub, assignment.Properties.Parameters, nil, cluster.Location)
			if err != nil {
				return unknown(err)
			}
			if blocks {
				exempt, err := isExempt(assignment, "")
				if err != nil {
					return unknown(err)
				}
				blocks = !exempt
			}
		}
		if blocks {
			id := "unknown assignment ID"
			if assignment.ID != nil {
				id = *assignment.ID
			}
			message := fmt.Sprintf("Azure Policy assignment %q (%s) conflicts with the image registry Storage Account's required public network access Enabled in managed resource group %q. The policy can deny this access or disable it through Modify; actual modification depends on the resource API version. An Enabled cluster image registry requires this access. Exempt the managed resource group from the applicable policy, change its effective effect to Audit or Disabled, or recreate the cluster with properties.clusterImageRegistry.state set to Disabled.", assignmentDisplayName(assignment), id, target)
			return FailedValidation("StorageAccountPublicNetworkAccessDenied", message, message)
		}
	}
	message := fmt.Sprintf("No confirmed enforced policy conflict with Enabled image registry Storage Account public network access in %q.", target)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, message, message)
}

func assignmentIsEnforced(assignment *armpolicy.Assignment) bool {
	return assignment != nil && assignment.Properties != nil && (assignment.Properties.EnforcementMode == nil || *assignment.Properties.EnforcementMode == armpolicy.EnforcementModeDefault)
}

func assignmentExcludesManagedResourceGroup(assignment *armpolicy.Assignment, target string) bool {
	for _, scope := range assignment.Properties.NotScopes {
		if scope == nil || *scope == "" {
			continue
		}
		// Management group ancestry is unavailable: any such exclusion may
		// exclude this subscription, so conservatively do not block.
		if strings.HasPrefix(strings.ToLower(*scope), "/providers/microsoft.management/managementgroups/") || storagePolicyScopeContains(*scope, target) {
			return true
		}
	}
	return len(assignment.Properties.ResourceSelectors) > 0
}

func assignmentHasPolicyEffectOverride(assignment *armpolicy.Assignment) bool {
	for _, override := range assignment.Properties.Overrides {
		if override != nil && override.Kind != nil && *override.Kind == armpolicy.OverrideKindPolicyEffect {
			// Includes member selectors: conservatively skip the whole
			// initiative rather than treating an overridden member as Deny.
			return true
		}
	}
	return false
}

func storagePolicyScopeContains(scope, target string) bool {
	scope, target = strings.ToLower(strings.TrimRight(scope, "/")), strings.ToLower(target)
	return scope != "" && (scope == target || strings.HasPrefix(target, scope+"/"))
}

func policyAssignmentExempt(exemptions []*azureclient.PolicyExemption, assignment *armpolicy.Assignment, reference, target string, now time.Time) bool {
	for _, exemption := range exemptions {
		if exemption == nil || exemption.Properties == nil || assignment.ID == nil {
			continue
		}
		p := exemption.Properties
		if p.PolicyAssignmentID == nil || !strings.EqualFold(*p.PolicyAssignmentID, *assignment.ID) || (p.ExpiresOn != nil && !p.ExpiresOn.After(now)) {
			continue
		}
		scope := exemption.Scope()
		// Unknown scope, management group ancestry, resource selectors and
		// unrecognized assignmentScopeValidation cannot disprove applicability.
		// Suppress conflicts conservatively; ignoring narrowing selectors may
		// miss a conflict but cannot invent one. Known unrelated scopes still
		// do not exempt the target.
		if strings.HasPrefix(strings.ToLower(scope), "/subscriptions/") && !storagePolicyScopeContains(scope, target) {
			continue
		}
		if len(p.PolicyDefinitionReferenceIDs) == 0 {
			return true
		}
		for _, ref := range p.PolicyDefinitionReferenceIDs {
			if ref != nil && reference != "" && strings.EqualFold(*ref, reference) {
				return true
			}
		}
	}
	return false
}

func assignmentDisplayName(assignment *armpolicy.Assignment) string {
	if assignment.Properties.DisplayName != nil {
		return *assignment.Properties.DisplayName
	}
	if assignment.Name != nil {
		return *assignment.Name
	}
	if assignment.ID != nil {
		return *assignment.ID
	}
	return "unknown policy assignment"
}

// Accept only unversioned, fully qualified built-in, subscription or management
// group definition IDs. Unsupported IDs are inconclusive.
func storagePolicyDefinitionID(id string) (kind, scope, name string) {
	parts := strings.Split(strings.TrimPrefix(id, "/"), "/")
	i := 0
	if len(parts) == 6 && strings.EqualFold(parts[0], "subscriptions") {
		scope, i = parts[1], 2
	} else if len(parts) == 8 && strings.EqualFold(parts[0], "providers") && strings.EqualFold(parts[1], "Microsoft.Management") && strings.EqualFold(parts[2], "managementGroups") {
		scope, i = "mg:"+parts[3], 4
	} else if len(parts) != 4 {
		return "", "", ""
	}
	if !strings.EqualFold(parts[i], "providers") || !strings.EqualFold(parts[i+1], "Microsoft.Authorization") || parts[i+3] == "" {
		return "", "", ""
	}
	return strings.ToLower(parts[i+2]), scope, parts[i+3]
}

func getStoragePolicySet(ctx context.Context, client azureclient.PolicySetDefinitionsClient, scope, name, sub string) (*armpolicy.SetDefinition, error) {
	switch {
	case strings.HasPrefix(scope, "mg:"):
		resp, err := client.GetAtManagementGroup(ctx, strings.TrimPrefix(scope, "mg:"), name, nil)
		return &resp.SetDefinition, err
	case scope == "":
		resp, err := client.GetBuiltIn(ctx, name, nil)
		return &resp.SetDefinition, err
	case strings.EqualFold(scope, sub):
		resp, err := client.Get(ctx, name, nil)
		return &resp.SetDefinition, err
	default:
		// Never fetch a different same-name definition in this subscription.
		return nil, nil
	}
}
