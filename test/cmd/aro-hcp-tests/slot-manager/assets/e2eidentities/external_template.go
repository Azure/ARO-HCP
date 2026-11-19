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

package e2eidentities

import (
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

// Compare the compiled Bicep tree, not merely its top-level resource IDs: a
// nested deployment can otherwise remove/recreate identities without changing
// the outer resource-group inventory. Existing expressions and properties must
// remain identical. The supported additive path is the template's named UAMI
// definitions, with their literal name bindings, schema, and output entries.
func preflightAdditiveIdentityTemplate(previous, next any) error {
	if reflect.DeepEqual(previous, next) {
		return nil
	}
	template, ok := next.(map[string]interface{})
	if !ok {
		return fmt.Errorf("expected a compiled ARM template object")
	}
	variables, _ := template["variables"].(map[string]interface{})
	bindings, _ := variables["identities"].(map[string]interface{})
	if len(bindings) == 0 {
		return fmt.Errorf("identity name bindings are unavailable")
	}
	names := sets.New[string]()
	for key, value := range bindings {
		name, ok := value.(string)
		if !ok || strings.TrimSpace(name) == "" || strings.HasPrefix(name, "[") || names.Has(strings.ToLower(name)) {
			return fmt.Errorf("identity binding %q is not a unique literal name", key)
		}
		names.Insert(strings.ToLower(name))
	}
	return compareAdditiveIdentityTree(previous, next, "template", bindings)
}

func compareAdditiveIdentityTree(previous, next any, path string, bindings map[string]interface{}) error {
	if reflect.DeepEqual(previous, next) {
		return nil
	}
	switch previous := previous.(type) {
	case map[string]interface{}:
		current, ok := next.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s changed shape", path)
		}
		for key, value := range previous {
			candidate, found := current[key]
			if !found {
				return fmt.Errorf("%s.%s was removed", path, key)
			}
			if err := compareAdditiveIdentityTree(value, candidate, path+"."+key, bindings); err != nil {
				return err
			}
		}
		for key, value := range current {
			if _, found := previous[key]; found {
				continue
			}
			if !safeIdentityTemplateAddition(path, key, value, current, bindings) {
				return fmt.Errorf("%s.%s is not a proven additive identity definition", path, key)
			}
		}
		return nil
	case []interface{}:
		current, ok := next.([]interface{})
		if !ok || len(previous) != len(current) {
			return fmt.Errorf("%s resource/expression collection changed", path)
		}
		for index, value := range previous {
			if err := compareAdditiveIdentityTree(value, current[index], fmt.Sprintf("%s[%d]", path, index), bindings); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s changed an existing value or expression", path)
	}
}

func safeIdentityTemplateAddition(path, key string, value any, siblings, bindings map[string]interface{}) bool {
	switch {
	case path == "template.variables.identities":
		// All bindings were checked for literal, unique values above.
		return true
	case strings.HasSuffix(path, ".properties.template.definitions.ManagedIdentities.properties"):
		_, bound := bindings[key]
		return bound && reflect.DeepEqual(value, map[string]interface{}{"type": "string"})
	case strings.HasSuffix(path, ".properties.template.outputs.msiIdentities.value"):
		_, bound := bindings[key]
		return bound && value == "[parameters('identities')."+key+"]"
	case strings.HasSuffix(path, ".properties.template.resources"):
		resource, ok := value.(map[string]interface{})
		if !ok || len(resource) != 4 || resource["type"] != "Microsoft.ManagedIdentity/userAssignedIdentities" || resource["location"] != "[resourceGroup().location]" {
			return false
		}
		apiVersion, ok := resource["apiVersion"].(string)
		if !ok || apiVersion == "" {
			return false
		}
		name, ok := resource["name"].(string)
		if !ok || !strings.HasPrefix(name, "[parameters('identities').") || !strings.HasSuffix(name, "]") {
			return false
		}
		binding := strings.TrimSuffix(strings.TrimPrefix(name, "[parameters('identities')."), "]")
		if _, found := bindings[binding]; !found {
			return false
		}
		matchingNames := 0
		knownVersion := false
		for siblingKey, sibling := range siblings {
			other, ok := sibling.(map[string]interface{})
			if !ok {
				return false
			}
			if other["name"] == name {
				matchingNames++
			}
			if siblingKey != key && other["type"] == "Microsoft.ManagedIdentity/userAssignedIdentities" && other["apiVersion"] == apiVersion {
				knownVersion = true
			}
		}
		return matchingNames == 1 && knownVersion
	default:
		return false
	}
}
