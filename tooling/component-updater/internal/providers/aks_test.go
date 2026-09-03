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

func TestAKSProvider_NextVersion(t *testing.T) {
	p := NewAKSProvider("sub", nil)

	tests := []struct {
		name      string
		current   string
		available []string
		want      string
	}{
		{"next minor available", "1.35", []string{"1.34", "1.35", "1.36"}, "1.36"},
		{"already latest", "1.36", []string{"1.34", "1.35", "1.36"}, ""},
		{"gap", "1.33", []string{"1.33", "1.35"}, ""},
		{"invalid", "abc", []string{"1.35"}, ""},
		{"empty", "1.35", nil, ""},
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

func TestExtractMinor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.35.2", "1.35"},
		{"1.36.0", "1.36"},
		{"1.35", "1.35"},
		{"1", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractMinor(tt.input)
			if got != tt.want {
				t.Errorf("extractMinor(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSortMinorVersions(t *testing.T) {
	versions := []string{"1.36", "1.34", "1.35"}
	sortMinorVersions(versions)
	expected := []string{"1.34", "1.35", "1.36"}
	for i, v := range versions {
		if v != expected[i] {
			t.Errorf("index %d: got %q, want %q", i, v, expected[i])
		}
	}
}
