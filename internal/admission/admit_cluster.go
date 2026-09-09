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
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// ClusterAdmissionContext carries dependencies that cluster mutation/admission
// needs beyond the cluster object itself. The Subscription is required for all
// operations. ServiceProviderCluster and ClusterNodePools are populated for
// UPDATE-time admission checks that depend on existing server-side state
// (e.g., version-skew validation).
type ClusterAdmissionContext struct {
	Clock        utilsclock.PassiveClock
	Subscription *coreapi.Subscription
	// OriginalCluster is a deepcopy of the inbound cluster as the user submitted
	// it, taken before any admission mutation runs. It is the read-only source
	// of truth for fields (like tags) that are *consumed* during mutation but
	// whose new-object value may already have been overwritten by the time the
	// mutation actually runs.
	OriginalCluster        *coreapi.HCPOpenShiftCluster
	ServiceProviderCluster *coreapi.ServiceProviderCluster
	// ClusterNodePools is the list of node pools belonging to the cluster, used
	// for minor-version skew checks against the desired cluster version.
	ClusterNodePools []ClusterAdmissionNodePool
	// SubscriptionClusters lists cluster documents in the same subscription
	// (not including the current cluster being admitted), used
	// for cross-cluster platform resource uniqueness on CREATE.
	// The list is empty on UPDATE.
	SubscriptionClusters []*coreapi.HCPOpenShiftCluster
	// SubscriptionNodePools lists node pool documents under SubscriptionClusters,
	// used to ensure a cluster subnet is not already assigned to another cluster's
	// node pool on CREATE.
	// The list is empty on UPDATE.
	SubscriptionNodePools []*coreapi.HCPOpenShiftClusterNodePool
}

// ClusterAdmissionNodePool is a single node pool plus its prefetched service
// provider record. The cluster admission walks these to validate version skew
// of every node pool against the desired cluster version.
type ClusterAdmissionNodePool struct {
	NodePool                *coreapi.HCPOpenShiftClusterNodePool
	ServiceProviderNodePool *coreapi.ServiceProviderNodePool
}

// MutateCluster applies admission-time mutations to a cluster (generating
// the ClusterUID on CREATE and translating experimental tags into
// ServiceProviderProperties.ExperimentalFeatures). It returns any field errors
// produced by the mutation step.
func MutateCluster(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, newObj, oldObj *coreapi.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	// ServiceProviderProperties HCPOpenShiftClusterServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	errs = append(errs, mutateClusterServiceProviderProperties(ctx, admissionContext, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties, safe.Field(oldObj, validation.ToClusterServiceProviderProperties))...)

	// Relocate an exact version supplied through version.id onto the
	// experimental features. Runs after mutateClusterServiceProviderProperties
	// (which seeds ExperimentalFeatures from tags) because it needs both the
	// customer-facing version.id and the service-provider ExperimentalFeatures.
	errs = append(errs, mutateClusterControlPlaneExactVersion(ctx, admissionContext, op, newObj, oldObj)...)

	return errs
}

