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

package rollout

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/blang/semver/v4"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

const cincinnatiRequestTimeout = 30 * time.Second

// cincinnatiBestVersionSelector implements BestVersionSelector on top of the
// OpenShift update service, reusing the per-cluster desired-version controller's
// SelectControlPlaneVersion (which applies GetZStreamOffset: stable holds one
// z-stream back, other channel groups take the latest).
type cincinnatiBestVersionSelector struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

var _ BestVersionSelector = cincinnatiBestVersionSelector{}

// NewCincinnatiBestVersionSelector returns a BestVersionSelector backed by the
// OpenShift update service.
func NewCincinnatiBestVersionSelector() BestVersionSelector {
	return cincinnatiBestVersionSelector{roundTrip: http.DefaultTransport.RoundTrip}
}

// BestExactVersionForProfile returns the best exact version for a canonical
// minor version and channel group, offset by the channel group's z-stream offset.
// The graph request, including retries and reading the body, is bounded by
// cincinnatiRequestTimeout.
// Selection currently uses recency only.
// TODO: filter platform/control-plane risks from Cincinnati conditional updates.
func (s cincinnatiBestVersionSelector) BestExactVersionForProfile(ctx context.Context, profile coreapi.VersionProfile) (*semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)
	normalized, err := versionpolicy.NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	if normalized != profile {
		return nil, fmt.Errorf("invalid rollout minor version %q: must be canonical major.minor", profile.ID)
	}
	channelGroup := profile.ChannelGroup
	// Nightly builds are assigned through the experimental exact-version
	// override; Cincinnati does not publish a graph for them.
	if channelGroup == "nightly" {
		return nil, nil
	}
	targetMinor, err := semver.Parse(profile.ID + ".0")
	if err != nil {
		return nil, fmt.Errorf("invalid minor version %q: %w", profile.ID, err)
	}
	logger.Info("Querying upgrade graph for best version", "channelGroup", channelGroup, "targetMinor", targetMinor.String(), "zStreamOffset", clusterversion.GetZStreamOffset(channelGroup))
	ctx, cancel := context.WithTimeout(ctx, cincinnatiRequestTimeout)
	defer cancel()
	best, err := clusterversion.SelectControlPlaneVersion(ctx, s.roundTrip, channelGroup, targetMinor, clusterversion.GetZStreamOffset(channelGroup))
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to select best version for profile %+v: %w", profile, err))
	}
	return best, nil
}
