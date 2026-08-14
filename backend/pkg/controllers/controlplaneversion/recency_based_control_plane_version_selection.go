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

package controlplaneversion

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"
)

// ARO-HCP specific user agent; put whatever you like in here to identify yourself.
var userAgent = "AROHCPFixme/0.1"

// SelectControlPlaneVersion retrieves a release from a chosen
// OpenShift update service channel with a chosen offset.  For
// example, the most recent release in the fast-4.20 channel.  Or the
// penultimate release in the stable-4.22 channel.
func SelectControlPlaneVersion(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, channel string, offset uint) (*configv1.Release, error) {
	releases, updateService, err := cincinnati(ctx, roundTripper, updateService, userAgent, channel)
	if err != nil {
		return nil, err
	}

	// The channel's graph can include releases from earlier minors (e.g. a
	// stable-4.21 graph also lists 4.20.x nodes). Filter to the channel's own
	// major.minor so an offset never selects a release from an older minor.
	releases, err = filterReleasesToChannelMinor(releases, channel)
	if err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found in %s", updateService)
	}
	if int(offset) >= len(releases) {
		return nil, fmt.Errorf("%d releases found in %s, which is not enough for the requested %d offset", len(releases), updateService, offset)
	}
	return &releases[offset], nil
}

// filterReleasesToChannelMinor returns only the releases whose major.minor matches the channel's own
// major.minor. A channel's update graph can include releases from earlier minors (e.g. a stable-4.21
// graph also lists 4.20.x nodes); dropping them keeps an offset from selecting a release from an
// older minor. The channel is "<group>-<major>.<minor>" (e.g. "stable-4.21"). Releases whose version
// does not parse are skipped.
func filterReleasesToChannelMinor(releases []configv1.Release, channel string) ([]configv1.Release, error) {
	channelVersion, err := semver.ParseTolerant(channel[strings.LastIndex(channel, "-")+1:])
	if err != nil {
		return nil, fmt.Errorf("parse major.minor from channel %q: %w", channel, err)
	}
	matchingReleases := make([]configv1.Release, 0, len(releases))
	for _, release := range releases {
		releaseVersion, err := semver.ParseTolerant(release.Version)
		if err != nil {
			continue
		}
		if releaseVersion.Major == channelVersion.Major && releaseVersion.Minor == channelVersion.Minor {
			matchingReleases = append(matchingReleases, release)
		}
	}
	return matchingReleases, nil
}