// mutateClusterControlPlaneExactVersion pins the control plane to an exact
// OpenShift version onto
// ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion.
//
// It only acts when the ExperimentalReleaseFeatures AFEC is registered. With the
// AFEC registered a customer may express an exact pin two ways:
//   - the control-plane-exact-version tag carrying a full semantic version, or
//   - a full "<major>.<minor>.<patch>" version.id (a value that parses as strict
//     semver, including pre-release/build metadata such as nightly or ec builds).
//
// When either source is present, version.id is reduced to its "<major>.<minor>"
// release line and the exact version is stored on ExperimentalFeatures. When
// both the tag and a full version.id are supplied, the tag is authoritative: the
// full-semver tag value determines the exact version and version.id's patch is
// discarded.
//
// The tag must carry a value — a present-but-empty tag is rejected with a field
// error rather than acting as a bare enable flag. A version.id that is not a full
// version (a bare "<major>.<minor>", or malformed) is left untouched here; static
// validation reports a malformed version.id.
//
// When neither source is present but the old cluster carried an exact pin, the
// customer is removing it, so the exact version is cleared.
//
// Tags are read from admissionContext.OriginalCluster (the pre-mutation source
// of truth) while version.id is read from and written back to the object being
// mutated.
func mutateClusterControlPlaneExactVersion(_ context.Context, admissionContext *ClusterAdmissionContext, _ operation.Operation, newObj, oldObj *coreapi.HCPOpenShiftCluster) field.ErrorList {
	subscription := admissionContext.Subscription
	if subscription == nil || !subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		return nil
	}

	var tags map[string]string
	if admissionContext.OriginalCluster != nil {
		tags = admissionContext.OriginalCluster.Tags
	}
	tagPresent := hasTag(tags, metadataapi.TagClusterControlPlaneExactVersion)
	tagValue := lookupTag(tags, metadataapi.TagClusterControlPlaneExactVersion)

	versionID := newObj.CustomerProperties.Version.ID
	// version.id carries an exact pin only when it is a full "<major>.<minor>.<patch>"
	// version. Strict semver parsing is the reliable test: it accepts pre-release
	// and build metadata such as nightly or ec builds (e.g. "5.0.0-ec.6",
	// "5.0.0-0.nightly-multi-2026-07-09-124132") while rejecting a bare
	// "<major>.<minor>" like "4.17".
	parsedVersionID, versionIDErr := semver.Parse(versionID)
	versionIDIsExact := versionIDErr == nil

	tagsPath := field.NewPath("tags")

	switch {
	case tagPresent && len(tagValue) == 0:
		// The tag is authoritative and must carry a value; a present-but-empty tag
		// is not a valid enable flag.
		return field.ErrorList{field.Invalid(
			tagsPath.Key(metadataapi.TagClusterControlPlaneExactVersion), tagValue,
			"must specify an exact \"<major>.<minor>.<patch>\" version",
		)}
	case len(tagValue) > 0:
		// The tag value is the authoritative exact version. version.id's patch (if
		// any) is discarded and version.id reduced to the tag's release line.
		parsed, err := semver.Parse(tagValue)
		if err != nil {
			return field.ErrorList{field.Invalid(
				tagsPath.Key(metadataapi.TagClusterControlPlaneExactVersion), tagValue,
				"must be a valid semantic version (e.g. \"4.17.3\")",
			)}
		}
		newObj.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = &parsed
		newObj.CustomerProperties.Version.ID = fmt.Sprintf("%d.%d", parsed.Major, parsed.Minor)
	case versionIDIsExact:
		// No tag, but the customer pinned an exact version directly through a full
		// version.id. A version.id that is not a full version (a bare
		// "<major>.<minor>", or malformed) is left untouched here and validated by
		// static validation.
		exact := parsedVersionID
		newObj.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = &exact
		newObj.CustomerProperties.Version.ID = fmt.Sprintf("%d.%d", parsedVersionID.Major, parsedVersionID.Minor)
	default:
		// Neither the tag nor a patch-bearing version.id is present. If the old
		// cluster carried an exact pin, the customer is removing it, so clear it.
		if oldObj != nil && oldObj.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion != nil {
			newObj.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = nil
		}
	}
	return nil
}

// mutateClusterServiceProviderProperties applies mutations that live on the
// service-provider half of the cluster.
func mutateClusterServiceProviderProperties(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateClusterUID(ctx, admissionContext, op, fldPath.Child("clusterUID"), &newObj.ClusterUID, safe.Field(oldObj, validation.ToClusterServiceProviderPropertiesClusterUID))...)
	errs = append(errs, mutateClusterExperimentalFeatures(ctx, admissionContext, op, fldPath.Child("experimentalFeatures"), &newObj.ExperimentalFeatures, safe.Field(oldObj, toSPExperimentalFeatures))...)
	errs = append(errs, mutateCreateOperationCompletionDeadline(ctx, admissionContext, op, fldPath.Child("createOperationCompletionDeadline"), &newObj.CreateOperationCompletionDeadline)...)
	errs = append(errs, mutateDeleteOperationCompletionTimeout(ctx, admissionContext, op, fldPath.Child("deleteOperationCompletionTimeout"), &newObj.DeleteOperationCompletionTimeout)...)

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

