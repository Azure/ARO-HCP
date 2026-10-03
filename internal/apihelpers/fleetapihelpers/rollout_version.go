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

package fleetapihelpers

import (
	"fmt"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/versionpolicy"
)

// ValidateRolloutVersion checks the structured identity, including its agreement
// with the document name. A rollout always describes a canonical minor version
// and channel group.
func ValidateRolloutVersion(rollout *fleetapi.ControlPlaneVersionRollout) error {
	if rollout == nil || rollout.ResourceID == nil || rollout.ResourceID.Name == "" {
		return fmt.Errorf("rollout without resource ID")
	}
	profile := rollout.Spec.Version
	if !versionpolicy.AllowedChannelGroup(profile.ChannelGroup) {
		return fmt.Errorf("unsupported channel group %q", profile.ChannelGroup)
	}
	v, err := semver.Parse(profile.ID + ".0")
	if err != nil || profile.ID != fmt.Sprintf("%d.%d", v.Major, v.Minor) {
		return fmt.Errorf("invalid rollout minor version %q: must be canonical major.minor", profile.ID)
	}
	channel := profile.ChannelGroup + "-" + profile.ID
	if rollout.ResourceID.Name != channel {
		return fmt.Errorf("rollout name %q does not match spec.version channel %q", rollout.ResourceID.Name, channel)
	}
	return nil
}

// NormalizeRolloutVersion adapts persisted rollouts that predate spec.version.
// It returns a deep copy only when backfill is needed, or nil for an already
// valid rollout. Partially populated or mismatched profiles are errors.
// Callers persist the result with Replace(ctx, desired, existing, nil), retaining
// the original ETag and all selection/status state. Read adapters can use the
// result transiently; decoding must leave missing profiles visible to backfill.
func NormalizeRolloutVersion(rollout *fleetapi.ControlPlaneVersionRollout) (*fleetapi.ControlPlaneVersionRollout, error) {
	if rollout == nil || rollout.ResourceID == nil {
		return nil, fmt.Errorf("rollout without resource ID")
	}
	if rollout.Spec.Version != (coreapi.VersionProfile{}) {
		return nil, ValidateRolloutVersion(rollout)
	}
	profile, err := versionpolicy.ProfileForChannel(rollout.ResourceID.Name)
	if err != nil {
		return nil, fmt.Errorf("backfill rollout version: %w", err)
	}
	desired := rollout.DeepCopy()
	desired.Spec.Version = profile
	return desired, nil
}

// RolloutVersionForRead returns a validated profile at a persisted-document read
// boundary. Legacy identity is adapted without changing the document, so ordinary
// writers preserve its shape and the seeder can still detect pending backfill.
func RolloutVersionForRead(rollout *fleetapi.ControlPlaneVersionRollout) (coreapi.VersionProfile, error) {
	normalized, err := NormalizeRolloutVersion(rollout)
	if err != nil {
		return coreapi.VersionProfile{}, err
	}
	if normalized != nil {
		return normalized.Spec.Version, nil
	}
	return rollout.Spec.Version, nil
}
