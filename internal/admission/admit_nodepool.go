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
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// NodePoolAdmissionContext carries dependencies that node pool mutation/admission needs
// beyond the node pool object itself. It includes the parent cluster and optionally
// the service provider cluster (for version skew validation) and
// nodepool (for update-specific validations like version upgrades).
type NodePoolAdmissionContext struct {
	Clock        utilsclock.PassiveClock
	Subscription *arm.Subscription
	// OriginalNodePool is a deepcopy of the inbound node pool as the user submitted
	// it, taken before any admission mutation runs. It is the read-only source
	// of truth for fields (like tags) that are *consumed* during mutation but
	// whose new-object value may already have been overwritten by the time the
	// mutation actually runs.
	OriginalNodePool        *api.HCPOpenShiftClusterNodePool
	Cluster                 *api.HCPOpenShiftCluster
	ServiceProviderNodePool *api.ServiceProviderNodePool
	ServiceProviderCluster  *api.ServiceProviderCluster
}

// MutateNodePool applies admission-time mutations to a node pool (e.g. defaulting
// the subnet from the parent cluster on CREATE). It returns any field errors
// produced by the mutation step.
func MutateNodePool(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, newObj, oldObj *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	errs := field.ErrorList{}

	//Properties HCPOpenShiftClusterNodePoolProperties `json:"properties"`
	errs = append(errs, mutateNodePoolProperties(ctx, admissionContext, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, validation.ToNodePoolProperties))...)

	errs = append(errs, mutateNodePoolServiceProviderProperties(ctx, admissionContext, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties)...)

	return errs
}

func mutateNodePoolProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterNodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateNodePoolPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, validation.ToNodePoolPropertiesPlatform))...)

	return errs
}

func mutateNodePoolPlatform(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	if op.Type == operation.Create {
		if newObj.SubnetID == nil {
			newObj.SubnetID = admissionContext.Cluster.CustomerProperties.Platform.DeepCopy().SubnetID
		}
	}

	return errs
}

func mutateNodePoolServiceProviderProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *api.HCPOpenShiftClusterNodePoolServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateNodePoolExperimentalTags(ctx, admissionContext, op)...)
	errs = append(errs, mutateNodePoolCreateOperationCompletionDeadline(ctx, admissionContext, op, fldPath.Child("createOperationCompletionDeadline"), &newObj.CreateOperationCompletionDeadline)...)

	return errs
}

// mutateNodePoolExperimentalTags rejects unrecognized experimental node pool
// tags when the ExperimentalReleaseFeatures AFEC is registered.
func mutateNodePoolExperimentalTags(_ context.Context, admissionContext *NodePoolAdmissionContext, _ operation.Operation) field.ErrorList {
	subscription := admissionContext.Subscription
	if subscription == nil || !subscription.HasRegisteredFeature(api.FeatureExperimentalReleaseFeatures) {
		return nil
	}

	var tags map[string]string
	if admissionContext.OriginalNodePool != nil {
		tags = admissionContext.OriginalNodePool.Tags
	}
	tagsPath := field.NewPath("tags")
	var errs field.ErrorList

	knownTags := sets.New(api.TagNodePoolMaxCreationDuration)
	for k := range tags {
		if strings.HasPrefix(strings.ToLower(k), api.ExperimentalNodePoolTagPrefix) && !knownTags.Has(strings.ToLower(k)) {
			errs = append(errs, field.Invalid(tagsPath.Key(k), k, "unrecognized experimental tag"))
			return errs
		}
	}

	return errs
}

// mutateNodePoolCreateOperationCompletionDeadline sets the deadline by which a
// node pool creation operation must complete. On CREATE it defaults to 60
// minutes from now; when the subscription has the ExperimentalReleaseFeatures
// AFEC registered, the caller may override the duration via the
// TagNodePoolMaxCreationDuration ARM resource tag.
func mutateNodePoolCreateOperationCompletionDeadline(_ context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, _ *field.Path, newObj **metav1.Time) field.ErrorList {
	if op.Type != operation.Create {
		return nil
	}

	duration := defaultCreateOperationCompletionDeadlineDuration

	subscription := admissionContext.Subscription
	if subscription != nil && subscription.HasRegisteredFeature(api.FeatureExperimentalReleaseFeatures) {
		var tags map[string]string
		if admissionContext.OriginalNodePool != nil {
			tags = admissionContext.OriginalNodePool.Tags
		}
		if tagValue := lookupTag(tags, api.TagNodePoolMaxCreationDuration); len(tagValue) > 0 {
			parsed, err := time.ParseDuration(tagValue)
			if err != nil {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(api.TagNodePoolMaxCreationDuration), tagValue, "must be a valid Go duration string (e.g. \"19m\", \"30m\")")}
			}
			if parsed < minCreateOperationCompletionDeadlineDuration {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(api.TagNodePoolMaxCreationDuration), tagValue, fmt.Sprintf("must be at least %s", minCreateOperationCompletionDeadlineDuration))}
			}
			duration = parsed
		}
	}

	deadline := metav1.NewTime(admissionContext.Clock.Now().Add(duration))
	*newObj = &deadline
	return nil
}

// NodePoolDeleteAdmissionContext carries dependencies that node pool deletion admission needs.
type NodePoolDeleteAdmissionContext struct {
	// ClusterNodePools is a list of all node pools for the cluster, including the one being deleted.
	ClusterNodePools []*api.HCPOpenShiftClusterNodePool
}