func toSPExperimentalFeatures(oldObj *coreapi.HCPOpenShiftClusterServiceProviderProperties) *coreapi.ExperimentalFeatures {
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
func mutateClusterExperimentalFeatures(_ context.Context, admissionContext *ClusterAdmissionContext, _ operation.Operation, _ *field.Path, newObj, _ *coreapi.ExperimentalFeatures) field.ErrorList {
	subscription := admissionContext.Subscription
	if subscription == nil || !subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		*newObj = coreapi.ExperimentalFeatures{}
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
	knownTags := sets.New(metadataapi.TagClusterSingleReplica, metadataapi.TagClusterSizeOverride, metadataapi.TagClusterCPOImageOverride, metadataapi.TagClusterControlPlaneExactVersion, metadataapi.TagClusterMaxCreationDuration, metadataapi.TagClusterMaxDeletionDuration)
	for k := range tags {
		if strings.HasPrefix(strings.ToLower(k), metadataapi.ExperimentalClusterTagPrefix) && !knownTags.Has(strings.ToLower(k)) {
			errs = append(errs, field.Invalid(tagsPath.Key(k), k, "unrecognized experimental tag"))
			return errs
		}
	}

	var experimentalFeatures coreapi.ExperimentalFeatures

	singleReplicaValue := lookupTag(tags, metadataapi.TagClusterSingleReplica)
	switch coreapi.ControlPlaneAvailability(singleReplicaValue) {
	case coreapi.SingleReplicaControlPlane:
		experimentalFeatures.ControlPlaneAvailability = coreapi.SingleReplicaControlPlane
	case coreapi.DefaultControlPlaneAvailability:
		// absent or empty
	default:
		errs = append(errs, field.Invalid(
			tagsPath.Key(metadataapi.TagClusterSingleReplica), singleReplicaValue,
			fmt.Sprintf("must be %q or empty", coreapi.SingleReplicaControlPlane),
		))
	}

	sizeOverrideValue := lookupTag(tags, metadataapi.TagClusterSizeOverride)
	switch coreapi.ControlPlanePodSizing(sizeOverrideValue) {
	case coreapi.MinimalControlPlanePodSizing:
		experimentalFeatures.ControlPlanePodSizing = coreapi.MinimalControlPlanePodSizing
	case coreapi.DefaultControlPlanePodSizing:
		// absent or empty
	default:
		errs = append(errs, field.Invalid(
			tagsPath.Key(metadataapi.TagClusterSizeOverride), sizeOverrideValue,
			fmt.Sprintf("must be %q or empty", coreapi.MinimalControlPlanePodSizing),
		))
	}

	cpoImageValue := lookupTag(tags, metadataapi.TagClusterCPOImageOverride)
	if cpoImageValue != "" {
		trimmed := strings.TrimSpace(cpoImageValue)
		if trimmed == "" {
			errs = append(errs, field.Invalid(
				tagsPath.Key(metadataapi.TagClusterCPOImageOverride), cpoImageValue,
				"must not be blank when provided",
			))
		} else {
			experimentalFeatures.ControlPlaneOperatorImage = trimmed
		}
	}

	// The control-plane-exact-version tag is handled entirely by
	// mutateClusterControlPlaneExactVersion (which also reconciles it against
	// version.id), so it is intentionally not translated here.

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

// hasTag reports whether the given tag key is present (case-insensitive),
// regardless of its value. Unlike lookupTag it distinguishes an empty-valued
// tag from an absent one, which the exact-version relocation relies on to treat
// the tag as a bare enable flag.
func hasTag(tags map[string]string, key string) bool {
	for k := range tags {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

const defaultCreateOperationCompletionDeadlineDuration = 60 * time.Minute
const minCreateOperationCompletionDeadlineDuration = time.Minute

// DefaultDeleteOperationCompletionDeadlineDuration is the fallback duration
// used by the frontend DELETE handler when DeleteOperationCompletionTimeout
// is nil (no tag / no AFEC).
const DefaultDeleteOperationCompletionDeadlineDuration = 12 * time.Hour

// MinDeleteOperationCompletionDeadlineDuration is the minimum value accepted
// for the TagClusterMaxDeletionDuration tag.
const MinDeleteOperationCompletionDeadlineDuration = time.Minute

// mutateCreateOperationCompletionDeadline sets the deadline by which a cluster
// creation operation must complete. On CREATE it defaults to 60 minutes from
// now; when the subscription has the ExperimentalReleaseFeatures AFEC
// registered, the caller may override the duration via the
// TagClusterMaxCreationDuration ARM resource tag.
func mutateCreateOperationCompletionDeadline(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, _ *field.Path, newObj **metav1.Time) field.ErrorList {
	if op.Type != operation.Create {
		return nil
	}

	duration := defaultCreateOperationCompletionDeadlineDuration

	subscription := admissionContext.Subscription
	if subscription != nil && subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		var tags map[string]string
		if admissionContext.OriginalCluster != nil {
			tags = admissionContext.OriginalCluster.Tags
		}
		if tagValue := lookupTag(tags, metadataapi.TagClusterMaxCreationDuration); len(tagValue) > 0 {
			parsed, err := time.ParseDuration(tagValue)
			if err != nil {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagClusterMaxCreationDuration), tagValue, "must be a valid Go duration string (e.g. \"19m\", \"30m\")")}
			}
			if parsed < minCreateOperationCompletionDeadlineDuration {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagClusterMaxCreationDuration), tagValue, fmt.Sprintf("must be at least %s", minCreateOperationCompletionDeadlineDuration))}
			}
			duration = parsed
		}
	}

	deadline := metav1.NewTime(admissionContext.Clock.Now().Add(duration))
	*newObj = &deadline
	return nil
}

