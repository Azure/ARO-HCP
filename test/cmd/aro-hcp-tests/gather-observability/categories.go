// Copyright 2025 Microsoft Corporation
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

package gatherobservability

import (
	"fmt"
	"regexp"

	_ "embed"

	"sigs.k8s.io/yaml"
)

//go:embed alert-categories/categories.yaml
var defaultCategoriesData []byte

// Category CI policies. See ARO-28187 and docs/ci/ARO-28187-alert-categorization.md:
// the intent (per David Eads' review of the earlier known-issues-skip-list
// approach) is to sort alert firings by customer/service blast radius and
// fail CI according to that blast radius, rather than blanket-suppressing
// alerts that are merely noisy.
const (
	// policyFail fails the owning test case whenever the category has any
	// firings at all.
	policyFail = "fail"
	// policyFailOverThreshold fails the owning test case only once the
	// category's firings for that rule exceed the configured threshold
	// (minFirings and/or minDurationSeconds).
	policyFailOverThreshold = "fail-over-threshold"
	// policyWarn never fails the test case; firings are still reported
	// (as a non-failing/skip annotation) so they remain visible.
	policyWarn = "warn"
	// policyIgnore never fails the test case and is the successor to the
	// old known-issues skip list (expected, understood noise).
	policyIgnore = "ignore"
)

func isKnownPolicy(p string) bool {
	switch p {
	case policyFail, policyFailOverThreshold, policyWarn, policyIgnore:
		return true
	default:
		return false
	}
}

// categoryThreshold configures the "fail-over-threshold" policy. A category
// fails once the number of firings reaches minFirings, or once their merged
// duration reaches minDurationSeconds, whichever comes first. A zero value
// disables that particular condition.
type categoryThreshold struct {
	minFirings         int
	minDurationSeconds float64
}

// categoryRule is one alternative match for a category: an alert firing is
// claimed by the category if it satisfies any one of the category's rules.
// An empty rule (no workspace/pattern/labels) matches every alert, which is
// how the trailing catch-all category is expressed.
type categoryRule struct {
	workspace string // "" means any workspace
	pattern   *regexp.Regexp
	labels    map[string]*regexp.Regexp
}

// category holds a compiled blast-radius category: a name, David Eads' tier
// number, a CI policy, and the rules used to claim alert firings.
type category struct {
	name      string
	tier      int
	policy    string
	threshold categoryThreshold
	reason    string
	rules     []categoryRule
}

type rawCategoryMatch struct {
	Workspace string            `yaml:"workspace,omitempty"`
	Name      string            `yaml:"name,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

type rawCategoryThreshold struct {
	MinFirings         int     `yaml:"minFirings,omitempty"`
	MinDurationSeconds float64 `yaml:"minDurationSeconds,omitempty"`
}

type rawCategory struct {
	Name      string               `yaml:"name"`
	Tier      int                  `yaml:"tier"`
	Policy    string               `yaml:"policy"`
	Threshold rawCategoryThreshold `yaml:"threshold,omitempty"`
	Reason    string               `yaml:"reason"`
	Match     []rawCategoryMatch   `yaml:"match"`
}

// parseCategories parses the categories YAML data, validates required
// fields, and compiles all name and label regex patterns. Each pattern is
// wrapped in ^(?:...)$ for full-match semantics, matching the matching
// engine the previous known-issues list used.
func parseCategories(data []byte) ([]category, error) {
	var cfg struct {
		Categories []rawCategory `yaml:"categories"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse categories config: %w", err)
	}
	result := make([]category, len(cfg.Categories))
	for i, rc := range cfg.Categories {
		if rc.Name == "" {
			return nil, fmt.Errorf("category %d: name is required", i)
		}
		if rc.Reason == "" {
			return nil, fmt.Errorf("category %d (%s): reason is required", i, rc.Name)
		}
		if !isKnownPolicy(rc.Policy) {
			return nil, fmt.Errorf("category %d (%s): unknown policy %q, expected one of fail, fail-over-threshold, warn, ignore", i, rc.Name, rc.Policy)
		}
		if rc.Policy == policyFailOverThreshold && rc.Threshold.MinFirings <= 0 && rc.Threshold.MinDurationSeconds <= 0 {
			return nil, fmt.Errorf("category %d (%s): policy fail-over-threshold requires threshold.minFirings and/or threshold.minDurationSeconds greater than zero", i, rc.Name)
		}
		if len(rc.Match) == 0 && i != len(cfg.Categories)-1 {
			return nil, fmt.Errorf("category %d (%s): an empty match list matches every alert and must only be used on the last category (a catch-all); it would otherwise shadow every category after it", i, rc.Name)
		}

		rules := make([]categoryRule, len(rc.Match))
		for j, m := range rc.Match {
			namePattern := m.Name
			if namePattern == "" {
				namePattern = ".*"
			}
			re, err := regexp.Compile("^(?:" + namePattern + ")$")
			if err != nil {
				return nil, fmt.Errorf("category %d (%s): match %d: invalid name regex: %w", i, rc.Name, j, err)
			}
			var labelPatterns map[string]*regexp.Regexp
			if len(m.Labels) > 0 {
				labelPatterns = make(map[string]*regexp.Regexp, len(m.Labels))
				for k, v := range m.Labels {
					lre, err := regexp.Compile("^(?:" + v + ")$")
					if err != nil {
						return nil, fmt.Errorf("category %d (%s): match %d: invalid label regex for %q: %w", i, rc.Name, j, k, err)
					}
					labelPatterns[k] = lre
				}
			}
			if m.Workspace != "" && m.Workspace != workspaceSvc && m.Workspace != workspaceHcp && m.Workspace != workspaceInfra {
				return nil, fmt.Errorf("category %d (%s): match %d: unknown workspace %q, expected one of %q, %q, %q", i, rc.Name, j, m.Workspace, workspaceSvc, workspaceHcp, workspaceInfra)
			}
			rules[j] = categoryRule{workspace: m.Workspace, pattern: re, labels: labelPatterns}
		}

		result[i] = category{
			name:   rc.Name,
			tier:   rc.Tier,
			policy: rc.Policy,
			threshold: categoryThreshold{
				minFirings:         rc.Threshold.MinFirings,
				minDurationSeconds: rc.Threshold.MinDurationSeconds,
			},
			reason: rc.Reason,
			rules:  rules,
		}
	}
	return result, nil
}

