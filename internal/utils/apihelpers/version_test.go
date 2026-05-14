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

package apihelpers

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

func TestFindLowestAndHighestClusterVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		versions   []resourcesapi.HCPClusterActiveVersion
		wantLowest *semver.Version
		wantHigh   *semver.Version
	}{
		{
			name:       "nil ActiveVersions returns nil",
			versions:   nil,
			wantLowest: nil,
			wantHigh:   nil,
		},
		{
			name:       "empty ActiveVersions returns nil",
			versions:   []resourcesapi.HCPClusterActiveVersion{},
			wantLowest: nil,
			wantHigh:   nil,
		},
		{
			name:       "single entry returns that control plane version for both bounds",
			versions:   []resourcesapi.HCPClusterActiveVersion{{Version: resourcesapi.Ptr(semver.MustParse("4.22.0"))}},
			wantLowest: resourcesapi.Ptr(semver.MustParse("4.22.0")),
			wantHigh:   resourcesapi.Ptr(semver.MustParse("4.22.0")),
		},
		{
			name: "unsorted active versions return semantic min and max",
			versions: []resourcesapi.HCPClusterActiveVersion{
				{Version: resourcesapi.Ptr(semver.MustParse("4.20.0"))},
				{Version: resourcesapi.Ptr(semver.MustParse("4.23.0"))},
				{Version: resourcesapi.Ptr(semver.MustParse("4.22.0"))},
			},
			wantLowest: resourcesapi.Ptr(semver.MustParse("4.20.0")),
			wantHigh:   resourcesapi.Ptr(semver.MustParse("4.23.0")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLow, gotHigh := FindLowestAndHighestClusterVersion(tt.versions)
			if tt.wantLowest == nil || tt.wantHigh == nil {
				assert.Nil(t, gotLow)
				assert.Nil(t, gotHigh)
			} else {
				assert.NotNil(t, gotLow)
				assert.NotNil(t, gotHigh)
				assert.Equal(t, *tt.wantLowest, *gotLow)
				assert.Equal(t, *tt.wantHigh, *gotHigh)
			}
		})
	}
}

func TestFindLowestAndHighestNodePoolVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		versions   []resourcesapi.HCPNodePoolActiveVersion
		wantLowest *semver.Version
		wantHigh   *semver.Version
	}{
		{
			name:       "nil ActiveVersions returns nil",
			versions:   nil,
			wantLowest: nil,
			wantHigh:   nil,
		},
		{
			name:       "empty ActiveVersions returns nil",
			versions:   []resourcesapi.HCPNodePoolActiveVersion{},
			wantLowest: nil,
			wantHigh:   nil,
		},
		{
			name:       "single entry returns that version for both bounds",
			versions:   []resourcesapi.HCPNodePoolActiveVersion{{Version: resourcesapi.Ptr(semver.MustParse("4.22.0"))}},
			wantLowest: resourcesapi.Ptr(semver.MustParse("4.22.0")),
			wantHigh:   resourcesapi.Ptr(semver.MustParse("4.22.0")),
		},
		{
			name: "unsorted active versions return semantic min and max",
			versions: []resourcesapi.HCPNodePoolActiveVersion{
				{Version: resourcesapi.Ptr(semver.MustParse("4.20.0"))},
				{Version: resourcesapi.Ptr(semver.MustParse("4.23.0"))},
				{Version: resourcesapi.Ptr(semver.MustParse("4.22.0"))},
			},
			wantLowest: resourcesapi.Ptr(semver.MustParse("4.20.0")),
			wantHigh:   resourcesapi.Ptr(semver.MustParse("4.23.0")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotLow, gotHigh := FindLowestAndHighestNodePoolVersion(tt.versions)
			if tt.wantLowest == nil || tt.wantHigh == nil {
				assert.Nil(t, gotLow)
				assert.Nil(t, gotHigh)
			} else {
				assert.NotNil(t, gotLow)
				assert.NotNil(t, gotHigh)
				assert.Equal(t, *tt.wantLowest, *gotLow)
				assert.Equal(t, *tt.wantHigh, *gotHigh)
			}
		})
	}
}
