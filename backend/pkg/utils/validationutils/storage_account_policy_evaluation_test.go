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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

type storagePolicyTestBuilder struct {
	azureclient.FirstPartyApplicationClientBuilder
	assignments    azureclient.PolicyAssignmentsClient
	definitions    azureclient.PolicyDefinitionsClient
	exemptions     azureclient.PolicyExemptionsClient
	sets           azureclient.PolicySetDefinitionsClient
	fail           string
	exemptionCalls int
}

func (b *storagePolicyTestBuilder) PolicyAssignmentsClient(tenant, sub string) (azureclient.PolicyAssignmentsClient, error) {
	if b.fail == "assignments" {
		return nil, errors.New("assignments client failure")
	}
	if b.assignments != nil {
		return b.assignments, nil
	}
	return b.FirstPartyApplicationClientBuilder.PolicyAssignmentsClient(tenant, sub)
}

func (b *storagePolicyTestBuilder) PolicyDefinitionsClient(tenant, sub string) (azureclient.PolicyDefinitionsClient, error) {
	if b.fail == "definitions" {
		return nil, errors.New("definitions client failure")
	}
	if b.definitions != nil {
		return b.definitions, nil
	}
	return b.FirstPartyApplicationClientBuilder.PolicyDefinitionsClient(tenant, sub)
}

func (b *storagePolicyTestBuilder) PolicyExemptionsClient(_, _ string) (azureclient.PolicyExemptionsClient, error) {
	b.exemptionCalls++
	if b.fail == "exemptions" {
		return nil, errors.New("exemptions client failure")
	}
	return b.exemptions, nil
}

func (b *storagePolicyTestBuilder) PolicySetDefinitionsClient(_, _ string) (azureclient.PolicySetDefinitionsClient, error) {
	if b.fail == "sets" {
		return nil, errors.New("sets client failure")
	}
	return b.sets, nil
}

type storagePolicyTestExemptions struct {
	items []*azureclient.PolicyExemption
	err   error
	calls int
}

func (c *storagePolicyTestExemptions) NewListForResourceGroupPager(rg string) *runtime.Pager[azureclient.PolicyExemptionListResponse] {
	c.calls++
	return runtime.NewPager(runtime.PagingHandler[azureclient.PolicyExemptionListResponse]{
		More: func(azureclient.PolicyExemptionListResponse) bool { return false },
		Fetcher: func(context.Context, *azureclient.PolicyExemptionListResponse) (azureclient.PolicyExemptionListResponse, error) {
			return azureclient.PolicyExemptionListResponse{Value: c.items}, c.err
		},
	})
}

type storagePolicyTestAssignments struct {
	azureclient.PolicyAssignmentsClient
	t     *testing.T
	items []*armpolicy.Assignment
	err   error
}

func (c *storagePolicyTestAssignments) NewListForResourceGroupPager(rg string, options *armpolicy.AssignmentsClientListForResourceGroupOptions) *runtime.Pager[armpolicy.AssignmentsClientListForResourceGroupResponse] {
	assert.Equal(c.t, "managed-rg", rg)
	require.NotNil(c.t, options)
	assert.Equal(c.t, "atScope()", *options.Filter)
	return newTestPolicyAssignmentListPager(c.items, c.err)
}

type storagePolicyTestSets struct {
	azureclient.PolicySetDefinitionsClient
	definition armpolicy.SetDefinition
	err        error
	scope      string
}

func (c *storagePolicyTestSets) Get(context.Context, string, *armpolicy.SetDefinitionsClientGetOptions) (armpolicy.SetDefinitionsClientGetResponse, error) {
	c.scope = "subscription"
	return armpolicy.SetDefinitionsClientGetResponse{SetDefinition: c.definition}, c.err
}

func (c *storagePolicyTestSets) GetBuiltIn(context.Context, string, *armpolicy.SetDefinitionsClientGetBuiltInOptions) (armpolicy.SetDefinitionsClientGetBuiltInResponse, error) {
	c.scope = "builtin"
	return armpolicy.SetDefinitionsClientGetBuiltInResponse{SetDefinition: c.definition}, c.err
}

func (c *storagePolicyTestSets) GetAtManagementGroup(_ context.Context, mg, _ string, _ *armpolicy.SetDefinitionsClientGetAtManagementGroupOptions) (armpolicy.SetDefinitionsClientGetAtManagementGroupResponse, error) {
	c.scope = mg
	return armpolicy.SetDefinitionsClientGetAtManagementGroupResponse{SetDefinition: c.definition}, c.err
}

