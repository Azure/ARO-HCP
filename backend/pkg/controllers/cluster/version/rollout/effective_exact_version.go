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
	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
)

// effectiveExactVersion returns the rollout target without changing the graph's
// best version or the configured pin.
func effectiveExactVersion(s *fleetapi.ControlPlaneVersionRolloutSpec) *semver.Version {
	if hasActiveRolloutPin(s) {
		return s.PinnedVersion.ExactVersion
	}
	return s.BestExactVersion
}

// hasActiveRolloutPin distinguishes an authoritative pin from normal selection,
// including when the selected version would move a cluster backwards.
func hasActiveRolloutPin(s *fleetapi.ControlPlaneVersionRolloutSpec) bool {
	pin := s.PinnedVersion
	return pin.ExactVersion != nil && (pin.UntilExactVersion == nil || s.BestExactVersion == nil || !s.BestExactVersion.GT(*pin.UntilExactVersion))
}
