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
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// ClusterAdmissionContext carries dependencies that cluster mutation/admission
// needs beyond the cluster object itself. The Subscription is required for all
// operations. ServiceProviderCluster and ClusterNodePools are populated for
// UPDATE-time admission checks that depend on existing server-side state
// (e.g., version-skew validation).
type ClusterAdmissionContext struct {
	Subscription *arm.Subscription
	// OriginalCluster is a deepcopy of the inbound cluster as the user submitted
	// it, taken before any admission mutation runs. It is the read-only source
	// of truth for fields (like tags) that are *consumed* during mutation but
	// whose new-object value may already have been overwritten by the time the
	// mutation actually runs.
	OriginalCluster        *api.HCPOpenShiftCluster
	ServiceProviderCluster *api.ServiceProviderCluster
	// ClusterNodePools is the list of node pools belonging to the cluster, used
	// for minor-version skew checks against the desired cluster version.
	ClusterNodePools []ClusterAdmissionNodePool
}

// ClusterAdmissionNodePool is a single node pool plus its prefetched service
// provider record. The cluster admission walks these to validate version skew
// of every node pool against the desired cluster version.
type ClusterAdmissionNodePool struct {
	NodePool                *api.HCPOpenShiftClusterNodePool
	ServiceProviderNodePool *api.ServiceProviderNodePool
}

// MutateCluster applies admission-time mutations to a cluster (generating
// the ClusterUID on CREATE and translating experimental tags into
// ServiceProviderProperties.ExperimentalFeatures). It returns any field errors
// produced by the mutation step.
func MutateCluster(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, newObj, oldObj *api.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	// ServiceProviderProperties HCPOpenShiftClusterServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	errs = append(errs, mutateClusterServiceProviderProperties(ctx, admissionContext, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties, safe.Field(oldObj, validation.ToClusterServiceProviderProperties))...)

	return errs
}

// mutateClusterServiceProviderProperties applies mutations that live on the
// service-provider half of the cluster.
func mutateClusterServiceProviderProperties(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateClusterUID(ctx, admissionContext, op, fldPath.Child("clusterUID"), &newObj.ClusterUID, safe.Field(oldObj, validation.ToClusterServiceProviderPropertiesClusterUID))...)
	errs = append(errs, mutateClusterExperimentalFeatures(ctx, admissionContext, op, fldPath.Child("experimentalFeatures"), &newObj.ExperimentalFeatures, safe.Field(oldObj, toSPExperimentalFeatures))...)

	return errs
}

// mutateClusterUID generates a stable ClusterUID on CREATE if one was not
// already supplied. The field is immutable, so UPDATE leaves it alone.
func mutateClusterUID(_ context.Context, _ *ClusterAdmissionContext, op operation.Operation, _ *field.Path, newObj, _ *string) field.ErrorList {
	if op.Type == operation.Create && len(*newObj) == 0 {
		*newObj = uuid.New().String()
	}
	return nil
}

func toSPExperimentalFeatures(oldObj *api.HCPOpenShiftClusterServiceProviderProperties) *api.ExperimentalFeatures {
	return &oldObj.ExperimentalFeatures
}

