// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package version

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"

	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestApplyMinimumVersionOverride(t *testing.T) {
	tests := []struct {
		name            string
		selected        *semver.Version
		activeVersions  []coreapi.HCPClusterActiveVersion
		minimumVersions []semver.Version
		expected        *semver.Version
	}{
		{
			name:            "empty minimumVersions returns selected unchanged",
			selected:        ptr.To(semver.MustParse("4.19.15")),
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			minimumVersions: nil,
			expected:        ptr.To(semver.MustParse("4.19.15")),
		},
		{
			name:            "empty minimumVersions with nil selected returns nil",
			selected:        nil,
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			minimumVersions: nil,
			expected:        nil,
		},
		{
			name:            "same minor, selected >= minimum returns selected",
			selected:        ptr.To(semver.MustParse("4.19.20")),
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.19.20")),
		},
		{
			name:            "same minor, selected equals minimum returns selected",
			selected:        ptr.To(semver.MustParse("4.19.15")),
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.19.15")),
		},
		{
			name:            "same minor, selected < minimum returns minimum",
			selected:        ptr.To(semver.MustParse("4.19.10")),
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.5")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.19.15")),
		},
		{
			name:            "same minor, selected is nil returns minimum",
			selected:        nil,
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.10")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.19.15")),
		},
		{
			name:            "higher minor minimum forces y-stream upgrade",
			selected:        ptr.To(semver.MustParse("4.19.22")),
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.20.12")},
			expected:        ptr.To(semver.MustParse("4.20.12")),
		},
		{
			name:     "multiple higher minor minimums returns the lowest one",
			selected: ptr.To(semver.MustParse("4.19.22")),
			activeVersions: []coreapi.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate},
			},
			minimumVersions: []semver.Version{
				semver.MustParse("4.21.10"),
				semver.MustParse("4.20.12"),
				semver.MustParse("4.22.8"),
			},
			expected: ptr.To(semver.MustParse("4.20.12")),
		},
		{
			name:     "multiple same-minor minimums uses the highest",
			selected: ptr.To(semver.MustParse("4.19.5")),
			activeVersions: []coreapi.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.3")), State: configv1.CompletedUpdate},
			},
			minimumVersions: []semver.Version{
				semver.MustParse("4.19.10"),
				semver.MustParse("4.19.15"),
				semver.MustParse("4.19.8"),
			},
			expected: ptr.To(semver.MustParse("4.19.15")),
		},
		{
			name:            "no active versions (install case) with minimum higher than selected returns minimum",
			selected:        ptr.To(semver.MustParse("4.19.10")),
			activeVersions:  nil,
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.19.15")),
		},
		{
			name:            "no active versions (install case) with minimum lower than selected returns selected",
			selected:        ptr.To(semver.MustParse("4.19.20")),
			activeVersions:  nil,
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.19.20")),
		},
		{
			name:     "mixed: same-minor AND higher-minor minimums, higher-minor wins",
			selected: ptr.To(semver.MustParse("4.19.10")),
			activeVersions: []coreapi.HCPClusterActiveVersion{
				{Version: ptr.To(semver.MustParse("4.19.5")), State: configv1.CompletedUpdate},
			},
			minimumVersions: []semver.Version{
				semver.MustParse("4.19.15"),
				semver.MustParse("4.20.12"),
			},
			expected: ptr.To(semver.MustParse("4.20.12")),
		},
		{
			name:            "higher minor minimum with nil selected forces upgrade",
			selected:        nil,
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.19.15")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.20.12")},
			expected:        ptr.To(semver.MustParse("4.20.12")),
		},
		{
			name:            "no active versions and nil selected returns nil",
			selected:        nil,
			activeVersions:  nil,
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        nil,
		},
		{
			name:            "minimum for lower minor than current is ignored",
			selected:        ptr.To(semver.MustParse("4.20.10")),
			activeVersions:  []coreapi.HCPClusterActiveVersion{{Version: ptr.To(semver.MustParse("4.20.5")), State: configv1.CompletedUpdate}},
			minimumVersions: []semver.Version{semver.MustParse("4.19.15")},
			expected:        ptr.To(semver.MustParse("4.20.10")),
		},
		{
			name:            "no active versions (install case) with higher minor minimum forces upgrade",
			selected:        ptr.To(semver.MustParse("4.19.10")),
			activeVersions:  nil,
			minimumVersions: []semver.Version{semver.MustParse("4.20.12")},
			expected:        ptr.To(semver.MustParse("4.20.12")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyMinimumVersionOverride(tt.selected, tt.activeVersions, tt.minimumVersions)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.True(t, result.EQ(*tt.expected), "expected %s, got %s", tt.expected.String(), result.String())
			}
		})
	}
}
