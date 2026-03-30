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

package controllerutils

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewInertia(t *testing.T) {
	t.Run("returns error for nil matcher", func(t *testing.T) {
		_, err := NewInertia(5*time.Minute, InertiaCondition{
			ControllerNameMatcher: nil,
			Duration:              2 * time.Minute,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "condition 0 has a nil ControllerNameMatcher")
	})

	t.Run("succeeds with valid conditions", func(t *testing.T) {
		config, err := NewInertia(5*time.Minute, InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Validation"),
			Duration:              1 * time.Minute,
		})
		require.NoError(t, err)
		require.NotNil(t, config)
	})

	t.Run("succeeds with no conditions", func(t *testing.T) {
		config, err := NewInertia(5 * time.Minute)
		require.NoError(t, err)
		require.NotNil(t, config)
	})
}

func TestMustNewInertia(t *testing.T) {
	t.Run("panics on nil matcher", func(t *testing.T) {
		require.Panics(t, func() {
			MustNewInertia(5*time.Minute, InertiaCondition{
				ControllerNameMatcher: nil,
				Duration:              2 * time.Minute,
			})
		})
	})

	t.Run("succeeds with valid conditions", func(t *testing.T) {
		config := MustNewInertia(5*time.Minute, InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Validation"),
			Duration:              1 * time.Minute,
		})
		require.NotNil(t, config)
	})
}

func TestInertiaConfig_Inertia(t *testing.T) {
	config := MustNewInertia(5*time.Minute,
		InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Validation"),
			Duration:              1 * time.Minute,
		},
		InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Upgrade"),
			Duration:              10 * time.Minute,
		},
	)

	tests := []struct {
		name           string
		controllerName string
		wantDuration   time.Duration
	}{
		{
			name:           "matches first condition",
			controllerName: "ValidationNetworkCheck",
			wantDuration:   1 * time.Minute,
		},
		{
			name:           "matches second condition",
			controllerName: "UpgradeControlPlane",
			wantDuration:   10 * time.Minute,
		},
		{
			name:           "falls back to default",
			controllerName: "ClusterPropertiesSync",
			wantDuration:   5 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.Inertia(tt.controllerName)
			require.Equal(t, tt.wantDuration, got)
		})
	}
}

func TestInertiaConfig_FirstMatchWins(t *testing.T) {
	config := MustNewInertia(5*time.Minute,
		InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile(".*"),
			Duration:              1 * time.Minute,
		},
		InertiaCondition{
			ControllerNameMatcher: regexp.MustCompile("^Specific"),
			Duration:              10 * time.Minute,
		},
	)

	// The catch-all should match first
	got := config.Inertia("SpecificController")
	require.Equal(t, 1*time.Minute, got)
}