type storagePolicyTestDefinitions struct {
	azureclient.PolicyDefinitionsClient
	definition armpolicy.Definition
	err        error
	scope      string
}

func (c *storagePolicyTestDefinitions) Get(context.Context, string, *armpolicy.DefinitionsClientGetOptions) (armpolicy.DefinitionsClientGetResponse, error) {
	c.scope = "subscription"
	return armpolicy.DefinitionsClientGetResponse{Definition: c.definition}, c.err
}

func (c *storagePolicyTestDefinitions) GetBuiltIn(context.Context, string, *armpolicy.DefinitionsClientGetBuiltInOptions) (armpolicy.DefinitionsClientGetBuiltInResponse, error) {
	c.scope = "builtin"
	return armpolicy.DefinitionsClientGetBuiltInResponse{Definition: c.definition}, c.err
}

func (c *storagePolicyTestDefinitions) GetAtManagementGroup(_ context.Context, mg, _ string, _ *armpolicy.DefinitionsClientGetAtManagementGroupOptions) (armpolicy.DefinitionsClientGetAtManagementGroupResponse, error) {
	c.scope = mg
	return armpolicy.DefinitionsClientGetAtManagementGroupResponse{Definition: c.definition}, c.err
}

func TestStoragePolicyRuleEvaluation(t *testing.T) {
	pna := func(operator string, value any) any {
		return map[string]any{"field": storagePublicAccessAlias, operator: value}
	}
	tag := map[string]any{"field": "tags[department]", "equals": "finance"}
	tests := []struct {
		name      string
		condition any
		want      bool
	}{
		{"enabled", pna("equals", "Enabled"), true},
		{"opposite predicate", pna("equals", "Disabled"), false},
		{"notEquals enabled", pna("notEquals", "Enabled"), false},
		{"notEquals disabled", pna("notEquals", "Disabled"), true},
		{"in both states is not access specific", pna("in", []any{"Disabled", "ENABLED"}), false},
		{"in enabled", pna("in", []any{"ENABLED"}), true},
		{"notIn", pna("notIn", []string{"Disabled"}), true},
		{"notIn enabled", pna("notIn", []any{"Enabled"}), false},
		{"exists true", pna("exists", "true"), false},
		{"exists false", pna("exists", false), false},
		{"malformed exists", pna("exists", []any{"true"}), false},
		{"not", map[string]any{"not": pna("equals", "Disabled")}, true},
		{"tag uncertain", map[string]any{"allOf": []any{pna("equals", "Enabled"), tag}}, false},
		{"not unknown", map[string]any{"allOf": []any{pna("equals", "Enabled"), map[string]any{"not": tag}}}, false},
		{"anyOf disabled uncertain", map[string]any{"anyOf": []any{tag, pna("equals", "Enabled")}}, false},
		{"anyOf uncertain", map[string]any{"anyOf": []any{tag, pna("equals", "Disabled")}}, false},
		{"broad type anyOf false alias", map[string]any{"anyOf": []any{map[string]any{"field": "type", "equals": "Microsoft.Storage/storageAccounts"}, pna("equals", "Disabled")}}, false},
		{"broad type anyOf enabled alias", map[string]any{"anyOf": []any{map[string]any{"field": "type", "equals": "Microsoft.Storage/storageAccounts"}, pna("equals", "Enabled")}}, false},
		{"not alias exists false", map[string]any{"not": pna("exists", false)}, false},
		{"tag only", tag, false},
		{"alias only in arbitrary text", map[string]any{"field": "type", "equals": "Microsoft.Storage/storageAccounts", "description": storagePublicAccessAlias}, false},
		{"wrong type", map[string]any{"allOf": []any{pna("equals", "Enabled"), map[string]any{"field": "type", "equals": "Microsoft.Compute/virtualMachines"}}}, false},
		{"location matches", map[string]any{"allOf": []any{pna("equals", "Enabled"), map[string]any{"field": "LOCATION", "equals": "EastUS"}}}, true},
		{"location differs", map[string]any{"allOf": []any{pna("equals", "Enabled"), map[string]any{"field": "location", "equals": "westus"}}}, false},
		{"parameter comparison", pna("equals", "[parameters('access')]"), true},
		{"unknown parameter", pna("notEquals", "[parameters('missing')]"), false},
		{"unknown expression", pna("notEquals", "[concat('Dis', 'abled')]"), false},
		{"unsupported operator", pna("like", "Enabled"), false},
		{"malformed leaf", map[string]any{"field": storagePublicAccessAlias, "equals": "Enabled", "notEquals": "Disabled"}, false},
		{"empty allOf", map[string]any{"allOf": []any{}}, false},
		{"case insensitive field", map[string]any{"field": strings.ToUpper(storagePublicAccessAlias), "equals": "enabled"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := map[string]any{"if": tt.condition, "then": map[string]any{"effect": "[parameters('enforcement')]"}}
			assert.Equal(t, tt.want, storagePolicyRuleDenies(rule, map[string]any{"enforcement": "Deny", "access": "Enabled"}, "eastus"))
		})
	}
	condition := pna("equals", "Enabled")
	for range 40 {
		condition = map[string]any{"not": condition}
	}
	assert.False(t, storagePolicyRuleDenies(map[string]any{"if": condition, "then": map[string]any{"effect": "Deny"}}, nil, "eastus"), "excessive depth must be inconclusive")
	children := make([]any, 600)
	for i := range children {
		children[i] = pna("equals", "Enabled")
	}
	assert.False(t, storagePolicyRuleDenies(map[string]any{"if": map[string]any{"allOf": children}, "then": map[string]any{"effect": "Deny"}}, nil, "eastus"), "excessive nodes must be inconclusive")
}

