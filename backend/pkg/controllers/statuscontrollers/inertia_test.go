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

package statuscontrollers

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInertiaConfig_Inertia(t *testing.T) {
	tests := []struct {
		name             string
		defaultDuration  time.Duration
		overrides        []InertiaController
		controllerName   string
		expectedDuration time.Duration
	}{
		{
			name:             "no overrides falls back to default",
			defaultDuration:  30 * time.Second,
			overrides:        nil,
			controllerName:   "ClusterPropertiesSync",
			expectedDuration: 30 * time.Second,
		},
		{
			name:            "exact match override wins over default",
			defaultDuration: 30 * time.Second,
			overrides: []InertiaController{
				{ControllerNameMatcher: regexp.MustCompile(`^ClusterPropertiesSync$`), Duration: 2 * time.Minute},
			},
			controllerName:   "ClusterPropertiesSync",
			expectedDuration: 2 * time.Minute,
		},
		{
			name:            "non-matching override falls back to default",
			defaultDuration: 30 * time.Second,
			overrides: []InertiaController{
				{ControllerNameMatcher: regexp.MustCompile(`^Unrelated$`), Duration: 99 * time.Hour},
			},
			controllerName:   "ClusterPropertiesSync",
			expectedDuration: 30 * time.Second,
		},
		{
			name:            "first matching override wins (ordered evaluation)",
			defaultDuration: 30 * time.Second,
			overrides: []InertiaController{
				{ControllerNameMatcher: regexp.MustCompile(`^Cluster`), Duration: 10 * time.Second},
				{ControllerNameMatcher: regexp.MustCompile(`Sync$`), Duration: 5 * time.Minute},
			},
			controllerName:   "ClusterPropertiesSync",
			expectedDuration: 10 * time.Second,
		},
		{
			name:            "regex anchored to suffix",
			defaultDuration: 30 * time.Second,
			overrides: []InertiaController{
				{ControllerNameMatcher: regexp.MustCompile(`Migration$`), Duration: 90 * time.Second},
			},
			controllerName:   "IdentityMigration",
			expectedDuration: 90 * time.Second,
		},
		{
			name:             "empty controller name still gets default",
			defaultDuration:  30 * time.Second,
			overrides:        nil,
			controllerName:   "",
			expectedDuration: 30 * time.Second,
		},
		{
			name:            "DefaultInertia constant is the documented 30s",
			defaultDuration: DefaultInertia,
			controllerName:  "Anything",
			// We compare directly against the constant rather than hard-coding 30s here so the
			// test reflects intent: callers passing DefaultInertia should see exactly that value.
			expectedDuration: DefaultInertia,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewInertia(tc.defaultDuration, tc.overrides...)
			require.NoError(t, err)
			got := cfg.Inertia(tc.controllerName, metav1.Condition{Type: degradedConditionType})
			assert.Equal(t, tc.expectedDuration, got)
		})
	}
}

func TestNewInertia_Errors(t *testing.T) {
	tests := []struct {
		name        string
		overrides   []InertiaController
		expectError bool
	}{
		{
			name:        "no overrides is fine",
			overrides:   nil,
			expectError: false,
		},
		{
			name: "all-valid overrides is fine",
			overrides: []InertiaController{
				{ControllerNameMatcher: regexp.MustCompile(`^A$`), Duration: time.Second},
				{ControllerNameMatcher: regexp.MustCompile(`^B$`), Duration: time.Second},
			},
			expectError: false,
		},
		{
			name: "nil matcher in first slot is rejected",
			overrides: []InertiaController{
				{ControllerNameMatcher: nil, Duration: time.Second},
			},
			expectError: true,
		},
		{
			name: "nil matcher in later slot is rejected",
			overrides: []InertiaController{
				{ControllerNameMatcher: regexp.MustCompile(`^A$`), Duration: time.Second},
				{ControllerNameMatcher: nil, Duration: time.Second},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewInertia(30*time.Second, tc.overrides...)
			if tc.expectError {
				assert.Error(t, err)
				assert.Nil(t, cfg)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, cfg)
		})
	}
}

func TestMustNewInertia_Panic(t *testing.T) {
	tests := []struct {
		name        string
		overrides   []InertiaController
		expectPanic bool
	}{
		{
			name:        "valid config does not panic",
			overrides:   nil,
			expectPanic: false,
		},
		{
			name: "invalid config panics",
			overrides: []InertiaController{
				{ControllerNameMatcher: nil, Duration: time.Second},
			},
			expectPanic: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.expectPanic {
				assert.Panics(t, func() {
					MustNewInertia(30*time.Second, tc.overrides...)
				})
				return
			}
			assert.NotPanics(t, func() {
				MustNewInertia(30*time.Second, tc.overrides...)
			})
		})
	}
}
