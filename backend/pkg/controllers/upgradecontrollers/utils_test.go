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

package upgradecontrollers

import (
	"testing"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"
)

func TestIsValidNextYStreamUpgradePath(t *testing.T) {
	tests := []struct {
		name          string
		actualMinor   string
		desiredMinor  string
		expectedValid bool
	}{
		{
			name:          "Valid Y-stream upgrade",
			actualMinor:   "4.19",
			desiredMinor:  "4.20",
			expectedValid: true,
		},
		{
			name:          "Same minor version",
			actualMinor:   "4.19",
			desiredMinor:  "4.19",
			expectedValid: false,
		},
		{
			name:          "Downgrade attempt",
			actualMinor:   "4.20",
			desiredMinor:  "4.19",
			expectedValid: false,
		},
		{
			name:          "Skip minor version",
			actualMinor:   "4.19",
			desiredMinor:  "4.21",
			expectedValid: false,
		},
		{
			name:          "Major version change",
			actualMinor:   "4.19",
			desiredMinor:  "5.0",
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidNextYStreamUpgradePath(tt.actualMinor, tt.desiredMinor)
			if result != tt.expectedValid {
				t.Errorf("isValidNextYStreamUpgradePath(%q, %q) = %v, expected %v",
					tt.actualMinor, tt.desiredMinor, result, tt.expectedValid)
			}
		})
	}
}

func TestIsVersionInTargetMinor(t *testing.T) {
	tests := []struct {
		name        string
		ver         string
		targetMinor string
		expected    bool
	}{
		{
			name:        "Same major and minor",
			ver:         "4.19.15",
			targetMinor: "4.19.0",
			expected:    true,
		},
		{
			name:        "Different minor version",
			ver:         "4.20.0",
			targetMinor: "4.19.0",
			expected:    false,
		},
		{
			name:        "Different major version",
			ver:         "5.0.0",
			targetMinor: "4.19.0",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, err := semver.Parse(tt.ver)
			if err != nil {
				t.Fatalf("Failed to parse version %q: %v", tt.ver, err)
			}

			targetMinor, err := semver.Parse(tt.targetMinor)
			if err != nil {
				t.Fatalf("Failed to parse target minor %q: %v", tt.targetMinor, err)
			}

			result := isVersionInTargetMinor(ver, targetMinor)
			if result != tt.expected {
				t.Errorf("isVersionInTargetMinor(%q, %q) = %v, expected %v",
					tt.ver, tt.targetMinor, result, tt.expected)
			}
		})
	}
}

func TestSortReleasesByVersionDescending(t *testing.T) {
	tests := []struct {
		name     string
		input    []configv1.Release
		expected []string // Expected version strings in order
	}{
		{
			name:     "Empty slice",
			input:    []configv1.Release{},
			expected: []string{},
		},
		{
			name: "Single element",
			input: []configv1.Release{
				{Version: "4.19.15"},
			},
			expected: []string{"4.19.15"},
		},
		{
			name: "Random order with multiple minors and patches",
			input: []configv1.Release{
				{Version: "4.19.22"},
				{Version: "4.20.5"},
				{Version: "4.19.15"},
				{Version: "4.20.3"},
			},
			expected: []string{"4.20.5", "4.20.3", "4.19.22", "4.19.15"},
		},
		{
			name: "Versions with pre-release info",
			input: []configv1.Release{
				{Version: "4.20.0"},
				{Version: "4.20.0-rc.1"},
				{Version: "4.19.22"},
			},
			expected: []string{"4.20.0", "4.20.0-rc.1", "4.19.22"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to avoid modifying the test case
			releases := make([]configv1.Release, len(tt.input))
			copy(releases, tt.input)

			// Sort the releases
			sortReleasesByVersionDescending(releases)

			// Verify the order
			if len(releases) != len(tt.expected) {
				t.Fatalf("Expected %d releases, got %d", len(tt.expected), len(releases))
			}

			for i, expectedVersion := range tt.expected {
				if releases[i].Version != expectedVersion {
					t.Errorf("Position %d: expected version %q, got %q", i, expectedVersion, releases[i].Version)
				}
			}
		})
	}
}

// mustParse is a test helper that parses a semantic version string and panics if parsing fails.
// This is useful in test setup where version strings are known to be valid.
func mustParse(version string) semver.Version {
	v, err := semver.Parse(version)
	if err != nil {
		panic("invalid version in test: " + version)
	}
	return v
}