// mutateDeleteOperationCompletionTimeout sets or clears
// DeleteOperationCompletionTimeout based on the TagClusterMaxDeletionDuration
// tag. Runs on both CREATE and UPDATE so that tag changes are reflected.
// When the AFEC is not registered or the tag is absent, the field is set to nil
// and the frontend DELETE handler falls back to DefaultDeleteOperationCompletionDeadlineDuration.
func mutateDeleteOperationCompletionTimeout(_ context.Context, admissionContext *ClusterAdmissionContext, _ operation.Operation, _ *field.Path, newObj **time.Duration) field.ErrorList {
	subscription := admissionContext.Subscription
	if subscription == nil || !subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		*newObj = nil
		return nil
	}

	var tags map[string]string
	if admissionContext.OriginalCluster != nil {
		tags = admissionContext.OriginalCluster.Tags
	}
	tagValue := lookupTag(tags, metadataapi.TagClusterMaxDeletionDuration)
	if len(tagValue) == 0 {
		*newObj = nil
		return nil
	}

	parsed, err := time.ParseDuration(tagValue)
	if err != nil {
		tagsPath := field.NewPath("tags")
		return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagClusterMaxDeletionDuration), tagValue, "must be a valid Go duration string (e.g. \"24m\", \"30m\")")}
	}
	if parsed < MinDeleteOperationCompletionDeadlineDuration {
		tagsPath := field.NewPath("tags")
		return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagClusterMaxDeletionDuration), tagValue, fmt.Sprintf("must be at least %s", MinDeleteOperationCompletionDeadlineDuration))}
	}
	*newObj = &parsed
	return nil
}

// AdmitCluster performs non-static checks of cluster. Checks that require more
// information than is contained inside of the cluster instance itself. For
// UPDATE operations that may change the cluster version, the admissionContext
// must carry the prefetched ServiceProviderCluster and ClusterNodePools.
func AdmitCluster(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, newObj, oldObj *coreapi.HCPOpenShiftCluster) field.ErrorList {
	if op.Type == operation.Update && oldObj != nil && oldObj.ServiceProviderProperties.DeletionTimestamp != nil {
		return field.ErrorList{field.Forbidden(field.NewPath(""), "cluster is being deleted and cannot be updated")}
	}

	errs := field.ErrorList{}

	// CustomerProperties HCPOpenShiftClusterCustomerProperties `json:"customerProperties,omitempty"`
	errs = append(errs, admitClusterCustomerProperties(ctx, admissionContext, op, field.NewPath("properties"), &newObj.CustomerProperties, safe.Field(oldObj, validation.ToClusterCustomerProperties))...)

	return errs
}