func TestStoragePolicyLocationNormalization(t *testing.T) {
	for _, tt := range []struct {
		operator string
		value    any
		want     bool
	}{
		{"equals", "East US", true},
		{"notEquals", "East US", false},
		{"in", []any{"West US", "East US"}, true},
		{"notIn", []any{"West US", "East US"}, false},
		{"notEquals", "West US", true},
		{"notIn", []string{"West US"}, true},
	} {
		t.Run(tt.operator+"/"+fmt.Sprint(tt.value), func(t *testing.T) {
			for _, location := range []string{"eastus", "East US", "EAST US"} {
				rule := map[string]any{
					"if": map[string]any{"allOf": []any{
						map[string]any{"field": storagePublicAccessAlias, "equals": "Enabled"},
						map[string]any{"field": "location", tt.operator: "[parameters('region')]"},
					}},
					"then": map[string]any{"effect": "Deny"},
				}
				assert.Equal(t, tt.want, storagePolicyRuleDenies(rule, map[string]any{"region": tt.value}, location), location)
			}
		})
	}
}

func TestStoragePolicyModifyEvaluation(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any, map[string]any)
		want   bool
	}{
		{"disables Enabled", nil, true},
		{"parameter value", func(_ map[string]any, op map[string]any) { op["value"] = "[parameters('access')]" }, true},
		{"enables access", func(_ map[string]any, op map[string]any) { op["value"] = "Enabled" }, false},
		{"unrelated alias", func(_ map[string]any, op map[string]any) {
			op["field"] = "Microsoft.Storage/storageAccounts/allowBlobPublicAccess"
		}, false},
		{"unknown condition", func(_ map[string]any, op map[string]any) {
			op["condition"] = "[greaterOrEquals(requestContext().apiVersion, '2019-04-01')]"
		}, false},
		{"literal condition unsupported", func(_ map[string]any, op map[string]any) { op["condition"] = true }, false},
		{"standalone replace unsupported", func(_ map[string]any, op map[string]any) { op["operation"] = "replace" }, false},
		{"add not replacement", func(_ map[string]any, op map[string]any) { op["operation"] = "add" }, false},
		{"remove unsupported", func(_ map[string]any, op map[string]any) { op["operation"] = "remove" }, false},
		{"unknown value", func(_ map[string]any, op map[string]any) { op["value"] = "[concat('Dis', 'abled')]" }, false},
		{"condition false on Enabled", func(rule map[string]any, _ map[string]any) {
			rule["if"] = map[string]any{"field": storagePublicAccessAlias, "equals": "Disabled"}
		}, false},
		{"condition unknown", func(rule map[string]any, _ map[string]any) {
			rule["if"] = map[string]any{"field": "tags[owner]", "equals": "finance"}
		}, false},
		{"type only can modify access", func(rule map[string]any, _ map[string]any) {
			rule["if"] = map[string]any{"field": "type", "equals": "Microsoft.Storage/storageAccounts"}
		}, true},
		{"Audit ignored", func(rule map[string]any, _ map[string]any) { rule["then"].(map[string]any)["effect"] = "Audit" }, false},
		{"Disabled ignored", func(rule map[string]any, _ map[string]any) { rule["then"].(map[string]any)["effect"] = "Disabled" }, false},
		{"later enabled write ambiguous", func(rule map[string]any, op map[string]any) {
			rule["then"].(map[string]any)["details"].(map[string]any)["operations"] = []any{op, map[string]any{"operation": "addOrReplace", "field": storagePublicAccessAlias, "value": "Enabled"}}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := map[string]any{"operation": "addOrReplace", "field": storagePublicAccessAlias, "value": "Disabled"}
			rule := map[string]any{
				"if":   map[string]any{"field": storagePublicAccessAlias, "equals": "Enabled"},
				"then": map[string]any{"effect": "Modify", "details": map[string]any{"operations": []any{op}}},
			}
			if tt.change != nil {
				tt.change(rule, op)
			}
			assert.Equal(t, tt.want, storagePolicyRuleDenies(rule, map[string]any{"access": "Disabled"}, "eastus"))
		})
	}
}

