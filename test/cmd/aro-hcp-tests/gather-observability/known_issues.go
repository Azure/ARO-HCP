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

//go:embed known-issues/knownIssues.yaml
var defaultKnownIssuesData []byte

// knownIssue holds a compiled known issue pattern with name and label regexes.
type knownIssue struct {
	pattern *regexp.Regexp
	labels  map[string]*regexp.Regexp
	reason  string
}

// parseKnownIssues parses known issues YAML data, validates required fields,
// and compiles all name and label regex patterns. Each pattern is wrapped in ^(?:...)$
// for full-match semantics.
func parseKnownIssues(data []byte) ([]knownIssue, error) {
	var cfg struct {
		KnownIssues []struct {
			Name   string            `yaml:"name"`
			Reason string            `yaml:"reason"`
			Labels map[string]string `yaml:"labels,omitempty"`
		} `yaml:"knownIssues"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse known issues config: %w", err)
	}
	result := make([]knownIssue, len(cfg.KnownIssues))
	for i, ki := range cfg.KnownIssues {
		if ki.Name == "" {
			return nil, fmt.Errorf("knownIssue %d: name is required", i)
		}
		if ki.Reason == "" {
			return nil, fmt.Errorf("knownIssue %d (%s): reason is required", i, ki.Name)
		}
		re, err := regexp.Compile("^(?:" + ki.Name + ")$")
		if err != nil {
			return nil, fmt.Errorf("knownIssue %d (%s): invalid name regex: %w", i, ki.Name, err)
		}
		var labelPatterns map[string]*regexp.Regexp
		if len(ki.Labels) > 0 {
			labelPatterns = make(map[string]*regexp.Regexp, len(ki.Labels))
			for k, v := range ki.Labels {
				lre, err := regexp.Compile("^(?:" + v + ")$")
				if err != nil {
					return nil, fmt.Errorf("knownIssue %d (%s): invalid label regex for %q: %w", i, ki.Name, k, err)
				}
				labelPatterns[k] = lre
			}
		}
		result[i] = knownIssue{pattern: re, labels: labelPatterns, reason: ki.Reason}
	}
	return result, nil
}

// classifyAlerts returns a copy of the alerts with Metadata.KnownIssue and
// Metadata.KnownIssueReason set based on the given known issue patterns.
// The first matching pattern wins.
// When a known issue has label patterns, all specified labels must also match.
func classifyAlerts(alerts []alert, issues []knownIssue) []alert {
	result := make([]alert, len(alerts))
	copy(result, alerts)
	for i := range result {
		for _, ki := range issues {
			if !ki.pattern.MatchString(result[i].Alert.Name) {
				continue
			}
			if !matchLabels(result[i].Alert.Labels, ki.labels) {
				continue
			}
			result[i].Metadata.KnownIssue = true
			result[i].Metadata.KnownIssueReason = ki.reason
			break
		}
	}
	return result
}

// matchLabels returns true if all required label patterns match the alert's labels.
// Returns true when there are no required label patterns.
func matchLabels(alertLabels map[string]string, requiredLabels map[string]*regexp.Regexp) bool {
	for k, pattern := range requiredLabels {
		v, ok := alertLabels[k]
		if !ok || !pattern.MatchString(v) {
			return false
		}
	}
	return true
}
