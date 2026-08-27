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
	"context"
	"fmt"
	"net/http"

	"github.com/blang/semver/v4"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// cincinnatiBestVersionSelector implements BestVersionSelector on top of the
// OpenShift update service, reusing the per-cluster desired-version controller's
// SelectControlPlaneVersion (which applies GetZStreamOffset: stable holds one
// z-stream back, other channel groups take the latest).
type cincinnatiBestVersionSelector struct{}

var _ BestVersionSelector = cincinnatiBestVersionSelector{}

// NewCincinnatiBestVersionSelector returns a BestVersionSelector backed by the
// OpenShift update service.
func NewCincinnatiBestVersionSelector() BestVersionSelector {
	return cincinnatiBestVersionSelector{}
}

// BestExactVersionForChannel returns the best exact version for the y-stream
// channel, offset by the channel group's z-stream offset. A fresh round-tripper
// is used for each call.
func (cincinnatiBestVersionSelector) BestExactVersionForChannel(ctx context.Context, yStreamChannel string) (*semver.Version, error) {
	channelGroup, minor, ok := parseYStreamChannel(yStreamChannel)
	if !ok {
		return nil, fmt.Errorf("invalid y-stream channel %q", yStreamChannel)
	}
	targetMinor, err := semver.ParseTolerant(minor)
	if err != nil {
		return nil, fmt.Errorf("invalid minor %q in channel %q: %w", minor, yStreamChannel, err)
	}
	best, err := clusterversion.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, channelGroup, targetMinor, clusterversion.GetZStreamOffset(channelGroup))
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to select best version for channel %q: %w", yStreamChannel, err))
	}
	return best, nil
}
