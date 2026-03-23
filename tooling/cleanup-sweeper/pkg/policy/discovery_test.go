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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestSelectsResourceGroup_IntendedLegacyPolicyBehavior(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 15, 0, 0, 0, time.UTC)
	pol := &RGDiscoveryPolicy{
		Rules: []RGDiscoveryRule{
			{
				Name:   "skip-managed-resource-groups",
				Action: RGDiscoveryActionSkip,
				Match:  RGDiscoveryMatch{Any: true},
				Conditions: RGDiscoveryConditions{
					ManagedBySet: boolPtr(true),
				},
			},
			{
				Name:   "pers-persist-delete-after-15d",
				Action: RGDiscoveryActionDelete,
				Match:  RGDiscoveryMatch{NamePrefix: "hcp-underlay-pers-"},
				Conditions: RGDiscoveryConditions{
					TagsEq: map[string]string{"persist": "true"},
				},
				OlderThan: 15 * 24 * time.Hour,
			},
			{
				Name:   "pers-non-persist-delete-after-2d",
				Action: RGDiscoveryActionDelete,
				Match:  RGDiscoveryMatch{NamePrefix: "hcp-underlay-pers-"},
				Conditions: RGDiscoveryConditions{
					TagsNotEq: map[string]string{"persist": "true"},
				},
				OlderThan: 48 * time.Hour,
			},
			{
				Name:   "skip-persist-true-globally",
				Action: RGDiscoveryActionSkip,
				Match:  RGDiscoveryMatch{Any: true},
				Conditions: RGDiscoveryConditions{
					TagsEq: map[string]string{"persist": "true"},
				},
			},
			{
				Name:      "global-default-delete-after-2d",
				Action:    RGDiscoveryActionDelete,
				Match:     RGDiscoveryMatch{Any: true},
				OlderThan: 48 * time.Hour,
			},
		},
	}

	testCases := []struct {
		name           string
		rg             *armresources.ResourceGroup
		excluded       sets.Set[string]
		expectSelected bool
	}{
		{
			name: "managed RG is always skipped",
			rg: newResourceGroup(
				"hcp-underlay-pers-usw3rvaz",
				timePtr(now.Add(-20*24*time.Hour)),
				map[string]string{"persist": "false"},
				true,
			),
			excluded:       sets.New[string](),
			expectSelected: false,
		},
		{
			name: "pers persist true older than 15 days is selected",
			rg: newResourceGroup(
				"hcp-underlay-pers-usw3rvaz",
				timePtr(now.Add(-16*24*time.Hour)),
				map[string]string{"persist": "true"},
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: true,
		},
		{
			name: "pers persist true younger than 15 days is skipped",
			rg: newResourceGroup(
				"hcp-underlay-pers-usw3rvaz",
				timePtr(now.Add(-10*24*time.Hour)),
				map[string]string{"persist": "true"},
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: false,
		},
		{
			name: "pers persist false older than 2 days is selected",
			rg: newResourceGroup(
				"hcp-underlay-pers-usw3rvaz",
				timePtr(now.Add(-72*time.Hour)),
				map[string]string{"persist": "false"},
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: true,
		},
		{
			name: "pers missing persist tag uses 2-day rule",
			rg: newResourceGroup(
				"hcp-underlay-pers-usw3rvaz",
				timePtr(now.Add(-72*time.Hour)),
				nil,
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: true,
		},
		{
			name: "non-pers persist true is skipped by global persist protection",
			rg: newResourceGroup(
				"hcp-underlay-dev-usw3rvaz",
				timePtr(now.Add(-72*time.Hour)),
				map[string]string{"persist": "true"},
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: false,
		},
		{
			name: "non-pers older than 2 days is selected by global default",
			rg: newResourceGroup(
				"hcp-underlay-dev-usw3rvaz",
				timePtr(now.Add(-72*time.Hour)),
				nil,
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: true,
		},
		{
			name: "missing createdAt is skipped",
			rg: newResourceGroup(
				"hcp-underlay-dev-usw3rvaz",
				nil,
				nil,
				false,
			),
			excluded:       sets.New[string](),
			expectSelected: false,
		},
		{
			name: "excluded resource group is skipped regardless of age",
			rg: newResourceGroup(
				"hcp-underlay-dev-usw3rvaz",
				timePtr(now.Add(-72*time.Hour)),
				nil,
				false,
			),
			excluded:       sets.New("hcp-underlay-dev-usw3rvaz"),
			expectSelected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			selected, _ := pol.SelectsResourceGroup(tc.rg, tc.excluded, now)
			if selected != tc.expectSelected {
				t.Fatalf("expected selected=%t, got selected=%t", tc.expectSelected, selected)
			}
		})
	}
}

func TestParseCreatedAt_PythonCompatibleFormat(t *testing.T) {
	t.Parallel()

	tags := map[string]*string{
		"createdAt": strPtr("2026-03-16T15:00:00.123456789123Z"),
	}

	parsed, ok := parseCreatedAt(tags)
	if !ok {
		t.Fatalf("expected createdAt to parse")
	}

	expected := time.Date(2026, time.March, 16, 15, 0, 0, 123456789, time.UTC)
	if !parsed.Equal(expected) {
		t.Fatalf("expected parsed time %s, got %s", expected, parsed)
	}
}

func TestSelectsResourceGroup_ReturnsStructuredRuleReason(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 15, 0, 0, 0, time.UTC)
	pol := &RGDiscoveryPolicy{
		Rules: []RGDiscoveryRule{
			{
				Name:      "global-default-delete-after-2d",
				Action:    RGDiscoveryActionDelete,
				Match:     RGDiscoveryMatch{Any: true},
				OlderThan: 48 * time.Hour,
			},
		},
	}
	rg := newResourceGroup("example-rg", timePtr(now.Add(-72*time.Hour)), nil, false)

	selected, reason := pol.SelectsResourceGroup(rg, sets.New[string](), now)
	if !selected {
		t.Fatalf("expected resource group to be selected")
	}
	if reason.Rule == nil {
		t.Fatalf("expected structured rule details in reason")
	}
	if reason.Rule.Index != 0 {
		t.Fatalf("expected rule index 0, got %d", reason.Rule.Index)
	}
	if reason.Rule.Name != "global-default-delete-after-2d" {
		t.Fatalf("unexpected rule name %q", reason.Rule.Name)
	}
	if reason.Rule.Result != "expired" {
		t.Fatalf("unexpected rule result %q", reason.Rule.Result)
	}
	if got, want := reason.String(), "rule[0]-expired"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSelectionReasonSourceDescription_WithAndWithoutRule(t *testing.T) {
	t.Parallel()

	withRule := RGSelectionReason{
		Rule: &RGSelectionRule{
			Index:  2,
			Name:   "named-rule",
			Result: "expired",
		},
	}
	if got, want := withRule.SourceDescription(), `matched policy rule "named-rule" (expired)`; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	withoutRule := RGSelectionReason{Code: "excluded"}
	if got, want := withoutRule.SourceDescription(), "matched policy (excluded)"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func newResourceGroup(name string, createdAt *time.Time, tags map[string]string, managed bool) *armresources.ResourceGroup {
	resourceGroup := &armresources.ResourceGroup{
		Name: strPtr(name),
	}

	if createdAt != nil {
		if tags == nil {
			tags = map[string]string{}
		}
		tags["createdAt"] = createdAt.UTC().Format(time.RFC3339Nano)
	}

	if len(tags) > 0 {
		resourceGroup.Tags = make(map[string]*string, len(tags))
		for key, value := range tags {
			resourceGroup.Tags[key] = strPtr(value)
		}
	}

	if managed {
		resourceGroup.ManagedBy = strPtr("/subscriptions/example/resourceGroups/example/providers/Microsoft.ManagedIdentity/userAssignedIdentities/example")
	}

	return resourceGroup
}

func strPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func timePtr(value time.Time) *time.Time {
	return &value
}
