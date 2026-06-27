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

package policy

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// RGSelectionReason explains why a resource group was selected or skipped.
type RGSelectionReason struct {
	Code string           `json:"code,omitempty"`
	Rule *RGSelectionRule `json:"rule,omitempty"`
}

// RGSelectionRule captures the matched rule metadata.
type RGSelectionRule struct {
	Index  int               `json:"index"`
	Name   string            `json:"name,omitempty"`
	Action RGDiscoveryAction `json:"action,omitempty"`
	Result string            `json:"result,omitempty"`
}

// String returns a compact machine-readable reason label.
func (r RGSelectionReason) String() string {
	if r.Rule == nil {
		return strings.TrimSpace(r.Code)
	}
	if strings.TrimSpace(r.Rule.Result) == "" {
		return fmt.Sprintf("rule[%d]", r.Rule.Index)
	}
	return fmt.Sprintf("rule[%d]-%s", r.Rule.Index, strings.TrimSpace(r.Rule.Result))
}

// SourceDescription returns a human-readable source summary for logs.
func (r RGSelectionReason) SourceDescription() string {
	if r.Rule == nil {
		normalizedCode := strings.TrimSpace(r.Code)
		if normalizedCode == "" {
			return "matched policy (unknown rule)"
		}
		return fmt.Sprintf("matched policy (%s)", normalizedCode)
	}

	ruleName := strings.TrimSpace(r.Rule.Name)
	ruleResult := strings.TrimSpace(r.Rule.Result)
	if ruleName == "" {
		if ruleResult == "" {
			return fmt.Sprintf("matched policy rule[%d]", r.Rule.Index)
		}
		return fmt.Sprintf("matched policy rule[%d] (%s)", r.Rule.Index, ruleResult)
	}

	if ruleResult == "" {
		return fmt.Sprintf("matched policy rule %q", ruleName)
	}
	return fmt.Sprintf("matched policy rule %q (%s)", ruleName, ruleResult)
}

func newRGSelectionReason(code string) RGSelectionReason {
	return RGSelectionReason{Code: strings.TrimSpace(code)}
}

func newRGSelectionRuleReason(ruleIndex int, rule RGDiscoveryRule, ruleResult string) RGSelectionReason {
	return RGSelectionReason{
		Code: "rule-match",
		Rule: &RGSelectionRule{
			Index:  ruleIndex,
			Name:   strings.TrimSpace(rule.Name),
			Action: rule.Action,
			Result: strings.TrimSpace(ruleResult),
		},
	}
}

// SelectsResourceGroup evaluates discovery rules for a resource group.
// knownResourceGroups is the set of all RG names in the subscription
// used by the managedByAlive condition to detect orphaned managed RGs.
func (p *RGDiscoveryPolicy) SelectsResourceGroup(
	rg *armresources.ResourceGroup,
	excludedResourceGroups sets.Set[string],
	knownResourceGroups sets.Set[string],
	now time.Time,
) (bool, RGSelectionReason) {
	if p == nil {
		return false, newRGSelectionReason("policy-disabled")
	}
	if rg == nil || rg.Name == nil {
		return false, newRGSelectionReason("invalid-resource-group")
	}
	if now.IsZero() {
		return false, newRGSelectionReason("invalid-reference-time")
	}

	rgName := strings.TrimSpace(*rg.Name)
	if rgName == "" {
		return false, newRGSelectionReason("missing-name")
	}
	if excludedResourceGroups.Has(strings.ToLower(rgName)) {
		return false, newRGSelectionReason("excluded")
	}

	for idx, rule := range p.Rules {
		if !rule.Match.MatchesResourceGroup(rgName) {
			continue
		}
		if !rule.Conditions.MatchesResourceGroup(rg, knownResourceGroups) {
			continue
		}

		switch rule.Action {
		case RGDiscoveryActionSkip:
			return false, newRGSelectionRuleReason(idx, rule, "skip")
		case RGDiscoveryActionDelete:
			createdAt, ok := parseCreatedAt(rg.Tags)
			if !ok {
				return false, newRGSelectionRuleReason(idx, rule, "missing-createdAt")
			}

			age := now.Sub(createdAt)
			if age <= 0 {
				return false, newRGSelectionRuleReason(idx, rule, "future-createdAt")
			}
			if age > rule.OlderThan {
				return true, newRGSelectionRuleReason(idx, rule, "expired")
			}
			return false, newRGSelectionRuleReason(idx, rule, "young")
		}
	}
	return false, newRGSelectionReason("no-rule-match")
}

// MatchesResourceGroup evaluates match criteria against a resource-group name.
func (m RGDiscoveryMatch) MatchesResourceGroup(resourceGroupName string) bool {
	if m.Any {
		return true
	}
	if m.NameRegex != nil {
		return m.NameRegex.MatchString(resourceGroupName)
	}
	return strings.HasPrefix(resourceGroupName, m.NamePrefix)
}

// MatchesResourceGroup evaluates additional condition predicates.
func (c RGDiscoveryConditions) MatchesResourceGroup(rg *armresources.ResourceGroup, knownResourceGroups sets.Set[string]) bool {
	if rg == nil {
		return false
	}

	if c.ManagedByAlive != nil {
		if rg.ManagedBy == nil {
			return false
		}
		parsed, err := azcorearm.ParseResourceID(*rg.ManagedBy)
		alive := err == nil && knownResourceGroups.Has(strings.ToLower(parsed.ResourceGroupName))
		if alive != *c.ManagedByAlive {
			return false
		}
	}
	if !tagsEqMatch(rg.Tags, c.TagsEq) {
		return false
	}
	if !tagsNotEqMatch(rg.Tags, c.TagsNotEq) {
		return false
	}
	return true
}

func parseCreatedAt(tags map[string]*string) (time.Time, bool) {
	raw, ok := lookupTag(tags, "createdAt")
	if !ok {
		return time.Time{}, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return parsed.UTC(), true
	}
	parsed, err = time.Parse(time.RFC3339, raw)
	if err == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

func tagsEqMatch(actualTags map[string]*string, expected map[string]string) bool {
	if len(expected) == 0 {
		return true
	}
	for key, value := range expected {
		actualValue, ok := lookupTag(actualTags, key)
		if !ok {
			return false
		}
		if !strings.EqualFold(strings.TrimSpace(actualValue), strings.TrimSpace(value)) {
			return false
		}
	}
	return true
}

func tagsNotEqMatch(actualTags map[string]*string, expected map[string]string) bool {
	if len(expected) == 0 {
		return true
	}
	for key, value := range expected {
		actualValue, ok := lookupTag(actualTags, key)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(actualValue), strings.TrimSpace(value)) {
			return false
		}
	}
	return true
}

func lookupTag(tags map[string]*string, key string) (string, bool) {
	if len(tags) == 0 {
		return "", false
	}
	for existingKey, existingValue := range tags {
		if !strings.EqualFold(existingKey, key) || existingValue == nil {
			continue
		}
		return *existingValue, true
	}
	return "", false
}