// admitClusterCustomerProperties drills down into the customer-facing portion
// of the cluster.
func admitClusterCustomerProperties(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitClusterVersionProfile(ctx, admissionContext, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, validation.ToClusterCustomerPropertiesVersion))...)
	errs = append(errs, admitClusterEtcdKmsKeyVersionChange(ctx, admissionContext, op, fldPath.Child("etcd", "dataEncryption", "customerManaged", "kms", "activeKey", "version"), newObj, oldObj)...)
	errs = append(errs, admitClusterPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform)...)

	return errs
}

func admitClusterPlatform(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.CustomerPlatformProfile) field.ErrorList {
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
func admitClusterManagedResourceGroupName(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.CustomerPlatformProfile) field.ErrorList {
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
func admitClusterSubnetResourceID(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.CustomerPlatformProfile) field.ErrorList {
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
func admitClusterNetworkSecurityGroupResourceID(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.CustomerPlatformProfile) field.ErrorList {
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
func admitClusterVersionProfile(ctx context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.VersionProfile) field.ErrorList {
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

	// Reject the version change if the requested update channel has no reachable
	// upgrade edge for this cluster. This only fires once the backend has
	// mirrored a non-empty channel list onto the ServiceProviderCluster; until
	// then it fails open (see admitClusterVersionID).
	errs = append(errs, admitClusterVersionID(ctx, admissionContext, op, fldPath, newObj, oldObj)...)

	return errs
}

// versionMajorMinor extracts the "<major>.<minor>" release line from an
// OpenShift version ID. Update channels are always named
// "<channelGroup>-<major>.<minor>" (e.g. "stable-4.20") — never with a patch or
// pre-release suffix — so channel lookups must use the major.minor of the
// requested version rather than its raw ID. Examples: "4.20" -> "4.20",
// "4.20.8" -> "4.20", "5.0.0-0.nightly-2026-08-05-123456" -> "5.0",
// "4.21.0-rc.1" -> "4.21". It returns an error when the ID cannot be parsed as
// a (tolerant) semantic version.
func versionMajorMinor(id string) (string, error) {
	v, err := semver.ParseTolerant(id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor), nil
}

// admitClusterVersionID verifies that, when the cluster's target version.id
// changes, an OpenShift update channel named "<channelGroup>-<major>.<minor>"
// (e.g. "stable-4.20") is present in the associated HostedCluster's observed
// status.version.desired.channels. The requested version's major.minor is
// derived with versionMajorMinor, so patch ("4.20.8"), nightly
// ("5.0.0-0.nightly-...") and pre-release ("4.21.0-rc.1") IDs all resolve to the
// correct release-line channel.
//
// Admission never reaches the management cluster (or the DB) for this data: the
// backend observes the HostedCluster and mirrors its
// status.version.desired.channels onto ServiceProviderCluster.Status.
// DesiredVersionChannels, which the frontend prefetches into the admission
// context. See internal/admission/CLAUDE.md.
//
// Semantics of the channel list: a channel only appears in
// status.version.desired.channels when the cluster's current version has a valid
// upgrade edge to a release served by that channel. Therefore, once the list is
// populated, a requested "<channelGroup>-<major>.<minor>" channel that is absent
// means there is no supported upgrade path to that version line and the update is
// rejected.
//
// Fail-open until synced: DesiredVersionChannels is populated asynchronously by
// the backend and is empty on freshly created clusters and until the first sync
// completes. When the list is not yet available we cannot validate the requested
// channel, so we must NOT block the update — we skip the check and let the
// backend converge. Enforcing against an empty list would reject every version
// change until the mirror is populated, which is incorrect. This check therefore
// only rejects when the backend has published a non-empty channel list that
// genuinely lacks the requested channel.
func admitClusterVersionID(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.VersionProfile) field.ErrorList {
	// Only enforce on UPDATE, and only when the customer is actually changing
	// version.id. On CREATE there is no prior version, and an unchanged version
	// need not be re-validated against the current channel list.
	if op.Type != operation.Update || oldObj == nil {
		return nil
	}
	if len(newObj.ID) == 0 || oldObj.ID == newObj.ID {
		return nil
	}

	// Without a channel group we cannot construct a channel name to look for, so
	// there is nothing to validate here. This mirrors the desired-version
	// controller, which also terminates version resolution when the channel
	// group is empty.
	if len(newObj.ChannelGroup) == 0 {
		return nil
	}
	// on nightlies and candidates, we don't wait for the target version to show up. We trust these people to
	// know what they're doing.
	if newObj.ChannelGroup == "nightly" || newObj.ChannelGroup == "candidate" {
		return nil
	}

	// No channel data yet: fail open (see the doc comment above). A genuinely
	// missing ServiceProviderCluster prefetch is still surfaced as an
	// InternalError by the version-skew check in admitClusterVersionProfile, so
	// we do not duplicate that here.
	if admissionContext.ServiceProviderCluster == nil {
		return nil
	}
	availableChannels := admissionContext.ServiceProviderCluster.Status.DesiredVersionChannels
	if len(availableChannels) == 0 {
		return nil
	}

	// Channels are keyed by release line ("<channelGroup>-<major>.<minor>"), so
	// compare against the major.minor of the requested version rather than its
	// raw ID (which may carry a patch or pre-release suffix).
	majorMinor, err := versionMajorMinor(newObj.ID)
	if err != nil {
		// The version ID failed to parse. admitClusterVersionProfile already
		// reports the parse failure as a field error, so skip the channel check
		// here rather than surfacing a duplicate error.
		return nil
	}
	expectedChannel := fmt.Sprintf("%s-%s", newObj.ChannelGroup, majorMinor)
	if slices.Contains(availableChannels, expectedChannel) {
		return nil
	}

	versionPath := fldPath.Child("id")
	return field.ErrorList{field.Invalid(
		versionPath,
		newObj.ID,
		fmt.Sprintf("no upgrade path to update channel %q is currently available for this cluster; "+
			"a channel appears in the cluster's desired version channels only when an upgrade edge to a "+
			"release in that channel exists", expectedChannel),
	)}
}

// minKmsKeyVersionRotationVersion is the minimum OCP version whose CPO
// supports etcd KMS key version rotation.
var minKmsKeyVersionRotationVersion = semver.Version{Major: 4, Minor: 22}

// admitClusterEtcdKmsKeyVersionChange rejects KMS key version changes when the
// cluster's active control plane version predates the CPO support (< 4.22).
// Only runs for API versions >= v20260630Preview where version is mutable;
// older API versions block version changes via the immutability check in validation.
func admitClusterEtcdKmsKeyVersionChange(_ context.Context, admissionContext *ClusterAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterCustomerProperties) field.ErrorList {
	if op.Type != operation.Update || oldObj == nil {
		return nil
	}

	if newObj.Etcd.DataEncryption.KeyManagementMode != metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged && newObj.Etcd.DataEncryption.KeyManagementMode != oldObj.Etcd.DataEncryption.KeyManagementMode {
		return field.ErrorList{field.Forbidden(fldPath, "KMS key version rotation is only supported for customer-managed encryption")}
	}

	if newObj.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version == oldObj.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version {
		return nil
	}

	apiVersion := metadataapi.APIVersionFromOptions(op.Options)
	if apiVersion.LT(metadataapi.APIVersionV20260630Preview) {
		return field.ErrorList{field.Forbidden(fldPath, "KMS key version is immutable for this API version")}
	}

	if admissionContext.ServiceProviderCluster == nil {
		return field.ErrorList{field.InternalError(fldPath, errors.New("cannot validate KMS key version rotation"))}
	}

	lowest, _ := apihelpers.FindLowestAndHighestClusterVersion(admissionContext.ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)
	clusterVersion := semver.Version{Major: lowest.Major, Minor: lowest.Minor}
	if clusterVersion.LT(minKmsKeyVersionRotationVersion) {
		return field.ErrorList{field.Invalid(fldPath, newObj.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version, fmt.Sprintf("KMS key version rotation requires cluster version %s or above", minKmsKeyVersionRotationVersion))}
	}

	return nil
}
