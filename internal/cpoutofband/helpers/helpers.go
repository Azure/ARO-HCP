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

package helpers

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/cpoutofband"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// RecognizedVersionForOutOfBandPin returns the recognized control-plane version
// Cluster Service is told while a cluster's desired control-plane version is an
// out-of-band pin: the version the cluster would run without the pin. For the
// nightly channel group that is the exact nightly build set on the cluster
// (ExperimentalFeatures.ControlPlaneExactVersion) — nightly builds are not
// published to the Cincinnati graph, so the channel rollout never selects a
// best version for a nightly channel — and for every other channel group it is
// the channel's best exact version, which channelBestExactVersion resolves. It
// also returns an error when the channel's best version has not been calculated.
func RecognizedVersionForOutOfBandPin(ctx context.Context, rolloutLister fleetlisters.ControlPlaneVersionRolloutLister, cluster *coreapi.Cluster, desiredVersion semver.Version) (*semver.Version, error) {
	channelGroup := cluster.CustomerProperties.Version.ChannelGroup
	if channelGroup == metadataapi.ChannelGroupNightly {
		exact := cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion
		if exact == nil {
			return nil, utils.TrackError(fmt.Errorf("nightly cluster %s has no exact nightly version set to tell Cluster Service for the out-of-band pin", cluster.Name))
		}

		if cpoutofband.IsOutOfBandControlPlaneVersion(exact.String()) {
			return nil, utils.TrackError(fmt.Errorf("nightly version %s of nightly cluster %s is itself out of band. There is no recognized version to tell Cluster Service for the out-of-band pin", exact.String(), cluster.Name))
		}
		return exact, nil
	}
	return channelBestExactVersion(ctx, rolloutLister, channelGroup, desiredVersion)
}

// channelBestExactVersion returns the fleet's recognized version for the
// y-stream channel derived from channelGroup and version's minor: the version
// the channel's rollout would drive a cluster toward without the out-of-band
// pin. It mirrors the Forced Cluster Desired Version pin-release logic:
// if the rollout has an active channel pin (hasActiveRolloutPin) it returns
// PinnedVersion.ExactVersion instead of BestExactVersion, holding the pin
// until BestExactVersion strictly exceeds UntilExactVersion. An active pin
// that is itself out-of-band is an error — the controller cannot fall back
// to BestExactVersion because the pin exists precisely to avoid it. It
// returns an error when the rollout does not exist, when a pin is active but
// out-of-band, or when neither a pin nor a BestExactVersion is available.
func channelBestExactVersion(ctx context.Context, rolloutLister fleetlisters.ControlPlaneVersionRolloutLister, channelGroup string, version semver.Version) (*semver.Version, error) {
	if len(channelGroup) == 0 {
		return nil, utils.TrackError(fmt.Errorf("cluster has no channel group"))
	}

	channel := yStreamChannel(channelGroup, minorString(version))

	rollout, err := rolloutLister.Get(ctx, channel)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, utils.TrackError(fmt.Errorf("ControlPlaneVersionRollout %q not found", channel))
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", channel, err))
	}

	if hasActiveRolloutPin(&rollout.Spec) {
		// We do not support a cluster-level control plane out of band pin and at the same time a channel-level pin that is control plane out of band.
		// In case this happens we don't want to fall back to the channel's best exact version rollout because the reason the channel level pin is there
		// is to prevent using the channel level best exact version.
		if cpoutofband.IsOutOfBandControlPlaneVersion(rollout.Spec.PinnedVersion.ExactVersion.String()) {
			return nil, utils.TrackError(fmt.Errorf("pinned version %s of ControlPlaneVersionRollout %q is itself out of band. There is no recognized version to tell Cluster Service for the out-of-band pin", rollout.Spec.PinnedVersion.ExactVersion.String(), channel))
		}
		// If there is an active channel-level pin that is not an out of band version we return the channel-level pin.
		return rollout.Spec.PinnedVersion.ExactVersion, nil
	}

	if rollout.Spec.BestExactVersion == nil {
		return nil, utils.TrackError(fmt.Errorf("ControlPlaneVersionRollout %q has no best exact version", channel))
	}

	return rollout.Spec.BestExactVersion, nil
}

func minorString(v semver.Version) string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func yStreamChannel(channelGroup, minor string) string {
	return channelGroup + "-" + minor
}

// hasActiveRolloutPin distinguishes an authoritative pin from normal selection,
// including when the selected version would move a cluster backwards.
func hasActiveRolloutPin(s *fleetapi.ControlPlaneVersionRolloutSpec) bool {
	pin := s.PinnedVersion
	return pin.ExactVersion != nil && (pin.UntilExactVersion == nil || s.BestExactVersion == nil || !s.BestExactVersion.GT(*pin.UntilExactVersion))
}
