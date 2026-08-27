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
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

func TestPolicyValidate_ParsesRegexDurationAndNormalizes(t *testing.T) {
	t.Parallel()

	data := []byte(`
rgOrdered:
  excludedResourceGroups:
    - " Global "
  discovery:
    rules:
      - action: delete
        match:
          nameRegex: "^hcp-underlay-pers-.*$"
        conditions:
          tagsEq:
            Persist: " TRUE "
        olderThan: "48h"
`)

	var pol Policy
	if err := yaml.Unmarshal(data, &pol); err != nil {
		t.Fatalf("expected policy to unmarshal: %v", err)
	}

	if err := pol.Validate(); err != nil {
		t.Fatalf("expected policy to validate: %v", err)
	}

	if len(pol.RGOrdered.ExcludedResourceGroups) != 1 || pol.RGOrdered.ExcludedResourceGroups[0] != "global" {
		t.Fatalf("unexpected excludedResourceGroups normalization: %#v", pol.RGOrdered.ExcludedResourceGroups)
	}

	if len(pol.RGOrdered.Discovery.Rules) != 1 {
		t.Fatalf("expected one discovery rule, got %d", len(pol.RGOrdered.Discovery.Rules))
	}
	rule := pol.RGOrdered.Discovery.Rules[0]
	if rule.Match.NameRegex == nil || !rule.Match.NameRegex.MatchString("hcp-underlay-pers-usw3rvaz") {
		t.Fatalf("expected compiled nameRegex matcher")
	}
	if rule.OlderThan != 48*time.Hour {
		t.Fatalf("expected olderThan to be 48h, got %s", rule.OlderThan)
	}
	if got := rule.Conditions.TagsEq["persist"]; got != "TRUE" {
		t.Fatalf("expected normalized persist tag value TRUE, got %q", got)
	}
}

func TestRepositoryPolicyProtectsIdentityContainerPools(t *testing.T) {
	t.Parallel()

	pol, err := Load("../../resourcegroups.policy.yaml")
	if err != nil {
		t.Fatalf("failed to load repository policy: %v", err)
	}
	if err := pol.Validate(); err != nil {
		t.Fatalf("failed to validate repository policy: %v", err)
	}

	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	excluded := sets.New(pol.RGOrdered.ExcludedResourceGroups...)

	testCases := []struct {
		name           string
		resourceGroup  string
		expectSelected bool
		expectRule     string
	}{
		{
			name:           "identity container older than 15 days is skipped case insensitively",
			resourceGroup:  "ARO-HCP-MSI-Container-dev-shard0-00-00",
			expectSelected: false,
			expectRule:     "skip-identity-container-pools",
		},
		{
			name:           "unrelated persistent group older than 15 days is selected",
			resourceGroup:  "example-persistent-resource-group",
			expectSelected: true,
			expectRule:     "persist-true-delete-after-15d",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resourceGroup := newResourceGroup(
				tc.resourceGroup,
				timePtr(now.Add(-16*24*time.Hour)),
				map[string]string{"persist": "true"},
				false,
			)
			selected, reason := pol.RGOrdered.Discovery.SelectsResourceGroup(
				resourceGroup,
				excluded,
				sets.New[string](),
				now,
			)

			if selected != tc.expectSelected {
				t.Fatalf("expected selected=%t, got selected=%t", tc.expectSelected, selected)
			}
			if reason.Rule == nil || reason.Rule.Name != tc.expectRule {
				t.Fatalf("expected rule %q, got %#v", tc.expectRule, reason.Rule)
			}
		})
	}
}

func TestPolicyValidate_RejectsInvalidRules(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		policy      Policy
		errContains string
	}{
		{
			name: "invalid action",
			policy: Policy{
				RGOrdered: RGOrderedPolicy{
					Discovery: RGDiscoveryPolicy{
						Rules: []RGDiscoveryRule{
							{
								Action: "unknown",
								Match:  RGDiscoveryMatch{Any: true},
							},
						},
					},
				},
			},
			errContains: "must be one of",
		},
		{
			name: "skip action cannot set olderThan",
			policy: Policy{
				RGOrdered: RGOrderedPolicy{
					Discovery: RGDiscoveryPolicy{
						Rules: []RGDiscoveryRule{
							{
								Action:    RGDiscoveryActionSkip,
								Match:     RGDiscoveryMatch{Any: true},
								OlderThan: 24 * time.Hour,
							},
						},
					},
				},
			},
			errContains: "must be omitted for skip action",
		},
		{
			name: "match must define exactly one selector",
			policy: Policy{
				RGOrdered: RGOrderedPolicy{
					Discovery: RGDiscoveryPolicy{
						Rules: []RGDiscoveryRule{
							{
								Action: RGDiscoveryActionDelete,
								Match: RGDiscoveryMatch{
									Any:        true,
									NamePrefix: "hcp-underlay-pers-",
								},
								OlderThan: 24 * time.Hour,
							},
						},
					},
				},
			},
			errContains: "must define exactly one of any, namePrefix, or nameRegex",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.policy.Validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("expected error to contain %q, got %q", tc.errContains, err.Error())
			}
		})
	}
}
