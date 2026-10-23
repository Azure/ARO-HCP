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

package resourcegroup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

func TestDiscoverCandidates(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		opts        RunOptions
		expectErr   bool
		errContains string
		want        []string
	}{
		{
			name: "explicit-only candidates are sorted",
			opts: RunOptions{
				ResourceGroups: sets.New("rg-b", "rg-a"),
				Policy: policy.RGOrderedPolicy{
					Discovery: policy.RGDiscoveryPolicy{},
				},
			},
			expectErr: false,
			want:      []string{"rg-a", "rg-b"},
		},
		{
			name: "policy discovery requires reference time when rules are configured",
			opts: RunOptions{
				ResourceGroups: sets.New[string](),
				ReferenceTime:  time.Time{},
				Policy: policy.RGOrderedPolicy{
					Discovery: policy.RGDiscoveryPolicy{
						Rules: []policy.RGDiscoveryRule{
							{
								Action:    policy.RGDiscoveryActionDelete,
								Match:     policy.RGDiscoveryMatch{Any: true},
								OlderThan: time.Hour,
							},
						},
					},
				},
			},
			expectErr:   true,
			errContains: "reference time is required",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := logr.NewContext(context.Background(), logr.Discard())
			got, err := discoverCandidates(ctx, tc.opts)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d resource groups, got %d", len(tc.want), len(got))
			}
			for idx := range tc.want {
				if got[idx] != tc.want[idx] {
					t.Fatalf("expected sorted resource groups %v, got %v", tc.want, got)
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func TestPromoteAndSortDeletionTargets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour).Format(time.RFC3339)
	young := now.Add(-time.Hour).Format(time.RFC3339)
	persistPolicy := policy.RGDiscoveryPolicy{
		Rules: []policy.RGDiscoveryRule{
			{
				Name:       "skip-managed",
				Action:     policy.RGDiscoveryActionSkip,
				Match:      policy.RGDiscoveryMatch{Any: true},
				Conditions: policy.RGDiscoveryConditions{ManagedByAlive: boolPtr(true)},
			},
			{
				Name:       "skip-persist",
				Action:     policy.RGDiscoveryActionSkip,
				Match:      policy.RGDiscoveryMatch{Any: true},
				Conditions: policy.RGDiscoveryConditions{TagsEq: map[string]string{"persist": "true"}},
			},
			{
				Name:      "delete-after-48h",
				Action:    policy.RGDiscoveryActionDelete,
				Match:     policy.RGDiscoveryMatch{Any: true},
				OlderThan: 48 * time.Hour,
			},
		},
	}

	testCases := []struct {
		name              string
		deletionTargets   sets.Set[string]
		allResourceGroups []*armresources.ResourceGroup
		excludedRGs       []string
		discovery         policy.RGDiscoveryPolicy
		want              []string
		wantTargetsAdded  []string
	}{
		{
			name:            "managed child of target is added and sorted first",
			deletionTargets: sets.New("parent-rg"),
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg")},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster")},
			},
			want:             []string{"child-rg", "parent-rg"},
			wantTargetsAdded: []string{"child-rg"},
		},
		{
			name:            "multiple children before parent",
			deletionTargets: sets.New("parent-rg"),
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg")},
				{Name: ptr("child-b"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/b")},
				{Name: ptr("child-a"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/a")},
			},
			want:             []string{"child-a", "child-b", "parent-rg"},
			wantTargetsAdded: []string{"child-a", "child-b"},
		},
		{
			name:            "child already a target is sorted first without duplication",
			deletionTargets: sets.New("parent-rg", "z-child-rg"),
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg")},
				{Name: ptr("z-child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster")},
			},
			want: []string{"z-child-rg", "parent-rg"},
		},
		{
			name:            "excluded child protects its parent",
			deletionTargets: sets.New("parent-rg"),
			excludedRGs:     []string{"child-rg"},
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg")},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster")},
			},
			want: []string{},
		},
		{
			name:            "managed RG whose parent is not a target is ignored",
			deletionTargets: sets.New("other-rg"),
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("other-rg")},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/not-a-target/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster")},
			},
			want: []string{"other-rg"},
		},
		{
			name:            "unparsable managedBy is skipped",
			deletionTargets: sets.New("parent-rg"),
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg")},
				{Name: ptr("child-rg"), ManagedBy: ptr("not-a-valid-resource-id")},
			},
			want: []string{"parent-rg"},
		},
		{
			name:            "case-insensitive parent matching",
			deletionTargets: sets.New("Parent-RG"),
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("Parent-RG")},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster")},
			},
			want:             []string{"child-rg", "Parent-RG"},
			wantTargetsAdded: []string{"child-rg"},
		},
		{
			name:            "child skipped by a policy rule protects its parent and siblings",
			deletionTargets: sets.New("parent-rg", "other-rg"),
			discovery:       persistPolicy,
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg"), Tags: map[string]*string{"createdAt": ptr(old)}},
				{Name: ptr("other-rg"), Tags: map[string]*string{"createdAt": ptr(old)}},
				// Mixed-case key and value: tagsEq matching is case-insensitive, as in real "persist=True" tags.
				{Name: ptr("child-a"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenShift/openShiftClusters/a"), Tags: map[string]*string{"createdAt": ptr(old), "Persist": ptr("True")}},
				{Name: ptr("child-b"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenShift/openShiftClusters/b"), Tags: map[string]*string{"createdAt": ptr(old)}},
			},
			want: []string{"other-rg"},
		},
		{
			name:            "managedByAlive skip rule does not protect a child",
			deletionTargets: sets.New("parent-rg"),
			discovery:       persistPolicy,
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg"), Tags: map[string]*string{"createdAt": ptr(old)}},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenShift/openShiftClusters/cluster"), Tags: map[string]*string{"createdAt": ptr(old)}},
			},
			want:             []string{"child-rg", "parent-rg"},
			wantTargetsAdded: []string{"child-rg"},
		},
		{
			name:            "child that is too young to delete is still promoted",
			deletionTargets: sets.New("parent-rg"),
			discovery:       persistPolicy,
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg"), Tags: map[string]*string{"createdAt": ptr(old)}},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenShift/openShiftClusters/cluster"), Tags: map[string]*string{"createdAt": ptr(young)}},
			},
			want:             []string{"child-rg", "parent-rg"},
			wantTargetsAdded: []string{"child-rg"},
		},
		{
			name:            "protection propagates through nested managed groups",
			deletionTargets: sets.New("grandparent-rg"),
			discovery:       persistPolicy,
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("grandparent-rg"), Tags: map[string]*string{"createdAt": ptr(old)}},
				{Name: ptr("parent-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/grandparent-rg/providers/Microsoft.Example/managers/p"), Tags: map[string]*string{"createdAt": ptr(old)}},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.Example/managers/c"), Tags: map[string]*string{"createdAt": ptr(old), "persist": ptr("true")}},
			},
			want: []string{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := logr.Discard()
			excluded := sets.New(tc.excludedRGs...)
			candidateSources := map[string]string{}

			got := promoteAndSortDeletionTargets(logger, tc.deletionTargets, tc.allResourceGroups, excluded, tc.discovery, now, candidateSources)

			if len(got) != len(tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
			for idx := range tc.want {
				if got[idx] != tc.want[idx] {
					t.Fatalf("expected %v, got %v", tc.want, got)
				}
			}
			for _, added := range tc.wantTargetsAdded {
				if !tc.deletionTargets.Has(added) {
					t.Errorf("expected %q to be added to deletion targets", added)
				}
			}
		})
	}
}

func TestPromoteAndSortDeletionTargetsZeroReferenceTimeFailsClosed(t *testing.T) {
	t.Parallel()

	discovery := policy.RGDiscoveryPolicy{
		Rules: []policy.RGDiscoveryRule{
			{
				Action:     policy.RGDiscoveryActionSkip,
				Match:      policy.RGDiscoveryMatch{Any: true},
				Conditions: policy.RGDiscoveryConditions{TagsEq: map[string]string{"persist": "true"}},
			},
		},
	}
	deletionTargets := sets.New("parent-rg")
	allResourceGroups := []*armresources.ResourceGroup{
		{Name: ptr("parent-rg")},
		{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenShift/openShiftClusters/cluster"), Tags: map[string]*string{"persist": ptr("true")}},
	}

	got := promoteAndSortDeletionTargets(logr.Discard(), deletionTargets, allResourceGroups, sets.New[string](), discovery, time.Time{}, map[string]string{})
	if len(got) != 0 {
		t.Fatalf("expected no targets, got %v", got)
	}
}
