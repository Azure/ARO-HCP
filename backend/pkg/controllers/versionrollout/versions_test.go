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

package versionrollout

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestEarliestActiveVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		active []coreapi.HCPClusterActiveVersion
		want   *semver.Version
	}{
		{name: "empty", active: nil, want: nil},
		{name: "single completed", active: []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, want: v("4.21.6")},
		{
			name:   "upgrade in flight returns oldest completed base",
			active: []coreapi.HCPClusterActiveVersion{partial("4.21.6"), completed("4.21.4")},
			want:   v("4.21.4"),
		},
		{
			name:   "sequential upgrades return oldest base",
			active: []coreapi.HCPClusterActiveVersion{partial("4.21.8"), partial("4.21.6"), completed("4.21.4")},
			want:   v("4.21.4"),
		},
		{
			name:   "skips nil versions",
			active: []coreapi.HCPClusterActiveVersion{completed("4.21.6"), {Version: nil}},
			want:   v("4.21.6"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := earliestActiveVersion(tc.active)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.NotNil(t, got)
			assert.True(t, got.EQ(*tc.want), "got %v want %v", got, tc.want)
		})
	}
}

func TestMaxVersion(t *testing.T) {
	t.Parallel()
	assert.Equal(t, v("4.21.6"), maxVersion(v("4.21.6"), nil))
	assert.Equal(t, v("4.21.6"), maxVersion(nil, v("4.21.6")))
	assert.Nil(t, maxVersion(nil, nil))
	assert.True(t, maxVersion(v("4.21.6"), v("4.21.4")).EQ(*v("4.21.6")))
	assert.True(t, maxVersion(v("4.21.4"), v("4.21.6")).EQ(*v("4.21.6")))
	assert.True(t, maxVersion(v("4.21.6"), v("4.21.6")).EQ(*v("4.21.6")))
}

func TestPercentOfCeil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int64(0), percentOfCeil(0, 100))
	assert.Equal(t, int64(0), percentOfCeil(5, 0))
	assert.Equal(t, int64(5), percentOfCeil(5, 100))
	assert.Equal(t, int64(1), percentOfCeil(5, 10), "5%% of 10 rounds up to 1")
	assert.Equal(t, int64(1), percentOfCeil(5, 1), "5%% of 1 rounds up to 1")
	assert.Equal(t, int64(3), percentOfCeil(5, 50), "5%% of 50 rounds up to 3")
}

func TestMinorStringAndChannel(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "4.21", minorString(semver.MustParse("4.21.6")))
	assert.Equal(t, "5.0", minorString(semver.MustParse("5.0.0-ec.4")))
	assert.Equal(t, "stable-4.21", yStreamChannel("stable", "4.21"))

	group, minor, ok := parseYStreamChannel("stable-4.21")
	assert.True(t, ok)
	assert.Equal(t, "stable", group)
	assert.Equal(t, "4.21", minor)

	_, _, ok = parseYStreamChannel("invalid")
	assert.False(t, ok)
}

func TestClusterMinor(t *testing.T) {
	t.Parallel()

	// desired takes precedence
	spc := newTestSPC("c1", v("4.22.0"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil)
	m, ok := clusterMinor(spc)
	assert.True(t, ok)
	assert.Equal(t, "4.22", m)

	// falls back to earliest active
	spc = newTestSPC("c1", nil, []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil)
	m, ok = clusterMinor(spc)
	assert.True(t, ok)
	assert.Equal(t, "4.21", m)

	// neither
	spc = newTestSPC("c1", nil, nil, nil)
	_, ok = clusterMinor(spc)
	assert.False(t, ok)
}
