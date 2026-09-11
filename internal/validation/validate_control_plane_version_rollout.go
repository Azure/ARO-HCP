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
	"regexp"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

// ValidateControlPlaneVersionRolloutCreate validates a new ControlPlaneVersionRollout.
func ValidateControlPlaneVersionRolloutCreate(_ context.Context, rollout *fleetapi.ControlPlaneVersionRollout) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateControlPlaneVersionRolloutIdentifier(rollout)...)
	return errs
}

// ValidateControlPlaneVersionRolloutUpdate validates an update to a ControlPlaneVersionRollout.
func ValidateControlPlaneVersionRolloutUpdate(_ context.Context, newRollout *fleetapi.ControlPlaneVersionRollout, _ *fleetapi.ControlPlaneVersionRollout) field.ErrorList {
	var errs field.ErrorList
	errs = append(errs, validateControlPlaneVersionRolloutIdentifier(newRollout)...)
	return errs
}

var rolloutChannelPattern = regexp.MustCompile(`^(stable|fast|candidate|nightly)-(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

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
	_, minor, _ := strings.Cut(channel, "-")
	_, parseErr := semver.Parse(minor + ".0")
	if !rolloutChannelPattern.MatchString(channel) || parseErr != nil {
		errs = append(errs, field.Invalid(path, channel, "y-stream channel must be <stable|fast|candidate|nightly>-<major>.<minor>"))
	}
	return errs
}
