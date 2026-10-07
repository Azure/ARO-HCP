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

package versionpolicy

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestNormalizeProfile(t *testing.T) {
	for _, group := range []string{"stable", "fast", "candidate", "nightly"} {
		for _, version := range []string{"4.21", "4.21.5", "4.21.0-0.nightly", "v4.21.3"} {
			profile, err := NormalizeProfile(coreapi.VersionProfile{ChannelGroup: group, ID: version})
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(coreapi.VersionProfile{ChannelGroup: group, ID: "4.21"}, profile))
		}
	}
	for _, profile := range []coreapi.VersionProfile{{ID: "bad", ChannelGroup: "stable"}, {ID: "4.21", ChannelGroup: "unknown"}, {}} {
		_, err := NormalizeProfile(profile)
		require.Error(t, err)
	}
}

func TestReleaseLineFloor(t *testing.T) {
	for _, tc := range []struct {
		version, floor string
		want           bool
	}{
		{"4.20", "4.20", true},
		{"4.20.0-rc.1", "4.20", true},
		{"4.19.999", "4.20", false},
		{"4.9", "4.20", false},
		{"5.0", "4.99", true},
		{"6.0", "5.99", true},
		{"invalid", "4.20", false},
		{"4.20", "invalid", false},
	} {
		t.Run(tc.version+"/"+tc.floor, func(t *testing.T) {
			require.Equal(t, tc.want, AtLeast(tc.version, tc.floor))
		})
	}
	require.True(t, AtLeast(MinimumPublicVersion, MinimumBackendVersion), "backend floor must not advance ahead of public admission")
}

func TestChannelProfiles(t *testing.T) {
	for _, tc := range []struct {
		channel, name string
		profile       coreapi.VersionProfile
	}{
		{"stable-4.20", "4.20", coreapi.VersionProfile{ID: "4.20", ChannelGroup: "stable"}},
		{"fast-5.0", "5.0-fast", coreapi.VersionProfile{ID: "5.0", ChannelGroup: "fast"}},
		{"candidate-6.0", "6.0-candidate", coreapi.VersionProfile{ID: "6.0", ChannelGroup: "candidate"}},
		{"nightly-4.23", "4.23-nightly", coreapi.VersionProfile{ID: "4.23", ChannelGroup: "nightly"}},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			profile, err := ProfileForChannel(tc.channel)
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(tc.profile, profile))
			channel, err := ChannelForProfile(profile)
			require.NoError(t, err)
			require.Equal(t, tc.channel, channel)
			require.Equal(t, tc.name, PublicName(profile))
		})
	}
	for _, channel := range []string{"eus-4.20", "stable", "stable-4", "stable-4.20.0", "stable-04.20", "stable-4.20-rc.1", "stable-4.20+build"} {
		_, err := ProfileForChannel(channel)
		require.Error(t, err, "reject noncanonical graph channel %q", channel)
	}
	channel, err := ChannelForProfile(coreapi.VersionProfile{ID: "4.20.0-rc.1", ChannelGroup: "candidate"})
	require.NoError(t, err)
	require.Equal(t, "candidate-4.20", channel)
	_, err = ChannelForProfile(coreapi.VersionProfile{ID: "bad", ChannelGroup: "stable"})
	require.Error(t, err)
	_, err = ChannelForProfile(coreapi.VersionProfile{ID: "4.20", ChannelGroup: "unknown"})
	require.Error(t, err)
	_, err = ProfileForChannel("eus-4.20")
	require.ErrorIs(t, err, ErrUnsupportedChannelGroup)
	_, err = ProfileForChannel("stable-invalid")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrUnsupportedChannelGroup)
}
