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
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

const storagePublicAccessAlias = "Microsoft.Storage/storageAccounts/publicNetworkAccess"

type storagePolicyTruth uint8

const (
	storagePolicyUnknown storagePolicyTruth = iota
	storagePolicyFalse
	storagePolicyTrue
)

var storagePolicyParameterReference = regexp.MustCompile(`(?i)^\[parameters\('([^']+)'\)\]$`)

// Only literals and a single parameter reference are supported, not ARM template
// evaluation. nil represents unresolved values and MUST shadow definition defaults.
func storagePolicyValue(value any, parameters map[string]any) any {
	if text, ok := value.(string); ok {
		if matches := storagePolicyParameterReference.FindStringSubmatch(strings.TrimSpace(text)); matches != nil {
			return parameters[strings.ToLower(matches[1])]
		}
		if strings.HasPrefix(strings.TrimSpace(text), "[") {
			return nil
		}
	}
	return value
}

func storagePolicyParameters(defaults map[string]*armpolicy.ParameterDefinitionsValue, assigned map[string]*armpolicy.ParameterValuesValue, parent map[string]any) map[string]any {
	values := map[string]any{}
	for key, value := range defaults {
		if value != nil {
			values[strings.ToLower(key)] = storagePolicyValue(value.DefaultValue, nil)
		}
	}
	for key, value := range assigned {
		values[strings.ToLower(key)] = nil
		if value != nil {
			values[strings.ToLower(key)] = storagePolicyValue(value.Value, parent)
		}
	}
	return values
}

func storagePolicyDenies(ctx context.Context, client azureclient.PolicyDefinitionsClient, id, sub string, assigned map[string]*armpolicy.ParameterValuesValue, parent map[string]any, location string) (bool, error) {
	if strings.EqualFold(id, knownStorageAccountPublicNetworkAccessDenyPolicyDefinitionID) {
		parameters := storagePolicyParameters(map[string]*armpolicy.ParameterDefinitionsValue{"effect": {DefaultValue: "Audit"}}, assigned, parent)
		effect, _ := parameters["effect"].(string)
		return strings.EqualFold(effect, "Deny"), nil
	}
	kind, scope, name := storagePolicyDefinitionID(id)
	if kind != "policydefinitions" {
		return false, nil
	}
	var definition armpolicy.Definition
	var err error
	switch {
	case strings.HasPrefix(scope, "mg:"):
		var response armpolicy.DefinitionsClientGetAtManagementGroupResponse
		response, err = client.GetAtManagementGroup(ctx, strings.TrimPrefix(scope, "mg:"), name, nil)
		definition = response.Definition
	case scope == "":
		var response armpolicy.DefinitionsClientGetBuiltInResponse
		response, err = client.GetBuiltIn(ctx, name, nil)
		definition = response.Definition
	case strings.EqualFold(scope, sub):
		var response armpolicy.DefinitionsClientGetResponse
		response, err = client.Get(ctx, name, nil)
		definition = response.Definition
	default:
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get policy definition %q: %w", id, err)
	}
	if definition.Properties == nil {
		return false, nil
	}
	parameters := storagePolicyParameters(definition.Properties.Parameters, assigned, parent)
	return storagePolicyRuleDenies(definition.Properties.PolicyRule, parameters, location), nil
}

// The historical Denies name includes narrow Modify-to-Disabled conflicts. This
// is a bounded diagnostic, not the full Azure Policy engine or a prediction of
// API-version-specific alias modifiability.
func storagePolicyRuleDenies(rule any, parameters map[string]any, location string) bool {
	ruleMap, ok := rule.(map[string]any)
	if !ok {
		return false
	}
	then, ok := ruleMap["then"].(map[string]any)
	if !ok {
		return false
	}
	effect, _ := storagePolicyValue(then["effect"], parameters).(string)
	if !strings.EqualFold(effect, "Deny") && !strings.EqualFold(effect, "Modify") {
		return false
	}
	budget := 512
	result, _ := storagePolicyCondition(ruleMap["if"], parameters, location, "Enabled", 0, &budget)
	if result != storagePolicyTrue {
		return false
	}
	if strings.EqualFold(effect, "Modify") {
		return storagePolicyModifiesAccess(then["details"], parameters)
	}
	// A broad Deny that also applies to Disabled is not specifically a public
	// access conflict. Merely mentioning the alias (e.g. exists) is insufficient.
	budget = 512
	disabled, _ := storagePolicyCondition(ruleMap["if"], parameters, location, "Disabled", 0, &budget)
	return disabled == storagePolicyFalse
}

