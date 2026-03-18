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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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

// AdmitClusterOnCreate performs non-static checks of cluster. Checks that
// require more information than is contained inside of the cluster instance itself.
func AdmitClusterOnCreate(ctx context.Context, newVersion *api.HCPOpenShiftCluster, subscription *arm.Subscription) field.ErrorList {
	// to be filled in
	errs := field.ErrorList{}

	return errs
}
