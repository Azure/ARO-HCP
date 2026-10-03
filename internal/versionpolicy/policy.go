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

// Package versionpolicy defines discovery and retirement policy shared by the
// public API and backend. These floors are advanced separately during retirement.
package versionpolicy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	// MinimumPublicVersion gates advertisement and new customer version requests.
	MinimumPublicVersion = "4.20"
	// MinimumBackendVersion is raised only after retiring clusters have drained.
	// Referenced channels below this floor must continue to be processed.
	MinimumBackendVersion = "4.20"
)

// AtLeast compares the major and minor components of versions.
func AtLeast(version, minimum string) bool {
	v, err := semver.ParseTolerant(version)
	if err != nil {
		return false
	}
	floor, err := semver.ParseTolerant(minimum)
	if err != nil {
		return false
	}
	return v.Major > floor.Major || (v.Major == floor.Major && v.Minor >= floor.Minor)
}

var ErrUnsupportedChannelGroup = errors.New("unsupported channel group")

func AllowedChannelGroup(group string) bool {
	return metadataapi.AllowedChannelGroupsWithExperimentalFlag.Has(group)
}

// ProfileForChannel requires canonical Cincinnati channel names of the form group-major.minor.
func ProfileForChannel(channel string) (coreapi.VersionProfile, error) {
	group, minor, ok := strings.Cut(channel, "-")
	if !AllowedChannelGroup(group) {
		return coreapi.VersionProfile{}, fmt.Errorf("%w in channel %q", ErrUnsupportedChannelGroup, channel)
	}
	if !ok {
		return coreapi.VersionProfile{}, fmt.Errorf("invalid version channel %q", channel)
	}
	v, err := semver.ParseTolerant(minor)
	if err != nil || minor != fmt.Sprintf("%d.%d", v.Major, v.Minor) {
		return coreapi.VersionProfile{}, fmt.Errorf("invalid version channel %q", channel)
	}
	return coreapi.VersionProfile{ID: minor, ChannelGroup: group}, nil
}

// NormalizeProfile maps exact versions, including prereleases, to a minor version
// while preserving the channel group.
func NormalizeProfile(profile coreapi.VersionProfile) (coreapi.VersionProfile, error) {
	if !AllowedChannelGroup(profile.ChannelGroup) {
		return coreapi.VersionProfile{}, fmt.Errorf("unsupported channel group %q", profile.ChannelGroup)
	}
	v, err := semver.ParseTolerant(profile.ID)
	if err != nil {
		return coreapi.VersionProfile{}, fmt.Errorf("invalid version %q: %w", profile.ID, err)
	}
	profile.ID = fmt.Sprintf("%d.%d", v.Major, v.Minor)
	return profile, nil
}

func ChannelForProfile(profile coreapi.VersionProfile) (string, error) {
	profile, err := NormalizeProfile(profile)
	if err != nil {
		return "", err
	}
	return profile.ChannelGroup + "-" + profile.ID, nil
}

func PublicName(profile coreapi.VersionProfile) string {
	if profile.ChannelGroup == metadataapi.ChannelGroupStable {
		return profile.ID
	}
	return profile.ID + "-" + profile.ChannelGroup
}
