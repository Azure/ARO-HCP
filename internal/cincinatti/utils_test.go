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

package cincinatti

import (
	"errors"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

func TestParseCincinnatiChannel(t *testing.T) {
	tests := []struct {
		name                 string
		channel              string
		expectedChannelGroup string
		expectedMinorVersion string
		expectError          bool
	}{
		{
			name:                 "Valid stable channel",
			channel:              "stable-4.20",
			expectedChannelGroup: "stable",
			expectedMinorVersion: "4.20",
			expectError:          false,
		},
		{
			name:                 "Valid fast channel",
			channel:              "fast-4.19",
			expectedChannelGroup: "fast",
			expectedMinorVersion: "4.19",
			expectError:          false,
		},
		{
			name:                 "Valid nightly channel",
			channel:              "nightly-4.21",
			expectedChannelGroup: "nightly",
			expectedMinorVersion: "4.21",
			expectError:          false,
		},
		{
			name:                 "Valid candidate channel",
			channel:              "candidate-4.18",
			expectedChannelGroup: "candidate",
			expectedMinorVersion: "4.18",
			expectError:          false,
		},
		{
			name:        "Invalid format",
			channel:     "stable.4.20",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channelGroup, minorVersion, err := ParseCincinnatiChannel(tt.channel)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			assertParseSuccess(t, err, channelGroup, minorVersion, tt.expectedChannelGroup, tt.expectedMinorVersion)
		})
	}
}

func assertParseSuccess(t *testing.T, err error, channelGroup, minorVersion, expectedChannelGroup, expectedMinorVersion string) {
	t.Helper()

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if channelGroup != expectedChannelGroup {
		t.Errorf("channelGroup = %q, expected %q", channelGroup, expectedChannelGroup)
	}

	if minorVersion != expectedMinorVersion {
		t.Errorf("minorVersion = %q, expected %q", minorVersion, expectedMinorVersion)
	}
}

func TestIsCincinnatiVersionNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Cincinnati VersionNotFound error",
			err:      &cincinnati.Error{Reason: "VersionNotFound"},
			expected: true,
		},
		{
			name:     "Cincinnati error with different reason",
			err:      &cincinnati.Error{Reason: "InvalidChannel"},
			expected: false,
		},
		{
			name:     "Non-Cincinnati error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "Wrapped Cincinnati VersionNotFound error",
			err:      errors.Join(errors.New("wrapper"), &cincinnati.Error{Reason: "VersionNotFound"}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCincinnatiVersionNotFoundError(tt.err)
			if result != tt.expected {
				t.Errorf("IsCincinnatiVersionNotFoundError() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestExcludeConditionalUpdatesWithAzureOrHyperShiftRisks(t *testing.T) {
	const (
		version1 = "4.19.1"
		version2 = "4.19.2"
		version3 = "4.19.3"
		version4 = "4.19.4"
	)

	tests := []struct {
		name           string
		input          []configv1.ConditionalUpdate
		expectedOutput []configv1.ConditionalUpdate
	}{
		{
			name:           "Empty input",
			input:          []configv1.ConditionalUpdate{},
			expectedOutput: []configv1.ConditionalUpdate{},
		},
		{
			name: "Updates with no risks",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks:   []configv1.ConditionalUpdateRisk{},
				},
				{
					Release: configv1.Release{Version: version2},
					Risks:   []configv1.ConditionalUpdateRisk{},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks:   []configv1.ConditionalUpdateRisk{},
				},
				{
					Release: configv1.Release{Version: version2},
					Risks:   []configv1.ConditionalUpdateRisk{},
				},
			},
		},
		{
			name: "Update with Azure risk",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Known issue with azure disk provisioning"},
					},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{},
		},
		{
			name: "Update with HyperShift risk",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Bug affects hypershift clusters"},
					},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{},
		},
		{
			name: "Update with HCP risk",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Known issue with hcp networking"},
					},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{},
		},
		{
			name: "Update with hosted control plane risk",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Issue affects hosted control plane clusters"},
					},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{},
		},
		{
			name: "Update with multiple platform risks",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Issue with azure storage"},
						{Message: "Problem with hypershift control plane"},
					},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{},
		},
		{
			name: "Update with other platform risks",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Known issue with AWS EBS volumes"},
						{Message: "Problem with GCP persistent disks"},
					},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Known issue with AWS EBS volumes"},
						{Message: "Problem with GCP persistent disks"},
					},
				},
			},
		},
		{
			name: "Mixed updates with various risks",
			input: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version1},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Azure networking issue"},
					},
				},
				{
					Release: configv1.Release{Version: version2},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Known issue with ovn-kubernetes"},
					},
				},
				{
					Release: configv1.Release{Version: version3},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "HCP upgrade issue"},
					},
				},
				{
					Release: configv1.Release{Version: version4},
					Risks:   []configv1.ConditionalUpdateRisk{},
				},
			},
			expectedOutput: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: version2},
					Risks: []configv1.ConditionalUpdateRisk{
						{Message: "Known issue with ovn-kubernetes"},
					},
				},
				{
					Release: configv1.Release{Version: version4},
					Risks:   []configv1.ConditionalUpdateRisk{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExcludeConditionalUpdatesWithAzureOrHyperShiftRisks(tt.input)

			assertConditionalUpdatesMatch(t, result, tt.expectedOutput)
		})
	}
}

func assertConditionalUpdatesMatch(t *testing.T, result, expected []configv1.ConditionalUpdate) {
	t.Helper()

	if len(result) != len(expected) {
		t.Fatalf("Expected %d updates, got %d", len(expected), len(result))
	}

	for i := range result {
		if result[i].Release.Version != expected[i].Release.Version {
			t.Errorf("Result[%d] version = %s, expected %s", i, result[i].Release.Version, expected[i].Release.Version)
		}
		if len(result[i].Risks) != len(expected[i].Risks) {
			t.Errorf("Result[%d] risks count = %d, expected %d", i, len(result[i].Risks), len(expected[i].Risks))
		}
	}
}
