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

package version

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// stableChannelGroup is the OpenShift channel group of generally-available stable releases.
const stableChannelGroup = "stable"

// defaultControlPlaneVersionOffset is the offset from the tip of a channel used when selecting a
// desired control plane version. Offset 0 selects the most recent (tip) release in the channel.
const defaultControlPlaneVersionOffset uint = 0

// GetZStreamOffset returns the offset from the tip of a channel to use when selecting a z-stream
// control plane version (offset 0 = the most recent release). The stable channel group uses the
// penultimate release (offset 1) to avoid selecting a brand-new stable z-stream immediately; all
// other channel groups use the tip.
func GetZStreamOffset(channel string) uint {
	if channel == stableChannelGroup {
		return 1
	}
	return defaultControlPlaneVersionOffset
}

// SelectControlPlaneVersion resolves the desired control plane version for a minor by querying the
// OpenShift update service (Cincinnati graph API) for that minor's channel and selecting the release
// at the given offset from the tip (offset 0 = most recent release). It bridges
// controlplaneversion.SelectControlPlaneVersion, which returns a configv1.Release, into the
// *semver.Version used by callers. It is exported for reuse by the fleet control-plane version
// rollout's best-version selector.
//
// This must not be called for the nightly channel group: the graph API does not serve nightly
// builds.
func SelectControlPlaneVersion(ctx context.Context, roundTripper controlplaneversion.RoundTrip, channelGroup string, minorVersion semver.Version, offset uint) (*semver.Version, error) {
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, minorVersion.Major, minorVersion.Minor)
	release, err := controlplaneversion.SelectControlPlaneVersion(ctx, roundTripper, nil, channel, offset)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	selected, err := semver.ParseTolerant(release.Version)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse selected control plane version %q for channel %s: %w", release.Version, channel, err))
	}
	return &selected, nil
}