func TestStoragePolicyExemptions(t *testing.T) {
	const target = "/subscriptions/sub/resourceGroups/managed-rg"
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	assignment := makeTestPolicyAssignment("deny", knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID, nil, nil)
	tests := []struct {
		name   string
		scope  string
		change func(*azureclient.PolicyExemptionProperties)
		ref    string
		want   bool
	}{
		{"active RG", target, nil, "", true},
		{"subscription", "/subscriptions/SUB", nil, "", true},
		{"scope prefix boundary", target + "-other", nil, "", false},
		{"different RG", "/subscriptions/sub/resourceGroups/other", nil, "", false},
		{"different subscription", "/subscriptions/other", nil, "", false},
		{"child resource", target + "/providers/Microsoft.Storage/storageAccounts/other", nil, "", false},
		{"expired", target, func(p *azureclient.PolicyExemptionProperties) { p.ExpiresOn = to.Ptr(now.Add(-time.Second)) }, "", false},
		{"expires exactly now", target, func(p *azureclient.PolicyExemptionProperties) { p.ExpiresOn = &now }, "", false},
		{"future", target, func(p *azureclient.PolicyExemptionProperties) { p.ExpiresOn = to.Ptr(now.Add(time.Second)) }, "", true},
		{"different assignment", target, func(p *azureclient.PolicyExemptionProperties) {
			p.PolicyAssignmentID = to.Ptr(*assignment.ID + "-other")
		}, "", false},
		{"assignment case insensitive", target, func(p *azureclient.PolicyExemptionProperties) {
			p.PolicyAssignmentID = to.Ptr(strings.ToUpper(*assignment.ID))
		}, "", true},
		{"member match", target, func(p *azureclient.PolicyExemptionProperties) {
			p.PolicyDefinitionReferenceIDs = []*string{to.Ptr("registry")}
		}, "registry", true},
		{"other member", target, func(p *azureclient.PolicyExemptionProperties) {
			p.PolicyDefinitionReferenceIDs = []*string{to.Ptr("other")}
		}, "registry", false},
		{"member exemption not whole set", target, func(p *azureclient.PolicyExemptionProperties) {
			p.PolicyDefinitionReferenceIDs = []*string{to.Ptr("registry")}
		}, "", false},
		{"unknown scope", "unrecognized", nil, "", true},
		{"management group ancestry unknown", "/providers/Microsoft.Management/managementGroups/parent", nil, "", true},
		{"selectors conservative", target, func(p *azureclient.PolicyExemptionProperties) {
			p.ResourceSelectors = json.RawMessage(`[{"name":"unknown-selector"}]`)
		}, "", true},
		{"unknown scope validation conservative", target, func(p *azureclient.PolicyExemptionProperties) { p.AssignmentScopeValidation = to.Ptr("future-value") }, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &azureclient.PolicyExemptionProperties{PolicyAssignmentID: assignment.ID}
			if tt.change != nil {
				tt.change(p)
			}
			exemption := &azureclient.PolicyExemption{ID: to.Ptr(tt.scope + "/providers/Microsoft.Authorization/policyExemptions/exempt"), Properties: p}
			assert.Equal(t, tt.want, policyAssignmentExempt([]*azureclient.PolicyExemption{nil, exemption}, assignment, tt.ref, target, now))
		})
	}
}

