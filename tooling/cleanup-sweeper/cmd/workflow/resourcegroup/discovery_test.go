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

func TestSortDeletionTargets(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		deletionTargets   sets.Set[string]
		allResourceGroups []*armresources.ResourceGroup
		excludedRGs       []string
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
			name:            "excluded child is not added",
			deletionTargets: sets.New("parent-rg"),
			excludedRGs:     []string{"child-rg"},
			allResourceGroups: []*armresources.ResourceGroup{
				{Name: ptr("parent-rg")},
				{Name: ptr("child-rg"), ManagedBy: ptr("/subscriptions/sub/resourceGroups/parent-rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster")},
			},
			want: []string{"parent-rg"},
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
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := logr.Discard()
			excluded := sets.New(tc.excludedRGs...)
			candidateSources := map[string]string{}

			got := promoteAndSortDeletionTargets(logger, tc.deletionTargets, tc.allResourceGroups, excluded, candidateSources)

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
