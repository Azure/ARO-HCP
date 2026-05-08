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
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// MutateCluster sets internal cluster state derived from subscription features
// and resource tags. Must be called before validation. Returns field errors if
// any experimental tag has an invalid value or is unrecognized.
func MutateCluster(cluster *api.HCPOpenShiftCluster, subscription *arm.Subscription) field.ErrorList {
	if subscription == nil || !subscription.HasRegisteredFeature(api.FeatureExperimentalReleaseFeatures) {
		cluster.ServiceProviderProperties.ExperimentalFeatures = api.ExperimentalFeatures{}
		return nil
	}

	tags := cluster.Tags
	fldPath := field.NewPath("tags")
	var errs field.ErrorList

	// Reject unrecognized experimental tags.
	knownTags := sets.New(api.TagClusterSingleReplica, api.TagClusterSizeOverride)
	for k := range tags {
		if strings.HasPrefix(strings.ToLower(k), api.ExperimentalClusterTagPrefix) && !knownTags.Has(strings.ToLower(k)) {
			errs = append(errs, field.Invalid(fldPath.Key(k), k, "unrecognized experimental tag"))
			return errs
		}
	}

	var experimentalFeatures api.ExperimentalFeatures

	singleReplicaValue := lookupTag(tags, api.TagClusterSingleReplica)
	switch api.ControlPlaneAvailability(singleReplicaValue) {
	case api.SingleReplicaControlPlane:
		experimentalFeatures.ControlPlaneAvailability = api.SingleReplicaControlPlane
	case api.DefaultControlPlaneAvailability:
		// absent or empty
	default:
		errs = append(errs, field.Invalid(
			fldPath.Key(api.TagClusterSingleReplica), singleReplicaValue,
			fmt.Sprintf("must be %q or empty", api.SingleReplicaControlPlane),
		))
	}

	sizeOverrideValue := lookupTag(tags, api.TagClusterSizeOverride)
	switch api.ControlPlanePodSizing(sizeOverrideValue) {
	case api.MinimalControlPlanePodSizing:
		experimentalFeatures.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
	case api.DefaultControlPlanePodSizing:
		// absent or empty
	default:
		errs = append(errs, field.Invalid(
			fldPath.Key(api.TagClusterSizeOverride), sizeOverrideValue,
			fmt.Sprintf("must be %q or empty", api.MinimalControlPlanePodSizing),
		))
	}

	if len(errs) > 0 {
		return errs
	}

	cluster.ServiceProviderProperties.ExperimentalFeatures = experimentalFeatures

	return nil
}

// lookupTag returns the value for the given tag key using case-insensitive
// comparison. Returns empty string if the tag is not found.
func lookupTag(tags map[string]string, key string) string {
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// MutateClusterCreate sets fields that are generated on cluster creation.
// Must be called after decoding and before validation on CREATE operations only.
func MutateClusterCreate(cluster *api.HCPOpenShiftCluster) {
	if len(cluster.ServiceProviderProperties.ClusterUID) == 0 {
		cluster.ServiceProviderProperties.ClusterUID = uuid.New().String()
	}
}

// AdmitClusterOnCreate performs non-static checks of cluster. Checks that
// require more information than is contained inside of the cluster instance itself.
func AdmitClusterOnCreate(ctx context.Context, newVersion *api.HCPOpenShiftCluster, subscription *arm.Subscription) field.ErrorList {
	// to be filled in
	errs := field.ErrorList{}

	return errs
}

// AdmitClusterOnUpdate performs non-static checks of cluster. Checks that
// require more information than is contained inside of the cluster instance itself.
func AdmitClusterOnUpdate(ctx context.Context, op operation.Operation, resourcesDBClient database.ResourcesDBClient, oldCluster, newCluster *api.HCPOpenShiftCluster) field.ErrorList {
	var errs field.ErrorList

	// Version                 VersionProfile              `json:"version,omitempty"`
	errs = append(errs, admitVersionProfileOnClusterUpdate(ctx, op, resourcesDBClient, oldCluster, newCluster)...)

	return errs
}

// admitVersionProfileOnClusterUpdate runs admission checks when properties.version changes
// (skew against active control-plane versions and existing node pool minor skew).
func admitVersionProfileOnClusterUpdate(ctx context.Context, op operation.Operation, resourcesDBClient database.ResourcesDBClient, oldCluster, newCluster *api.HCPOpenShiftCluster) field.ErrorList {
	if len(newCluster.CustomerProperties.Version.ID) == 0 ||
		oldCluster.CustomerProperties.Version.ID == newCluster.CustomerProperties.Version.ID {
		return nil
	}
	versionPath := field.NewPath("properties", "version", "id")
	var errs field.ErrorList

	oldVersion, oldParseErr := semver.ParseTolerant(oldCluster.CustomerProperties.Version.ID)
	if oldParseErr != nil {
		return field.ErrorList{field.Invalid(versionPath, oldCluster.CustomerProperties.Version.ID, oldParseErr.Error())}
	}

	serviceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, resourcesDBClient, oldCluster.ID)
	if err != nil {
		errs = append(errs, field.InternalError(versionPath, errors.New("cannot validate cluster version skew")))
	} else {
		lowest, highest := apihelpers.FindLowestAndHighestClusterVersion(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)
		if lowest != nil && highest != nil {
			// When the customer's current release line matches the lowest active CP, static validation
			// already enforced skew from the old cluster version; do not duplicate against lowest.
			if oldVersion.Major != lowest.Major || oldVersion.Minor != lowest.Minor {
				if skewErr := validation.OpenshiftVersionAtMostOneMinorSkew(lowest.String(), newCluster.CustomerProperties.Version.ID); skewErr != nil {
					errs = append(errs, field.Invalid(versionPath, newCluster.CustomerProperties.Version.ID, skewErr.Error()))
				}
			}
			errs = append(errs, validation.VersionMustBeAtLeast(ctx, op, versionPath, ptr.To(newCluster.CustomerProperties.Version.ID), nil, highest.String())...)
		}
	}

	clusterVersion, parseErr := semver.ParseTolerant(newCluster.CustomerProperties.Version.ID)
	if parseErr != nil {
		errs = append(errs, field.Invalid(versionPath, newCluster.CustomerProperties.Version.ID, parseErr.Error()))
	} else if npErr := ValidateClusterNodePoolsMinorVersionSkew(ctx, resourcesDBClient, oldCluster.ID, clusterVersion); npErr != nil {
		errs = append(errs, field.Invalid(versionPath, newCluster.CustomerProperties.Version.ID, npErr.Error()))
	}

	return errs
}