// categorizeAlerts returns a copy of the alerts with Metadata's category
// fields set based on the given categories. The first matching category
// wins. Alerts matching no category are left uncategorized; junit.go treats
// an uncategorized firing as policy "fail" (David Eads: "catch-all treated
// as [tier] 1a until reassigned"), so a categories config lacking an
// explicit trailing catch-all still fails closed rather than silently
// passing.
func categorizeAlerts(alerts []alert, categories []category) []alert {
	result := make([]alert, len(alerts))
	copy(result, alerts)
	for i := range result {
		for _, c := range categories {
			if !matchCategory(result[i], c) {
				continue
			}
			result[i].Metadata.Category = c.name
			result[i].Metadata.CategoryTier = c.tier
			result[i].Metadata.CategoryPolicy = c.policy
			result[i].Metadata.CategoryReason = c.reason
			result[i].Metadata.CategoryMinFirings = c.threshold.minFirings
			result[i].Metadata.CategoryMinDurationSeconds = c.threshold.minDurationSeconds
			// KnownIssue/KnownIssueReason drive the HTML dashboard's
			// known/unknown display and are the successor to the old
			// known-issues skip list, now the "ignore" policy.
			result[i].Metadata.KnownIssue = c.policy == policyIgnore
			if result[i].Metadata.KnownIssue {
				result[i].Metadata.KnownIssueReason = c.reason
			}
			break
		}
	}
	return result
}

// matchCategory returns true if the alert satisfies any one of the
// category's rules. A category with no rules matches every alert (the
// catch-all convention).
func matchCategory(a alert, c category) bool {
	if len(c.rules) == 0 {
		return true
	}
	for _, r := range c.rules {
		if matchRule(a, r) {
			return true
		}
	}
	return false
}

func matchRule(a alert, r categoryRule) bool {
	if r.workspace != "" && r.workspace != a.Metadata.MonitoringWorkspaceType {
		return false
	}
	if !r.pattern.MatchString(a.Alert.Name) {
		return false
	}
	return matchLabels(a.Alert.Labels, r.labels)
}

// matchLabels returns true if all required label patterns match the alert's
// labels. Returns true when there are no required label patterns.
func matchLabels(alertLabels map[string]string, requiredLabels map[string]*regexp.Regexp) bool {
	for k, pattern := range requiredLabels {
		v, ok := alertLabels[k]
		if !ok || !pattern.MatchString(v) {
			return false
		}
	}
	return true
}