func TestStoragePolicyValidationRegressions(t *testing.T) {
	const sub = "00000000-0000-0000-0000-000000000000"
	const target = "/subscriptions/" + sub + "/resourceGroups/managed-rg"
	const setID = "/subscriptions/" + sub + "/providers/Microsoft.Authorization/policySetDefinitions/initiative"
	const customID = "/subscriptions/" + sub + "/providers/Microsoft.Authorization/policyDefinitions/custom"
	type fixture struct {
		cluster            *coreapi.Cluster
		subscription       *coreapi.Subscription
		assignment         *armpolicy.Assignment
		blockingAssignment *armpolicy.Assignment
		assignments        *storagePolicyTestAssignments
		exemptions         *storagePolicyTestExemptions
		sets               *storagePolicyTestSets
		definitions        *storagePolicyTestDefinitions
		builder            *storagePolicyTestBuilder
	}
	exempt := func(f *fixture, reference string) {
		p := &azureclient.PolicyExemptionProperties{PolicyAssignmentID: f.assignment.ID}
		if reference != "" {
			p.PolicyDefinitionReferenceIDs = []*string{to.Ptr(reference)}
		}
		f.exemptions.items = []*azureclient.PolicyExemption{{ID: to.Ptr(target + "/providers/Microsoft.Authorization/policyExemptions/allowed"), Properties: p}}
	}
	modify := func(f *fixture) {
		f.assignment.Properties.PolicyDefinitionID = to.Ptr(customID)
		f.definitions.definition.Properties.PolicyRule.(map[string]any)["then"] = map[string]any{
			"effect": "Modify",
			"details": map[string]any{"operations": []any{map[string]any{
				"operation": "addOrReplace", "field": storagePublicAccessAlias, "value": "[parameters('access')]",
			}}},
		}
		f.definitions.definition.Properties.Parameters["access"] = &armpolicy.ParameterDefinitionsValue{DefaultValue: "Disabled"}
	}
	tests := []struct {
		name  string
		setup func(*fixture)
		want  OutcomeType
	}{
		{"initiative mapped default Deny", nil, OutcomeTypeFailed},
		{"no assignments needs no exemption API", func(f *fixture) {
			f.assignments.items = nil
			f.builder.fail = "exemptions"
		}, OutcomeTypePassed},
		{"Audit needs no exemption API", func(f *fixture) {
			f.assignment.Properties.Parameters = map[string]*armpolicy.ParameterValuesValue{"mode": {Value: "Audit"}}
			f.builder.fail = "exemptions"
		}, OutcomeTypePassed},
		{"excluded needs no exemption API", func(f *fixture) {
			f.assignment.Properties.NotScopes = []*string{to.Ptr(target)}
			f.builder.fail = "exemptions"
		}, OutcomeTypePassed},
		{"unenforced needs no exemption API", func(f *fixture) {
			f.assignment.Properties.EnforcementMode = to.Ptr(armpolicy.EnforcementModeDoNotEnforce)
			f.builder.fail = "exemptions"
		}, OutcomeTypePassed},
		{"nonblocking custom needs no exemption API", func(f *fixture) {
			f.assignment.Properties.PolicyDefinitionID = to.Ptr(customID)
			f.definitions.definition.Properties.PolicyRule.(map[string]any)["if"] = map[string]any{"field": storagePublicAccessAlias, "exists": "true"}
			f.builder.fail = "exemptions"
		}, OutcomeTypePassed},
		{"Modify disables registry access", modify, OutcomeTypeFailed},
		{"Modify active exemption", func(f *fixture) { modify(f); exempt(f, "") }, OutcomeTypePassed},
		{"Modify exemption failure unknown", func(f *fixture) { modify(f); f.exemptions.err = errors.New("page error") }, OutcomeTypeUnknown},
		{"Modify assigned Enabled overrides Disabled default", func(f *fixture) {
			modify(f)
			f.assignment.Properties.Parameters = map[string]*armpolicy.ParameterValuesValue{"access": {Value: "Enabled"}}
			f.builder.fail = "exemptions"
		}, OutcomeTypePassed},
		{"initiative mapped Modify Disabled", func(f *fixture) {
			modify(f)
			f.assignment.Properties.PolicyDefinitionID = to.Ptr(setID)
			member := f.sets.definition.Properties.PolicyDefinitions[0]
			member.PolicyDefinitionID = to.Ptr(customID)
			member.Parameters = map[string]*armpolicy.ParameterValuesValue{"access": {Value: "[parameters('access')]"}}
			f.sets.definition.Properties.Parameters["access"] = &armpolicy.ParameterDefinitionsValue{DefaultValue: "Disabled"}
		}, OutcomeTypeFailed},
		{"initiative mapped assigned Audit", func(f *fixture) {
			f.assignment.Properties.Parameters = map[string]*armpolicy.ParameterValuesValue{"mode": {Value: "Audit"}}
		}, OutcomeTypePassed},
		{"initiative assigned Deny overrides Audit default", func(f *fixture) {
			f.sets.definition.Properties.Parameters["mode"].DefaultValue = "Audit"
			f.assignment.Properties.Parameters = map[string]*armpolicy.ParameterValuesValue{"mode": {Value: "Deny"}}
		}, OutcomeTypeFailed},
		{"initiative builtin default Audit", func(f *fixture) { f.sets.definition.Properties.PolicyDefinitions[0].Parameters = nil }, OutcomeTypePassed},
		{"initiative literal Deny", func(f *fixture) {
			f.sets.definition.Properties.PolicyDefinitions[0].Parameters["effect"].Value = "Deny"
		}, OutcomeTypeFailed},
		{"unknown mapping never falls back to Deny", func(f *fixture) {
			f.sets.definition.Properties.PolicyDefinitions[0].PolicyDefinitionID = to.Ptr(customID)
			f.sets.definition.Properties.PolicyDefinitions[0].Parameters["effect"].Value = "[if(true(), 'Audit', 'Deny')]"
		}, OutcomeTypePassed},
		{"missing mapping parameter never falls back to Deny", func(f *fixture) {
			f.sets.definition.Properties.PolicyDefinitions[0].PolicyDefinitionID = to.Ptr(customID)
			f.sets.definition.Properties.PolicyDefinitions[0].Parameters["effect"].Value = "[parameters('missing')]"
		}, OutcomeTypePassed},
		{"initiative assignment expression remains unknown", func(f *fixture) {
			f.assignment.Properties.Parameters = map[string]*armpolicy.ParameterValuesValue{"mode": {Value: "[concat('De', 'ny')]"}}
		}, OutcomeTypePassed},
		{"active exemption", func(f *fixture) { exempt(f, "") }, OutcomeTypePassed},
		{"expired exemption", func(f *fixture) {
			exempt(f, "")
			f.exemptions.items[0].Properties.ExpiresOn = to.Ptr(time.Now().Add(-time.Hour))
		}, OutcomeTypeFailed},
		{"different exemption scope", func(f *fixture) {
			exempt(f, "")
			f.exemptions.items[0].ID = to.Ptr("/subscriptions/other/providers/Microsoft.Authorization/policyExemptions/allowed")
		}, OutcomeTypeFailed},
		{"different exemption assignment", func(f *fixture) {
			exempt(f, "")
			f.exemptions.items[0].Properties.PolicyAssignmentID = to.Ptr(*f.assignment.ID + "-other")
		}, OutcomeTypeFailed},
		{"reference exemption suppresses member", func(f *fixture) { exempt(f, "registry") }, OutcomeTypePassed},
		{"reference exemption leaves other member enforced", func(f *fixture) {
			exempt(f, "registry")
			member := *f.sets.definition.Properties.PolicyDefinitions[0]
			member.PolicyDefinitionReferenceID = to.Ptr("other")
			f.sets.definition.Properties.PolicyDefinitions = append(f.sets.definition.Properties.PolicyDefinitions, &member)
		}, OutcomeTypeFailed},
		{"other reference exemption does not suppress member", func(f *fixture) { exempt(f, "other") }, OutcomeTypeFailed},
		{"cached exemptions checked per assignment", func(f *fixture) {
			exempt(f, "")
			other := makeTestPolicyAssignment("other", knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID, nil, map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}})
			f.assignments.items = append(f.assignments.items, other)
			f.blockingAssignment = other
		}, OutcomeTypeFailed},
		{"direct builtin Deny", func(f *fixture) {
			f.assignment.Properties.PolicyDefinitionID = to.Ptr(knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID)
			f.assignment.Properties.Parameters = map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "Deny"}}
		}, OutcomeTypeFailed},
		{"custom default Deny", func(f *fixture) { f.assignment.Properties.PolicyDefinitionID = to.Ptr(customID) }, OutcomeTypeFailed},
		{"opposite custom predicate", func(f *fixture) {
			f.assignment.Properties.PolicyDefinitionID = to.Ptr(customID)
			f.definitions.definition.Properties.PolicyRule.(map[string]any)["if"] = map[string]any{"field": storagePublicAccessAlias, "equals": "Disabled"}
		}, OutcomeTypePassed},
		{"custom tag condition inconclusive", func(f *fixture) {
			f.assignment.Properties.PolicyDefinitionID = to.Ptr(customID)
			f.definitions.definition.Properties.PolicyRule.(map[string]any)["if"] = map[string]any{"allOf": []any{map[string]any{"field": storagePublicAccessAlias, "equals": "Enabled"}, map[string]any{"field": "tags[owner]", "exists": "false"}}}
		}, OutcomeTypePassed},
		{"management group NotScopes ancestry unknown", func(f *fixture) {
			f.assignment.Properties.NotScopes = []*string{to.Ptr("/providers/Microsoft.Management/managementGroups/parent")}
		}, OutcomeTypePassed},
		{"initiative member effect override", func(f *fixture) {
			f.assignment.Properties.Overrides = []*armpolicy.Override{{Kind: to.Ptr(armpolicy.OverrideKindPolicyEffect), Value: to.Ptr("Audit"), Selectors: []*armpolicy.Selector{{Kind: to.Ptr(armpolicy.SelectorKindPolicyDefinitionReferenceID), In: []*string{to.Ptr("registry")}}}}}
		}, OutcomeTypePassed},
		{"assignment page failure", func(f *fixture) { f.assignments.err = errors.New("page error") }, OutcomeTypeUnknown},
		{"exemption page failure", func(f *fixture) { f.exemptions.err = errors.New("page error") }, OutcomeTypeUnknown},
		{"definition fetch failure", func(f *fixture) {
			f.assignment.Properties.PolicyDefinitionID = to.Ptr(customID)
			f.definitions.err = errors.New("definition error")
		}, OutcomeTypeUnknown},
		{"initiative fetch failure", func(f *fixture) { f.sets.err = errors.New("initiative error") }, OutcomeTypeUnknown},
		{"assignments client failure", func(f *fixture) { f.builder.fail = "assignments" }, OutcomeTypeUnknown},
		{"exemptions client failure", func(f *fixture) { f.builder.fail = "exemptions" }, OutcomeTypeUnknown},
		{"definitions client failure", func(f *fixture) { f.builder.fail = "definitions" }, OutcomeTypeUnknown},
		{"initiatives client failure", func(f *fixture) { f.builder.fail = "sets" }, OutcomeTypeUnknown},
		{"missing tenant", func(f *fixture) { f.subscription.Properties = nil }, OutcomeTypeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assignment := makeTestPolicyAssignment("deny-registry", setID, nil, nil)
			f := &fixture{
				cluster: &coreapi.Cluster{
					TrackedResource:    coreapi.TrackedResource{Resource: coreapi.Resource{ID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + sub + "/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))}, Location: "eastus"},
					CustomerProperties: coreapi.ClusterCustomerProperties{ClusterImageRegistry: coreapi.ClusterImageRegistryProfile{State: metadataapi.ClusterImageRegistryStateEnabled}, Platform: coreapi.CustomerPlatformProfile{ManagedResourceGroup: "managed-rg"}},
				},
				subscription:       &coreapi.Subscription{Properties: &coreapi.SubscriptionProperties{TenantId: to.Ptr("tenant")}},
				assignment:         assignment,
				blockingAssignment: assignment,
				assignments:        &storagePolicyTestAssignments{t: t, items: []*armpolicy.Assignment{assignment}},
				exemptions:         &storagePolicyTestExemptions{},
				sets: &storagePolicyTestSets{definition: armpolicy.SetDefinition{Properties: &armpolicy.SetDefinitionProperties{
					Parameters:        map[string]*armpolicy.ParameterDefinitionsValue{"mode": {DefaultValue: "Deny"}},
					PolicyDefinitions: []*armpolicy.DefinitionReference{{PolicyDefinitionID: to.Ptr(knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID), PolicyDefinitionReferenceID: to.Ptr("registry"), Parameters: map[string]*armpolicy.ParameterValuesValue{"effect": {Value: "[parameters('mode')]"}}}},
				}}},
				definitions: &storagePolicyTestDefinitions{definition: armpolicy.Definition{Properties: &armpolicy.DefinitionProperties{
					Parameters: map[string]*armpolicy.ParameterDefinitionsValue{"effect": {DefaultValue: "Deny"}},
					PolicyRule: map[string]any{"if": map[string]any{"field": storagePublicAccessAlias, "equals": "Enabled"}, "then": map[string]any{"effect": "[parameters('effect')]"}},
				}}},
			}
			f.builder = &storagePolicyTestBuilder{assignments: f.assignments, definitions: f.definitions, exemptions: f.exemptions, sets: f.sets}
			if tt.setup != nil {
				tt.setup(f)
			}
			result := NewStorageAccountNetworkAccessValidation(f.builder).Validate(context.Background(), f.subscription, f.cluster)
			assert.Equal(t, tt.want, result.Outcome.Type, result.InternalMessage())
			assert.LessOrEqual(t, f.exemptions.calls, 1, "exemptions must be listed once, not per assignment/member")
			assert.LessOrEqual(t, f.builder.exemptionCalls, 1, "exemption client must be created at most once")
			if tt.want == OutcomeTypePassed && f.builder.fail == "exemptions" {
				assert.Zero(t, f.builder.exemptionCalls, "nonblocking policies must not require exemption access")
			}
			if tt.want == OutcomeTypeFailed {
				assert.Equal(t, 1, f.exemptions.calls, "confirmed conflicts require exemption lookup")
				assert.Contains(t, result.InternalMessage(), *f.blockingAssignment.ID)
				assert.Contains(t, result.InternalMessage(), *f.blockingAssignment.Name)
				assert.Contains(t, result.InternalMessage(), "conflicts with")
				assert.NotContains(t, result.InternalMessage(), "denies creation")
				assert.Contains(t, result.InternalMessage(), target)
				assert.Contains(t, result.InternalMessage(), "Enabled")
				assert.Contains(t, result.InternalMessage(), "properties.clusterImageRegistry.state")
			}
		})
	}
}