// AdmitNodePoolOnDelete performs non-static checks before deleting a node pool.
func AdmitNodePoolOnDelete(ctx context.Context, admissionContext *NodePoolDeleteAdmissionContext, _ *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	errs := field.ErrorList{}

	// We do a *best-effort* to check to see if we are the last node pool on the cluster and prevent deletion
	// if we are. This is because as of now (2026-05-29) it is not possible to delete the last node pool from the cluster
	// OCPBUGS-86702. This check won't fully prevent the last node pool deletion in all cases as there are edge cases
	// where race conditions can occur, but it should be good enough to prevent the last node pool deletion in most cases.
	// TODO once OCPBUGS-86702 is fixed, we should remove this check.
	if len(admissionContext.ClusterNodePools) <= 1 {
		errs = append(errs, field.Forbidden(field.NewPath("name"), "The last node pool can not be deleted from a cluster."))
	}

	return errs
}

// AdmitNodePool performs non-static checks of nodepool. Checks that require more information than is contained inside of
// the nodepool instance itself. For update operations, ServiceProviderNodePool must be specified in the admissionContext
func AdmitNodePool(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitNodePoolProperties(ctx, admissionContext, op, field.NewPath("properties"), &newNodePool.Properties, safe.Field(oldNodePool, validation.ToNodePoolProperties))...)

	return errs
}

func admitNodePoolProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterNodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitNodePoolVersion(ctx, admissionContext, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, validation.ToNodePoolPropertiesVersion))...)
	errs = append(errs, admitNodePoolPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, validation.ToNodePoolPropertiesPlatform))...)

	return errs
}

func admitNodePoolVersion(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolVersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	switch op.Type {
	case operation.Create:
		errs = append(errs, validateNodePoolVersionOnCreate(ctx, admissionContext, op, fldPath.Child("id"), newObj)...)
	case operation.Update:
		errs = append(errs, validateNodePoolVersionOnUpdate(ctx, admissionContext, op, fldPath.Child("id"), newObj, oldObj)...)
	}

	return errs
}

func admitNodePoolPlatform(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	clusterPlatform := &admissionContext.Cluster.CustomerProperties.Platform

	// Check only if it is a creating nodepool or a change in the Subnet.
	// Compare by string value (not pointer identity) so equal-but-distinct
	// *azcorearm.ResourceID values aren't treated as a change.
	if newObj.SubnetID != nil && clusterPlatform.SubnetID != nil {
		var oldSubnetID string
		if op.Type == operation.Update && oldObj.SubnetID != nil {
			oldSubnetID = oldObj.SubnetID.String()
		}
		newSubnetID := newObj.SubnetID.String()
		if op.Type == operation.Create || !strings.EqualFold(newSubnetID, oldSubnetID) {
			clusterVNet := clusterPlatform.SubnetID.Parent.String()
			nodePoolVNet := newObj.SubnetID.Parent.String()
			if !strings.EqualFold(nodePoolVNet, clusterVNet) {
				errs = append(errs, field.Invalid(
					fldPath.Child("subnetId"),
					newObj.SubnetID,
					fmt.Sprintf("must belong to the same VNet as the parent cluster VNet '%s'", clusterVNet),
				))
			}
		}
	}

	return errs
}

// validateNodePoolVersionOnCreate validates the requested node pool version at CREATE time.
// At CREATE time, only basic validations apply (format, minimum version).
func validateNodePoolVersionOnCreate(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *api.NodePoolVersionProfile) field.ErrorList {
	if len(newObj.ID) == 0 {
		return nil
	}

	errs := field.ErrorList{}
	newVersion, err := semver.Parse(newObj.ID)
	if err != nil {
		errs = append(errs, field.Invalid(fldPath, newObj.ID, fmt.Sprintf("invalid node pool version format: %s", err.Error())))
		return errs
	}
	spCluster := admissionContext.ServiceProviderCluster

	lowestCPVersion, highestCPVersion := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	err = validation.ValidateNodePoolVersionSkew(newVersion, lowestCPVersion, highestCPVersion, op.HasOption(api.FeatureExperimentalReleaseFeatures))
	if err != nil {
		errs = append(errs, field.Invalid(fldPath, newObj.ID, err.Error()))
	}

	return errs
}

// validateNodePoolVersionOnUpdate validates that a node pool version change is valid.
// It checks:
//   - Upgrade: at most +2 minor versions from current, and cannot exceed lowest control plane version
//   - Downgrade: at most -2 minor versions from the highest control plane version
//   - Cross-major changes (either direction) require AFEC FeatureExperimentalReleaseFeatures
//   - NP version must be in the allowed skew map when CP and NP are on different majors
func validateNodePoolVersionOnUpdate(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolVersionProfile) field.ErrorList {
	spNodePool, spCluster := admissionContext.ServiceProviderNodePool, admissionContext.ServiceProviderCluster
	// Skip validation if no version is specified or version didn't change
	if len(newObj.ID) == 0 || newObj.ID == oldObj.ID {
		return nil
	}

	errs := field.ErrorList{}

	newVersion, err := semver.Parse(newObj.ID)
	if err != nil {
		errs = append(errs, field.Invalid(fldPath, newObj.ID, fmt.Sprintf("invalid node pool version format: %s", err.Error())))
		// Return early, it cannot validate an unparseable version
		return errs
	}
	// Skip validation if the newVersion hasn't changed from the desired Version
	if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil &&
		newVersion.EQ(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
		return nil
	}

	lowestCPVersion, highestCPVersion := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	if err := validation.ValidateNodePoolVersionChange(newVersion, spNodePool.Status.NodePoolVersion.ActiveVersions, lowestCPVersion, highestCPVersion, op.HasOption(api.FeatureExperimentalReleaseFeatures)); err != nil {
		errs = append(errs, field.Invalid(fldPath, newObj.ID, err.Error()))
	}

	return errs
}
