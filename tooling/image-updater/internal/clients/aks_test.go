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

package clients

import (
	"strings"
	"testing"
)

func TestPickDefaultMeshRevision(t *testing.T) {
	tests := []struct {
		name       string
		all        []string
		upgradable []string
		want       string
		wantErrIn  string
	}{
		{
			name:       "steady_state_picks_highest_with_upgrades",
			all:        []string{"asm-1-28", "asm-1-29", "asm-1-30"},
			upgradable: []string{"asm-1-28", "asm-1-29"},
			want:       "asm-1-29",
		},
		{
			name:       "after_promotion_default_moves_forward",
			all:        []string{"asm-1-29", "asm-1-30", "asm-1-31"},
			upgradable: []string{"asm-1-29", "asm-1-30"},
			want:       "asm-1-30",
		},
		{
			name:       "single_revision_catalogue_falls_back_to_only_option",
			all:        []string{"asm-1-30"},
			upgradable: nil,
			want:       "asm-1-30",
		},
		{
			name:       "two_revision_catalogue_picks_the_upgradable_one",
			all:        []string{"asm-1-29", "asm-1-30"},
			upgradable: []string{"asm-1-29"},
			want:       "asm-1-29",
		},
		{
			name:       "ignores_lower_bleeding_edge_when_higher_has_upgrades",
			all:        []string{"asm-1-9", "asm-1-10"},
			upgradable: []string{"asm-1-9"},
			want:       "asm-1-9",
		},
		{
			name:      "empty_input_returns_error",
			all:       nil,
			wantErrIn: "no revisions returned",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickDefaultMeshRevision(tc.all, tc.upgradable, "westus3")
			if tc.wantErrIn != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrIn) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErrIn, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPickDefaultMeshRevision_UsesNumericOrdering(t *testing.T) {
	// Regression guard: lexical sort would rank "asm-1-9" > "asm-1-10".
	got, err := pickDefaultMeshRevision(
		[]string{"asm-1-9", "asm-1-10", "asm-1-11"},
		[]string{"asm-1-9", "asm-1-10"},
		"eastus2",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "asm-1-10" {
		t.Fatalf("got %q, want asm-1-10 (numeric, not lexical)", got)
	}
}

func TestPickDefaultMeshRevision_ErrorIncludesLocation(t *testing.T) {
	_, err := pickDefaultMeshRevision(nil, nil, "australiaeast")
	if err == nil || !strings.Contains(err.Error(), "australiaeast") {
		t.Fatalf("expected error to mention location, got %v", err)
	}
}
