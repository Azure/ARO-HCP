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

package upgrade

import (
	"slices"
	"testing"
)

func TestParseAsmRevision(t *testing.T) {
	cases := map[string]struct {
		wantMajor, wantMinor int
		wantErr              bool
	}{
		"asm-1-28":  {1, 28, false},
		"asm-1-9":   {1, 9, false},
		"asm-1-100": {1, 100, false},
		"asm-2-0":   {2, 0, false},
		"asm-1":     {0, 0, true},
		"1-28":      {0, 0, true},
		"asm-a-b":   {0, 0, true},
		"":          {0, 0, true},
	}
	for in, want := range cases {
		gotMajor, gotMinor, err := ParseAsmRevision(in)
		if want.wantErr {
			if err == nil {
				t.Errorf("ParseAsmRevision(%q) = (%d,%d,nil), want error", in, gotMajor, gotMinor)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAsmRevision(%q) error: %v", in, err)
			continue
		}
		if gotMajor != want.wantMajor || gotMinor != want.wantMinor {
			t.Errorf("ParseAsmRevision(%q) = (%d,%d), want (%d,%d)", in, gotMajor, gotMinor, want.wantMajor, want.wantMinor)
		}
	}
}

func TestCompareAsmRevisions_NumericMinor(t *testing.T) {
	// asm-1-9 must compare < asm-1-10 (numeric, not lexicographic).
	if got := CompareAsmRevisions("asm-1-9", "asm-1-10"); got >= 0 {
		t.Errorf("asm-1-9 vs asm-1-10 = %d, want < 0", got)
	}
	if got := CompareAsmRevisions("asm-1-10", "asm-1-9"); got <= 0 {
		t.Errorf("asm-1-10 vs asm-1-9 = %d, want > 0", got)
	}
	if got := CompareAsmRevisions("asm-1-28", "asm-1-28"); got != 0 {
		t.Errorf("equal revisions returned %d, want 0", got)
	}
}

func TestCompareAsmRevisions_MajorWins(t *testing.T) {
	if got := CompareAsmRevisions("asm-1-999", "asm-2-0"); got >= 0 {
		t.Errorf("asm-1-999 vs asm-2-0 = %d, want < 0", got)
	}
}

func TestCompareAsmRevisions_SortFuncOrder(t *testing.T) {
	got := []string{"asm-1-29", "asm-1-9", "asm-1-30", "asm-1-26"}
	slices.SortFunc(got, CompareAsmRevisions)
	want := []string{"asm-1-9", "asm-1-26", "asm-1-29", "asm-1-30"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sorted[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestHighestAsmRevision(t *testing.T) {
	got, err := HighestAsmRevision([]string{"asm-1-26", "asm-1-30", "asm-1-9"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "asm-1-30" {
		t.Errorf("HighestAsmRevision = %q, want asm-1-30", got)
	}
	if _, err := HighestAsmRevision(nil); err == nil {
		t.Errorf("expected error on empty slice")
	}
}

func TestHighestAsmRevision_UnparseableSortsFirst(t *testing.T) {
	// A garbage entry must not pretend to be the maximum.
	got, err := HighestAsmRevision([]string{"asm-1-28", "banana", "asm-1-29"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "asm-1-29" {
		t.Errorf("HighestAsmRevision returned %q, want asm-1-29 (garbage must not win)", got)
	}
}

func TestIntersectAsmRevisions(t *testing.T) {
	cases := []struct {
		name string
		in   [][]string
		want []string
	}{
		{
			name: "no sets",
			in:   nil,
			want: nil,
		},
		{
			name: "single set is returned deduped in first-set order",
			in:   [][]string{{"asm-1-28", "asm-1-29", "asm-1-28"}},
			want: []string{"asm-1-28", "asm-1-29"},
		},
		{
			name: "common across all",
			in: [][]string{
				{"asm-1-28", "asm-1-29", "asm-1-30"},
				{"asm-1-29", "asm-1-30"},
				{"asm-1-30", "asm-1-29"},
			},
			want: []string{"asm-1-29", "asm-1-30"},
		},
		{
			name: "no overlap yields empty",
			in: [][]string{
				{"asm-1-28"},
				{"asm-1-29"},
			},
			want: nil,
		},
		{
			name: "duplicates within one region do not inflate count",
			in: [][]string{
				{"asm-1-29", "asm-1-29"},
				{"asm-1-28"},
			},
			want: nil,
		},
		{
			name: "order follows first set",
			in: [][]string{
				{"asm-1-30", "asm-1-28", "asm-1-29"},
				{"asm-1-28", "asm-1-29", "asm-1-30"},
			},
			want: []string{"asm-1-30", "asm-1-28", "asm-1-29"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IntersectAsmRevisions(tc.in...)
			if !slices.Equal(got, tc.want) {
				t.Errorf("IntersectAsmRevisions(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
