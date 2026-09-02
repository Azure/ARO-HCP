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

package compute

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestLookupProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		wantOK  bool
	}{
		{name: "known profile", profile: ProfileProduction, wantOK: true},
		{name: "unknown profile", profile: "does-not-exist", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, ok := LookupProfile(tt.profile)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.NotEmpty(t, profile.Tiers, "expected known profile to have tiers")
			}
		})
	}
}

func TestValidProfileNames(t *testing.T) {
	names := ValidProfileNames()
	assert.Equal(t, []string{ProfileCI, ProfileDevelopment, ProfileIntegration, ProfileProduction}, names, "expected sorted, deduplicated profile names")
}

func TestTierFamilies(t *testing.T) {
	tiers := []TierConfig{
		{FamilyPriority: []VMFamily{"standardEDSv6Family", "standardEDSv5Family"}},
		{FamilyPriority: []VMFamily{"standardEDSv5Family", "standardDDSv6Family"}},
	}

	got := TierFamilies(tiers)
	want := sets.New[VMFamily]("standardEDSv6Family", "standardEDSv5Family", "standardDDSv6Family")
	assert.True(t, got.Equal(want), "got %v, want %v", got, want)
}

func TestValidateProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		wantErr bool
	}{
		{
			name: "all tiers meet minimum cores",
			profile: Profile{
				Tiers: []TierConfig{{Cores: minCoresPerTier}, {Cores: minCoresPerTier + 4}},
			},
		},
		{
			name: "no tiers is valid",
			profile: Profile{
				Tiers: nil,
			},
		},
		{
			name: "tier below minimum cores",
			profile: Profile{
				Tiers: []TierConfig{{Cores: minCoresPerTier - 1}},
			},
			wantErr: true,
		},
		{
			name: "unique families are valid",
			profile: Profile{
				Tiers: []TierConfig{{Cores: minCoresPerTier, FamilyPriority: []VMFamily{"standardEDSv6Family", "standardEDSv5Family"}}},
			},
		},
		{
			name: "duplicate family within a tier",
			profile: Profile{
				Tiers: []TierConfig{{Cores: minCoresPerTier, FamilyPriority: []VMFamily{"standardEDSv6Family", "standardEDSv6Family"}}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile(tt.profile)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestProfilesValid asserts every built-in profile satisfies ValidateProfile.
// This replaces the former init() panic: invalid hardcoded profiles now fail
// the build via test rather than crashing at process/import time.
func TestProfilesValid(t *testing.T) {
	for name, profile := range profiles {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, ValidateProfile(profile), "built-in profile %q is invalid", name)
		})
	}
}
