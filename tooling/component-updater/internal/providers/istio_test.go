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

package providers

import "testing"

func TestIstioProvider_NextVersion(t *testing.T) {
	p := NewIstioProvider("sub", nil)

	tests := []struct {
		name      string
		current   string
		available []string
		want      string
	}{
		{"next revision available", "asm-1-29", []string{"asm-1-28", "asm-1-29", "asm-1-30"}, "asm-1-30"},
		{"already latest", "asm-1-30", []string{"asm-1-28", "asm-1-29", "asm-1-30"}, ""},
		{"gap", "asm-1-27", []string{"asm-1-27", "asm-1-29"}, ""},
		{"invalid format", "istio-1.29", []string{"asm-1-29", "asm-1-30"}, ""},
		{"empty available", "asm-1-29", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.NextVersion(tt.current, tt.available)
			if got != tt.want {
				t.Errorf("NextVersion(%q, %v) = %q, want %q", tt.current, tt.available, got, tt.want)
			}
		})
	}
}

func TestParseASMRevision(t *testing.T) {
	tests := []struct {
		input     string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{"asm-1-29", 1, 29, true},
		{"asm-1-30", 1, 30, true},
		{"asm-2-1", 2, 1, true},
		{"istio-1-29", 0, 0, false},
		{"asm-abc-29", 0, 0, false},
		{"", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			major, minor, ok := parseASMRevision(tt.input)
			if ok != tt.wantOK || major != tt.wantMajor || minor != tt.wantMinor {
				t.Errorf("parseASMRevision(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, major, minor, ok, tt.wantMajor, tt.wantMinor, tt.wantOK)
			}
		})
	}
}

func TestSortASMRevisions(t *testing.T) {
	revisions := []string{"asm-1-30", "asm-1-28", "asm-1-29"}
	sortASMRevisions(revisions)
	expected := []string{"asm-1-28", "asm-1-29", "asm-1-30"}
	for i, v := range revisions {
		if v != expected[i] {
			t.Errorf("index %d: got %q, want %q", i, v, expected[i])
		}
	}
}
