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
	// SubscriptionClusters lists cluster documents in the same subscription
	// (not including the current cluster being admitted), used
	// for cross-cluster platform resource uniqueness on CREATE.
	// The list is empty on UPDATE.
	SubscriptionClusters []*api.HCPOpenShiftCluster
	// SubscriptionNodePools lists node pool documents under SubscriptionClusters,
	// used to ensure a cluster subnet is not already assigned to another cluster's
	// node pool on CREATE.
	// The list is empty on UPDATE.
	SubscriptionNodePools []*api.HCPOpenShiftClusterNodePool
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
	errs = append(errs, admitClusterPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform)...)

	return errs
}

func admitClusterPlatform(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *api.CustomerPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitClusterManagedResourceGroupName(ctx, admissionContext, op, fldPath, newObj)...)
	errs = append(errs, admitClusterSubnetResourceID(ctx, admissionContext, op, fldPath, newObj)...)
	errs = append(errs, admitClusterNetworkSecurityGroupResourceID(ctx, admissionContext, op, fldPath, newObj)...)
	return errs
}

// admitClusterManagedResourceGroupName ensures the managed resource group name
// is unique within the subscription on CREATE.
//
// Best-effort only: compares against SubscriptionClusters prefetched before
// admission runs. Concurrent creates with the same MRG name can both succeed.
func admitClusterManagedResourceGroupName(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *api.CustomerPlatformProfile) field.ErrorList {
	if op.Type != operation.Create {
		return nil
	}

	if admissionContext.OriginalCluster == nil {
		return field.ErrorList{field.InternalError(fldPath, errors.New("original cluster is required for admission"))}
	}

	mrgPath := fldPath.Child("managedResourceGroup")
	if len(newObj.ManagedResourceGroup) == 0 {
		return field.ErrorList{field.Required(mrgPath, "")}
	}

	subscriptionID := admissionContext.OriginalCluster.ID.SubscriptionID
	var errs field.ErrorList

	for _, existing := range admissionContext.SubscriptionClusters {
		if strings.EqualFold(newObj.ManagedResourceGroup, existing.CustomerProperties.Platform.ManagedResourceGroup) {
			errs = append(errs, field.Invalid(
				mrgPath,
				newObj.ManagedResourceGroup,
				fmt.Sprintf("Cluster with managed resource group name '%s' in subscription '%s' "+
					"already exists, please provide a unique managed resource group name",
					newObj.ManagedResourceGroup, subscriptionID),
			))
			break
		}
	}

	return errs
}

// admitClusterSubnetResourceID ensures that the subnet ID is not already in use by any other
// cluster or node pool within the same subscription when creating a new cluster.
//
// Best-effort only: compares against SubscriptionClusters and SubscriptionNodePools
// prefetched before admission runs. Concurrent creates (or a create racing with a
// node pool create) using the same subnet can both succeed.
func admitClusterSubnetResourceID(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *api.CustomerPlatformProfile) field.ErrorList {
	if op.Type != operation.Create {
		return nil
	}

	subnetPath := fldPath.Child("subnetId")
	if newObj.SubnetID == nil {
		return field.ErrorList{field.Required(subnetPath, "")}
	}
	subnetID := newObj.SubnetID.String()
	var errs field.ErrorList

	for _, existing := range admissionContext.SubscriptionClusters {
		existingSubnet := existing.CustomerProperties.Platform.SubnetID
		if existingSubnet == nil {
			errs = append(errs, field.InternalError(subnetPath, errors.New("existing cluster is missing subnetId")))
			continue
		}
		if strings.EqualFold(subnetID, existingSubnet.String()) {
			errs = append(errs, field.Invalid(
				subnetPath,
				subnetID,
				fmt.Sprintf("Subnet '%s' is already in use by another cluster", subnetID),
			))
			break
		}
	}

	for _, nodePool := range admissionContext.SubscriptionNodePools {
		nodePoolSubnet := nodePool.Properties.Platform.SubnetID
		if nodePoolSubnet == nil {
			errs = append(errs, field.InternalError(subnetPath, errors.New("existing node pool is missing subnetId")))
			continue
		}
		if strings.EqualFold(subnetID, nodePoolSubnet.String()) {
			errs = append(errs, field.Invalid(
				subnetPath,
				subnetID,
				fmt.Sprintf("Subnet '%s' is already in use by another cluster", subnetID),
			))
			break
		}
	}

	return errs
}

// admitClusterNetworkSecurityGroupResourceID ensures that the network security group ID is not already in use by any other
// cluster within the same subscription when creating a new cluster.
//
// Best-effort only: compares against SubscriptionClusters prefetched before
// admission runs. Concurrent creates with the same NSG can both succeed.
func admitClusterNetworkSecurityGroupResourceID(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *api.CustomerPlatformProfile) field.ErrorList {
	if op.Type != operation.Create {
		return nil
	}

	nsgPath := fldPath.Child("networkSecurityGroupId")
	if newObj.NetworkSecurityGroupID == nil {
		return field.ErrorList{field.Required(nsgPath, "")}
	}
	nsgID := newObj.NetworkSecurityGroupID.String()
	var errs field.ErrorList

	for _, existing := range admissionContext.SubscriptionClusters {
		existingNSG := existing.CustomerProperties.Platform.NetworkSecurityGroupID
		if existingNSG == nil {
			errs = append(errs, field.InternalError(nsgPath, errors.New("existing cluster is missing networkSecurityGroupId")))
			continue
		}
		if strings.EqualFold(nsgID, existingNSG.String()) {
			errs = append(errs, field.Invalid(
				nsgPath,
				nsgID,
				fmt.Sprintf("Network Security Group '%s' is already in use by another cluster", nsgID),
			))
			break
		}
	}

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