func TestStoragePolicyCreationOnlySkipsBeforeCredentials(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "registry disabled", true: "existing cluster"}[existing], func(t *testing.T) {
			cluster := &coreapi.Cluster{}
			if existing {
				cluster.CustomerProperties.ClusterImageRegistry.State = metadataapi.ClusterImageRegistryStateEnabled
				cluster.ServiceProviderProperties.ClusterServiceID = new(metadataapi.InternalID)
			} else {
				cluster.CustomerProperties.ClusterImageRegistry.State = metadataapi.ClusterImageRegistryStateDisabled
			}
			// nil builder and missing tenant/cluster ID would panic if accessed.
			result := NewStorageAccountNetworkAccessValidation(nil).Validate(context.Background(), &coreapi.Subscription{}, cluster)
			assert.Equal(t, OutcomeTypeSkipped, result.Outcome.Type)
		})
	}
}

func TestStoragePolicyDefinitionScopes(t *testing.T) {
	for _, tt := range []struct{ prefix, scope string }{
		{"", "builtin"},
		{"/subscriptions/sub", "subscription"},
		{"/providers/Microsoft.Management/managementGroups/parent", "parent"},
	} {
		t.Run(tt.scope, func(t *testing.T) {
			id := tt.prefix + "/providers/Microsoft.Authorization/policyDefinitions/custom"
			client := &storagePolicyTestDefinitions{}
			_, err := storagePolicyDenies(context.Background(), client, id, "sub", nil, nil, "eastus")
			require.NoError(t, err)
			assert.Equal(t, tt.scope, client.scope)
			kind, scope, name := storagePolicyDefinitionID(tt.prefix + "/providers/Microsoft.Authorization/policySetDefinitions/initiative")
			assert.Equal(t, "policysetdefinitions", kind)
			sets := &storagePolicyTestSets{}
			_, err = getStoragePolicySet(context.Background(), sets, scope, name, "sub")
			require.NoError(t, err)
			assert.Equal(t, tt.scope, sets.scope)
		})
	}
}