// mutateClusterExperimentalFeatures translates the experimental tag set from
// the original (pre-mutation) cluster into ExperimentalFeatures on the
// cluster's service provider properties. Tags are *read from*
// admissionContext.OriginalCluster — never from the cluster being mutated —
// because earlier admission steps may have overwritten the cluster's tag map.
// Without AFEC registration ExperimentalFeatures is zeroed and tags are
// ignored; with AFEC registered, unrecognized experimental tags and invalid
// values are rejected.
func mutateClusterExperimentalFeatures(_ context.Context, admissionContext *ClusterAdmissionContext, _ operation.Operation, _ *field.Path, newObj, _ *api.ExperimentalFeatures) field.ErrorList {
	subscription := admissionContext.Subscription
	if subscription == nil || !subscription.HasRegisteredFeature(api.FeatureExperimentalReleaseFeatures) {
		*newObj = api.ExperimentalFeatures{}
		return nil
	}

	var tags map[string]string
	if admissionContext.OriginalCluster != nil {
		tags = admissionContext.OriginalCluster.Tags
	}
	// Errors here are reported under the source-of-truth path so users see
	// "tags[key]" not "serviceProviderProperties.experimentalFeatures".
	tagsPath := field.NewPath("tags")
	var errs field.ErrorList

	// Reject unrecognized experimental tags.
	knownTags := sets.New(api.TagClusterSingleReplica, api.TagClusterSizeOverride, api.TagClusterCPOImageOverride, api.TagClusterFIPSEnabled)
	for k := range tags {
		if strings.HasPrefix(strings.ToLower(k), api.ExperimentalClusterTagPrefix) && !knownTags.Has(strings.ToLower(k)) {
			errs = append(errs, field.Invalid(tagsPath.Key(k), k, "unrecognized experimental tag"))
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
			tagsPath.Key(api.TagClusterSingleReplica), singleReplicaValue,
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
			tagsPath.Key(api.TagClusterSizeOverride), sizeOverrideValue,
			fmt.Sprintf("must be %q or empty", api.MinimalControlPlanePodSizing),
		))
	}

	cpoImageValue := lookupTag(tags, api.TagClusterCPOImageOverride)
	if cpoImageValue != "" {
		trimmed := strings.TrimSpace(cpoImageValue)
		if trimmed == "" {
			errs = append(errs, field.Invalid(
				tagsPath.Key(api.TagClusterCPOImageOverride), cpoImageValue,
				"must not be blank when provided",
			))
		} else {
			experimentalFeatures.ControlPlaneOperatorImage = trimmed
		}
	}

	fipsEnabled := lookupTag(tags, api.TagClusterFIPSEnabled)
	if fipsEnabled != "" {
		boolValue, err := strconv.ParseBool(fipsEnabled)
		if err != nil {
			errs = append(errs, field.Invalid(tagsPath.Key(api.TagClusterFIPSEnabled), fipsEnabled, "must be true or false"))
		} else {
			experimentalFeatures.FIPSEnabled = boolValue
		}
	}

	if len(errs) > 0 {
		return errs
	}

	*newObj = experimentalFeatures
	return errs
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

// AdmitCluster performs non-static checks of cluster. Checks that require more
// information than is contained inside of the cluster instance itself. For
// UPDATE operations that may change the cluster version, the admissionContext
// must carry the prefetched ServiceProviderCluster and ClusterNodePools.
func AdmitCluster(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, newObj, oldObj *api.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	// CustomerProperties HCPOpenShiftClusterCustomerProperties `json:"customerProperties,omitempty"`
	errs = append(errs, admitClusterCustomerProperties(ctx, admissionContext, op, field.NewPath("properties"), &newObj.CustomerProperties, safe.Field(oldObj, validation.ToClusterCustomerProperties))...)

	return errs
}

// admitClusterCustomerProperties drills down into the customer-facing portion
// of the cluster.
func admitClusterCustomerProperties(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterCustomerProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitClusterVersionProfile(ctx, admissionContext, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, validation.ToClusterCustomerPropertiesVersion))...)

	return errs
}

// admitClusterVersionProfile runs admission checks when properties.version
// changes (skew against active control-plane versions and existing node pool
// minor skew). On CREATE there is no prior version to compare against, so this
// is a no-op.
func admitClusterVersionProfile(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.VersionProfile) field.ErrorList {
	if op.Type != operation.Update || oldObj == nil {
		return nil
	}
	if len(newObj.ID) == 0 || oldObj.ID == newObj.ID {
		return nil
	}

	versionPath := fldPath.Child("id")
	var errs field.ErrorList

	oldVersion, oldParseErr := semver.ParseTolerant(oldObj.ID)
	if oldParseErr != nil {
		return field.ErrorList{field.Invalid(versionPath, oldObj.ID, oldParseErr.Error())}
	}

	if admissionContext.ServiceProviderCluster == nil {
		errs = append(errs, field.InternalError(versionPath, errors.New("cannot validate cluster version skew")))
	} else {
		lowest, highest := apihelpers.FindLowestAndHighestClusterVersion(admissionContext.ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)
		if lowest != nil && highest != nil {
			// When the customer's current release line matches the lowest active CP, static validation
			// already enforced skew from the old cluster version; do not duplicate against lowest.
			if oldVersion.Major != lowest.Major || oldVersion.Minor != lowest.Minor {
				if skewErr := validation.OpenshiftVersionAtMostOneMinorSkew(lowest.String(), newObj.ID); skewErr != nil {
					errs = append(errs, field.Invalid(versionPath, newObj.ID, skewErr.Error()))
				}
			}
			errs = append(errs, validation.VersionMustBeAtLeast(ctx, op, versionPath, ptr.To(newObj.ID), nil, highest.String())...)
		}
	}

	newVersion, parseErr := semver.ParseTolerant(newObj.ID)
	if parseErr != nil {
		errs = append(errs, field.Invalid(versionPath, newObj.ID, parseErr.Error()))
	} else if npErr := AdmitClusterNodePoolsMinorVersionSkew(ctx, admissionContext.ClusterNodePools, newVersion); npErr != nil {
		errs = append(errs, field.Invalid(versionPath, newObj.ID, npErr.Error()))
	}

	return errs
}
