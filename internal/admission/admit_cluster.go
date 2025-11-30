// Copyright 2025 Microsoft Corporation
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

package admission

import (
	"context"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// AdmitCluster performs non-static checks of cluster.  Checks that require more information than is contained inside of
// of the cluster instance itself.
func AdmitClusterOnCreate(ctx context.Context, newVersion *api.HCPOpenShiftCluster, subscription *arm.Subscription) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	errs := admitVersionProfileOnCreate(ctx, &newVersion.CustomerProperties.Version, op, subscription)

	return errs
}

// admitVersionProfile performs non-static check for a versionProfil of a cluster. This check requires subscription
func admitVersionProfileOnCreate(ctx context.Context, newVersion *api.VersionProfile, op operation.Operation, subscription *arm.Subscription) field.ErrorList {
	errs := field.ErrorList{}

	fldPath := field.NewPath("properties", "version")
	// Check if AllowDevNonStableChannels feature is enabled
	allowNonStableChannels := subscription != nil && subscription.HasRegisteredFeature(api.AllowDevNonStableChannels)

	// Version format validation depends on channel group and feature flag
	if allowNonStableChannels && newVersion.ChannelGroup != "stable" {
		// For non-stable channels with feature flag: allow full semver format (X.Y.Z-prerelease)
		errs = append(errs, validation.OpenshiftVersionWithOptionalMicro(ctx, op, fldPath.Child("id"), &newVersion.ID, nil)...)
	} else {
		// For stable or without feature flag: only MAJOR.MINOR format
		errs = append(errs, validation.OpenshiftVersionWithoutMicro(ctx, op, fldPath.Child("id"), &newVersion.ID, nil)...)
	}

	// Channel group validation based on subscription feature flags
	if subscription == nil || !allowNonStableChannels {
		// Without feature flag: only "stable" is allowed (empty would have failed static validation)
		errs = append(errs, validate.Enum(ctx, op, fldPath.Child("channelGroup"), &newVersion.ChannelGroup, nil, sets.New("stable"))...)
	}

	return errs
}