func storagePolicyModifiesAccess(details any, parameters map[string]any) bool {
	node, ok := details.(map[string]any)
	if !ok {
		return false
	}
	operations, ok := node["operations"].([]any)
	if !ok || len(operations) == 0 || len(operations) > 512 {
		return false
	}
	modifies := false
	for _, raw := range operations {
		op, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		field, _ := op["field"].(string)
		if !strings.EqualFold(field, storagePublicAccessAlias) {
			continue
		}
		// Operation conditions are expressions, deliberately not interpreted.
		// Reject ambiguous writes to this alias, including a later Enabled write.
		if _, conditional := op["condition"]; conditional {
			return false
		}
		operation, _ := op["operation"].(string)
		value, _ := storagePolicyValue(op["value"], parameters).(string)
		// Azure supports addOrReplace/add/remove, not standalone replace.
		// Only addOrReplace confirms replacement of the existing Enabled value.
		if !strings.EqualFold(operation, "addOrReplace") || !strings.EqualFold(value, "Disabled") {
			return false
		}
		modifies = true
	}
	return modifies
}

// Bounded three-valued evaluation of the hypothetical registry SA. Unknown
// fields (including tags/name), operators and malformed shapes remain unknown,
// including under not/notEquals. No marshaled JSON or arbitrary text is scanned.
func storagePolicyCondition(condition any, parameters map[string]any, location, publicAccess string, depth int, budget *int) (storagePolicyTruth, bool) {
	*budget--
	if depth > 32 || *budget < 0 {
		return storagePolicyUnknown, false
	}
	node, ok := condition.(map[string]any)
	if !ok {
		return storagePolicyUnknown, false
	}
	if len(node) == 1 {
		if child, ok := node["not"]; ok {
			value, alias := storagePolicyCondition(child, parameters, location, publicAccess, depth+1, budget)
			switch value {
			case storagePolicyTrue:
				value = storagePolicyFalse
			case storagePolicyFalse:
				value = storagePolicyTrue
			}
			return value, alias
		}
		for _, operator := range []string{"allOf", "anyOf"} {
			if children, ok := node[operator].([]any); ok && len(children) > 0 {
				if len(children) > *budget {
					return storagePolicyUnknown, false
				}
				result, alias := storagePolicyTrue, false
				if operator == "anyOf" {
					result = storagePolicyFalse
				}
				for _, child := range children {
					value, childAlias := storagePolicyCondition(child, parameters, location, publicAccess, depth+1, budget)
					alias = alias || childAlias
					if operator == "allOf" {
						if value == storagePolicyFalse || result == storagePolicyFalse {
							result = storagePolicyFalse
						} else if value == storagePolicyUnknown {
							result = storagePolicyUnknown
						}
					} else if value == storagePolicyTrue || result == storagePolicyTrue {
						result = storagePolicyTrue
					} else if value == storagePolicyUnknown {
						result = storagePolicyUnknown
					}
				}
				return result, alias
			}
		}
	}
	field, ok := node["field"].(string)
	if !ok || len(node) != 2 {
		return storagePolicyUnknown, false
	}
	alias := strings.EqualFold(field, storagePublicAccessAlias)
	// Azure location comparisons ignore spaces and case: East US == eastus.
	// Apply the same normalization to both operands for equals/in and negations.
	normalize := func(value string) string {
		if strings.EqualFold(field, "location") {
			return strings.ReplaceAll(value, " ", "")
		}
		return value
	}
	var actual string
	switch {
	case alias:
		actual = publicAccess
	case strings.EqualFold(field, "type"):
		actual = "Microsoft.Storage/storageAccounts"
	case strings.EqualFold(field, "location") && location != "":
		actual = location
	default:
		return storagePolicyUnknown, alias
	}
	actual = normalize(actual)
	for operator, raw := range node {
		if operator == "field" {
			continue
		}
		value := storagePolicyValue(raw, parameters)
		matches := false
		switch operator {
		case "equals", "notEquals":
			text, ok := value.(string)
			if !ok {
				return storagePolicyUnknown, alias
			}
			matches = strings.EqualFold(actual, normalize(text))
			if operator == "notEquals" {
				matches = !matches
			}
		case "in", "notIn":
			items, ok := value.([]any)
			if textItems, isStrings := value.([]string); isStrings {
				items, ok = make([]any, len(textItems)), true
				for i, item := range textItems {
					items[i] = item
				}
			}
			if !ok || len(items) > 512 {
				return storagePolicyUnknown, alias
			}
			for _, item := range items {
				text, ok := storagePolicyValue(item, nil).(string)
				if !ok {
					return storagePolicyUnknown, alias
				}
				matches = matches || strings.EqualFold(actual, normalize(text))
			}
			if operator == "notIn" {
				matches = !matches
			}
		case "exists":
			switch value {
			case "true", true:
				matches = true
			case "false", false:
				matches = false
			default:
				return storagePolicyUnknown, alias
			}
		default:
			return storagePolicyUnknown, alias
		}
		if matches {
			return storagePolicyTrue, alias
		}
		return storagePolicyFalse, alias
	}
	return storagePolicyUnknown, alias
}
