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

package validation

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

// ValidateControlPlaneVersionRolloutCreate validates a new ControlPlaneVersionRollout.
func ValidateControlPlaneVersionRolloutCreate(_ context.Context, rollout *fleetapi.ControlPlaneVersionRollout) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateControlPlaneVersionRolloutIdentifier(rollout)...)
	return errs
}

// ValidateControlPlaneVersionRolloutUpdate validates an update to a ControlPlaneVersionRollout.
// Existing legacy writers may preserve a missing profile until backfill. A stale
// legacy snapshot must reach the ETag check so it cannot erase persisted backfill.
func ValidateControlPlaneVersionRolloutUpdate(_ context.Context, newRollout *fleetapi.ControlPlaneVersionRollout, oldRollout *fleetapi.ControlPlaneVersionRollout) field.ErrorList {
	if oldRollout != nil && newRollout != nil &&
		oldRollout.Spec.Version == (coreapi.VersionProfile{}) && newRollout.Spec.Version == (coreapi.VersionProfile{}) &&
		oldRollout.ResourceID != nil && newRollout.ResourceID != nil && oldRollout.ResourceID.String() == newRollout.ResourceID.String() {
		if normalized, err := fleetapihelpers.NormalizeRolloutVersion(newRollout); err == nil {
			return validateControlPlaneVersionRolloutIdentifier(normalized)
		}
	}
	var errs field.ErrorList
	errs = append(errs, validateControlPlaneVersionRolloutIdentifier(newRollout)...)
	return errs
}

func validateControlPlaneVersionRolloutIdentifier(rollout *fleetapi.ControlPlaneVersionRollout) field.ErrorList {
	var errs field.ErrorList
	channel := rollout.GetStampIdentifier()
	path := field.NewPath("cosmosMetadata", "resourceID")
	if len(channel) == 0 {
		errs = append(errs, field.Required(
			path,
			"y-stream channel (top-level resource name) is required",
		))
		return errs
	}
	if err := fleetapihelpers.ValidateRolloutVersion(rollout); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec", "version"), rollout.Spec.Version, err.Error()))
	}
	return errs
}
