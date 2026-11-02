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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// NodePoolAdmissionContext carries dependencies that node pool mutation/admission needs
// beyond the node pool object itself. It includes the parent cluster and the service
// provider cluster (required for version skew validation at CREATE and UPDATE time).
// ServiceProviderNodePool is required for UPDATE only.
type NodePoolAdmissionContext struct {
	Clock        utilsclock.PassiveClock
	Subscription *coreapi.Subscription
	// OriginalNodePool is a deepcopy of the inbound node pool as the user submitted
	// it, taken before any admission mutation runs. It is the read-only source
	// of truth for fields (like tags) that are *consumed* during mutation but
	// whose new-object value may already have been overwritten by the time the
	// mutation actually runs.
	OriginalNodePool        *coreapi.NodePool
	Cluster                 *coreapi.Cluster
	ServiceProviderNodePool *coreapi.ServiceProviderNodePool
	ServiceProviderCluster  *coreapi.ServiceProviderCluster
	// ClusterNodePools is every node pool for the parent cluster. It is populated
	// on both CREATE and UPDATE for cluster-wide, cross-node-pool checks. On
	// UPDATE this includes the node pool being updated, carrying its stored
	// (pre-update) state. On CREATE the node pool being admitted does not exist
	// yet, so it is never included here.
	ClusterNodePools []*coreapi.NodePool
}

// MutateNodePool applies admission-time mutations to a node pool (e.g. defaulting
// the subnet from the parent cluster on CREATE). It returns any field errors
// produced by the mutation step.
func MutateNodePool(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, newObj, oldObj *coreapi.NodePool) field.ErrorList {
	errs := field.ErrorList{}

	//Properties NodePoolProperties `json:"properties"`
	errs = append(errs, mutateNodePoolProperties(ctx, admissionContext, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, validation.ToNodePoolProperties))...)

	errs = append(errs, mutateNodePoolServiceProviderProperties(ctx, admissionContext, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties)...)

	return errs
}

func mutateNodePoolProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateNodePoolPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, validation.ToNodePoolPropertiesPlatform))...)

	return errs
}

func mutateNodePoolPlatform(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	if op.Type == operation.Create {
		if newObj.SubnetID == nil {
			newObj.SubnetID = admissionContext.Cluster.CustomerProperties.Platform.DeepCopy().SubnetID
		}
	}

	return errs
}

func mutateNodePoolServiceProviderProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.NodePoolServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, mutateNodePoolExperimentalTags(ctx, admissionContext, op)...)
	errs = append(errs, mutateNodePoolCreateOperationCompletionDeadline(ctx, admissionContext, op, fldPath.Child("createOperationCompletionDeadline"), &newObj.CreateOperationCompletionDeadline)...)
	errs = append(errs, mutateNodePoolUpdateOperationCompletionDeadline(ctx, admissionContext, op, fldPath.Child("updateOperationCompletionDeadline"), &newObj.UpdateOperationCompletionDeadline)...)

	return errs
}

// mutateNodePoolExperimentalTags rejects unrecognized experimental node pool
// tags when the ExperimentalReleaseFeatures AFEC is registered.
func mutateNodePoolExperimentalTags(_ context.Context, admissionContext *NodePoolAdmissionContext, _ operation.Operation) field.ErrorList {
	subscription := admissionContext.Subscription
	if subscription == nil || !subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		return nil
	}

	var tags map[string]string
	if admissionContext.OriginalNodePool != nil {
		tags = admissionContext.OriginalNodePool.Tags
	}
	tagsPath := field.NewPath("tags")
	var errs field.ErrorList

	knownTags := sets.New(metadataapi.TagNodePoolMaxCreationDuration, metadataapi.TagNodePoolMaxUpdateDuration)
	for k := range tags {
		if strings.HasPrefix(strings.ToLower(k), metadataapi.ExperimentalNodePoolTagPrefix) && !knownTags.Has(strings.ToLower(k)) {
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
	if subscription != nil && subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		var tags map[string]string
		if admissionContext.OriginalNodePool != nil {
			tags = admissionContext.OriginalNodePool.Tags
		}
		if tagValue := lookupTag(tags, metadataapi.TagNodePoolMaxCreationDuration); len(tagValue) > 0 {
			parsed, err := time.ParseDuration(tagValue)
			if err != nil {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagNodePoolMaxCreationDuration), tagValue, "must be a valid Go duration string (e.g. \"19m\", \"30m\")")}
			}
			if parsed < minCreateOperationCompletionDeadlineDuration {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagNodePoolMaxCreationDuration), tagValue, fmt.Sprintf("must be at least %s", minCreateOperationCompletionDeadlineDuration))}
			}
			duration = parsed
		}
	}

	deadline := metav1.NewTime(admissionContext.Clock.Now().Add(duration))
	*newObj = &deadline
	return nil
}

// mutateNodePoolUpdateOperationCompletionDeadline sets the deadline by which a
// node pool update operation must complete. On UPDATE it defaults to 60
// minutes from now; when the subscription has the ExperimentalReleaseFeatures
// AFEC registered, the caller may override the duration via the
// TagNodePoolMaxUpdateDuration ARM resource tag.
func mutateNodePoolUpdateOperationCompletionDeadline(_ context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, _ *field.Path, newObj **metav1.Time) field.ErrorList {
	if op.Type != operation.Update {
		return nil
	}

	duration := defaultUpdateOperationCompletionDeadlineDuration

	subscription := admissionContext.Subscription
	if subscription != nil && subscription.HasRegisteredFeature(metadataapi.FeatureExperimentalReleaseFeatures) {
		var tags map[string]string
		if admissionContext.OriginalNodePool != nil {
			tags = admissionContext.OriginalNodePool.Tags
		}
		if tagValue := lookupTag(tags, metadataapi.TagNodePoolMaxUpdateDuration); len(tagValue) > 0 {
			parsed, err := time.ParseDuration(tagValue)
			if err != nil {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagNodePoolMaxUpdateDuration), tagValue, "must be a valid Go duration string (e.g. \"19m\", \"30m\")")}
			}
			if parsed < minUpdateOperationCompletionDeadlineDuration {
				tagsPath := field.NewPath("tags")
				return field.ErrorList{field.Invalid(tagsPath.Key(metadataapi.TagNodePoolMaxUpdateDuration), tagValue, fmt.Sprintf("must be at least %s", minUpdateOperationCompletionDeadlineDuration))}
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
	// NodePools is a list of all node pools for the cluster, including the one being deleted.
	// Node pools whose deletion has already been requested (ServiceProviderProperties.DeletionTimestamp
	// set) are also here, since their Cosmos document is only removed once the underlying
	// teardown completes.
	NodePools []*coreapi.NodePool
}

// AdmitNodePoolOnDelete performs non-static checks before deleting a node pool.
func AdmitNodePoolOnDelete(ctx context.Context, admissionContext *NodePoolDeleteAdmissionContext, nodePoolBeingDeleted *coreapi.NodePool) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitAtLeastOneNodePoolRemains(ctx, admissionContext, nodePoolBeingDeleted)...)

	return errs
}

// admitAtLeastOneNodePoolRemains is a *best-effort* check to see if we are the last node pool on the
// cluster and prevent deletion if we are. This is because as of now (2026-05-29) it is not possible
// to delete the last node pool from the cluster OCPBUGS-86702. This check won't fully prevent the
// last node pool deletion in all cases as there are edge cases where race conditions can occur, but
// it should be good enough to prevent the last node pool deletion in most cases.
// admissionContext.NodePools includes node pools whose deletion has already been requested
// (DeletionTimestamp set) but whose document has not yet been removed. Those must not count as
// survivors, or the last active node pool could be deleted concurrently with others already tearing
// down, leaving zero, which is what this check is trying to prevent.
// TODO once OCPBUGS-86702 is fixed, we should remove this check.
func admitAtLeastOneNodePoolRemains(_ context.Context, admissionContext *NodePoolDeleteAdmissionContext, nodePoolBeingDeleted *coreapi.NodePool) field.ErrorList {
	// A retried DELETE call on a node pool whose deletion was already accepted skips this check
	// rather than being re-evaluated by it: the deletion is already underway and will proceed
	// regardless of this request's outcome, so the retry is accepted idempotently here.
	if nodePoolBeingDeleted.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	for _, nodePool := range admissionContext.NodePools {
		if strings.EqualFold(nodePool.ResourceID.String(), nodePoolBeingDeleted.ResourceID.String()) {
			continue
		}
		if nodePool.ServiceProviderProperties.DeletionTimestamp == nil {
			// At least one of the other node pools is not being deleted, so we can allow the deletion.
			return nil
		}
	}
	return field.ErrorList{field.Forbidden(field.NewPath("name"), "The last node pool can not be deleted from a cluster.")}
}

// AdmitNodePool performs non-static checks of nodepool. Checks that require more information than is contained inside of
// the nodepool instance itself. ServiceProviderCluster must be specified in the admissionContext (version skew validation).
// For update operations, ServiceProviderNodePool must also be specified.
func AdmitNodePool(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, newNodePool, oldNodePool *coreapi.NodePool) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitNodePoolProperties(ctx, admissionContext, op, field.NewPath("properties"), &newNodePool.Properties, safe.Field(oldNodePool, validation.ToNodePoolProperties))...)

	return errs
}

func admitNodePoolProperties(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitNodePoolVersion(ctx, admissionContext, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, validation.ToNodePoolPropertiesVersion))...)
	errs = append(errs, admitNodePoolPlatform(ctx, admissionContext, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, validation.ToNodePoolPropertiesPlatform))...)
	errs = append(errs, admitReplicas(ctx, admissionContext, op, fldPath.Child("replicas"), newObj, oldObj)...)
	errs = append(errs, admitClusterComputeNodeCapacity(ctx, admissionContext, op, fldPath, newObj)...)
	return errs
}

func admitNodePoolVersion(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolVersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, validateNodePoolVersionChange(ctx, admissionContext, op, fldPath.Child("id"), newObj, oldObj)...)

	return errs
}

func admitNodePoolPlatform(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolPlatformProfile) field.ErrorList {
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

	errs = append(errs, admitNodePoolOSDisk(ctx, admissionContext, op, fldPath.Child("osDisk"), &newObj.OSDisk)...)

	return errs
}

func admitNodePoolOSDisk(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.OSDiskProfile) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, admitNodePoolOSDiskEncryptionSet(ctx, admissionContext, op, fldPath, newObj)...)

	return errs
}

// admitNodePoolOSDiskEncryptionSet ensures the OS disk's encryption set isn't
// in the cluster's managed resource group.
func admitNodePoolOSDiskEncryptionSet(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.OSDiskProfile) field.ErrorList {
	if newObj.EncryptionSetID == nil {
		return nil
	}

	clusterManagedResourceGroupName := admissionContext.Cluster.CustomerProperties.Platform.ManagedResourceGroup
	return validation.DifferentResourceGroupNameFromResourceID(ctx, op, fldPath.Child("encryptionSetId"), newObj.EncryptionSetID, nil, clusterManagedResourceGroupName)
}

// admitReplicas reduces the chances (not guaranteed) that updates drive the cluster
// to zero worker nodes. Zero nodes blocks cluster workloads like the ingress operator
// (see https://redhat.atlassian.net/browse/OCPBUGS-85364). Fixed replicas=0 is forced
// zero; autoscaling min=0 is not (autoscaler keeps pods schedulable).
//
// Rejects capacity-removing updates (fixed >0→0 or autoscaling max>0→0) only when no
// other pool provides capacity. Label/version changes and pre-existing zero pools pass.
//
// Limitation: Cached state allows concurrent updates to race. Both pools providing
// capacity can receive concurrent reduce-capacity updates, each see old state, both
// pass, and leave the cluster at zero. The operation deadline only bounds stuck
// updates; recovery requires a customer-issued corrective update.
//
//	Only runs on UPDATE; CREATE is additive.
func admitReplicas(_ context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolProperties) field.ErrorList {
	if op.Type != operation.Update || oldObj == nil {
		return nil
	}

	// Act only when this update takes a capacity-providing pool to a forced zero.
	if canRunWorkerNodes(newObj) || !canRunWorkerNodes(oldObj) {
		return nil
	}

	// The cluster is fine as long as any other node pool still provides capacity.
	// The lister copy of the pool under update carries its stored (old) state, so
	// skip it.
	existingNodePoolID := admissionContext.OriginalNodePool.ID
	for _, np := range admissionContext.ClusterNodePools {

		if strings.EqualFold(np.ID.String(), existingNodePoolID.String()) {
			continue
		}
		// Do not count a nodepool that is being deleted
		if np.ServiceProviderProperties.DeletionTimestamp != nil {
			continue
		}
		if canRunWorkerNodes(&np.Properties) {
			return nil
		}
	}

	return field.ErrorList{field.Forbidden(
		fldPath,
		"cannot scale this node pool down to zero replicas.",
	)}
}

// canRunWorkerNodes reports whether a node pool can run at least one worker node,
// now or via autoscaling. A fixed-size pool can when Replicas>0. An autoscaling
// pool can when it is allowed to run nodes (Max>0) even if Min=0, because the
// cluster autoscaler keeps nodes that running workloads need and can scale up from
// zero; it is never a forced zero.
func canRunWorkerNodes(np *coreapi.NodePoolProperties) bool {
	return nodePoolNodeCapacity(np) > 0
}

// nodePoolNodeCapacity returns the maximum number of compute nodes a node pool can
// run: AutoScaling.Max when autoscaling is enabled (the cluster autoscaler may
// scale up to that many nodes regardless of the current replica count), otherwise
// the fixed Replicas count.
func nodePoolNodeCapacity(np *coreapi.NodePoolProperties) int32 {
	if np.AutoScaling != nil {
		return np.AutoScaling.Max
	}
	return np.Replicas
}

// maxClusterComputeNodes is the hard cap on the total number of compute nodes a
// single cluster may have, summed across every node pool. 500 is the maximum
// that has been PerfScale validated and tested; exceeding it should not
// hard-break the cluster but may cause control-plane strain and noisy-neighbor
// effects. This limit is not discoverable at runtime and must be updated here
// manually if it ever changes.
const maxClusterComputeNodes = 500

// clusterAutoscalerMaxNodesTotal returns the cluster-wide, cross-node-pool
// autoscaler cap configured on the cluster, or 0 if none is configured. When
// set, a single shared cap governs every autoscaling-enabled node pool on the
// cluster collectively, instead of each pool being allowed to independently
// reach its own AutoScaling.Max.
func clusterAutoscalerMaxNodesTotal(cluster *coreapi.Cluster) int32 {
	if cluster == nil {
		return 0
	}
	return cluster.CustomerProperties.Autoscaling.MaxNodesTotal
}

// admitClusterComputeNodeCapacity enforces maxClusterComputeNodes at admission
// time, on both CREATE and UPDATE. It sums every node pool on the cluster (newObj
// plus the siblings in admissionContext.ClusterNodePools) into fixedReplicas
// (pools without autoscaling) and minReplicas/maxReplicas (sum of
// AutoScaling.Min/Max for pools with autoscaling), then runs two checks:
//
//  1. Reject if the guaranteed floor, fixedReplicas+minReplicas, already exceeds
//     the limit — every autoscaling pool keeps at least its Min nodes running,
//     so this can never be satisfied even when pools are scaled all the way down.
//  2. Otherwise reject if the worst-case ceiling exceeds the limit. Ordinarily
//     that ceiling is fixedReplicas+maxReplicas (every autoscaling pool scaled
//     all the way up independently). But when the cluster has a cluster-wide
//     autoscaler cap (clusterAutoscalerMaxNodesTotal), that single shared cap
//     replaces the per-pool maxReplicas sum: fixedReplicas+minReplicas+globalMax.
func admitClusterComputeNodeCapacity(_ context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj *coreapi.NodePoolProperties) field.ErrorList {
	if op.Type != operation.Create && op.Type != operation.Update {
		return nil
	}

	// On UPDATE, admissionContext.ClusterNodePools includes the pool being updated
	// (its stored, pre-update state); skip it so it isn't counted twice alongside
	// newObj. On CREATE, the pool being admitted doesn't exist in that list yet,
	// so OriginalNodePool.ID never matches anything there.
	var existingNodePoolID string
	if admissionContext.OriginalNodePool != nil && admissionContext.OriginalNodePool.ID != nil {
		existingNodePoolID = admissionContext.OriginalNodePool.ID.String()
	}

	var fixedReplicas, minReplicas, maxReplicas int32
	addNodePool := func(np *coreapi.NodePoolProperties) {
		if np.AutoScaling != nil {
			minReplicas += np.AutoScaling.Min
			maxReplicas += np.AutoScaling.Max
			return
		}
		fixedReplicas += np.Replicas
	}

	addNodePool(newObj)
	for _, np := range admissionContext.ClusterNodePools {
		if len(existingNodePoolID) > 0 && strings.EqualFold(np.ID.String(), existingNodePoolID) {
			continue
		}
		// A node pool being deleted will not hold its capacity going forward.
		if np.ServiceProviderProperties.DeletionTimestamp != nil {
			continue
		}
		addNodePool(&np.Properties)
	}

	// Errors are reported against newObj's own replicas/autoScaling fields (the
	// node pool actually being admitted), even though the sum spans every node
	// pool on the cluster.
	floorPath, ceilingPath := fldPath.Child("replicas"), fldPath.Child("replicas")
	if newObj.AutoScaling != nil {
		floorPath, ceilingPath = fldPath.Child("autoScaling").Child("min"), fldPath.Child("autoScaling").Child("max")
	}

	if floor := fixedReplicas + minReplicas; floor > maxClusterComputeNodes {
		return field.ErrorList{field.Invalid(floorPath, floor, fmt.Sprintf(
			"the guaranteed minimum number of compute nodes for this cluster '%d' (sum of fixed-size replicas and autoscaling minimums across all node pools) exceeds the maximum allowed '%d'; reduce the replica count or autoscaling minimum to be within the maximum allowed",
			floor, maxClusterComputeNodes))}
	}

	// Without a cluster-wide autoscaler, each autoscaling pool can independently
	// scale up to its own AutoScaling.Max, so the worst case is the sum of all
	// per-pool Max values plus fixed-size pools.
	ceiling := fixedReplicas + maxReplicas
	if globalMax := clusterAutoscalerMaxNodesTotal(admissionContext.Cluster); globalMax > 0 {
		// When a cluster-wide autoscaler is configured, it replaces the per-pool
		// Max values with a single shared cap (MaxNodesTotal) that governs all
		// autoscaling pools collectively. The worst case then becomes: fixed-size
		// pools (always running) + autoscaling pools' guaranteed minimums (always
		// running) + the shared autoscaler budget. Note: globalMax is the
		// user-configured cluster autoscaler cap, NOT the 500-node hard limit —
		// it is already validated to be <= 500 by static validation on the
		// Cluster resource (validate_cluster.go).
		ceiling = fixedReplicas + minReplicas + globalMax
	}
	if ceiling > maxClusterComputeNodes {
		return field.ErrorList{field.Invalid(ceilingPath, ceiling, fmt.Sprintf(
			"the maximum possible number of compute nodes for this cluster '%d' (sum of fixed-size replicas and autoscaling maximums across all node pools) exceeds the maximum allowed '%d'; reduce the replica count or autoscaling maximum to be within the maximum allowed",
			ceiling, maxClusterComputeNodes))}
	}

	return nil
}

// validateNodePoolVersionChange validates that a node pool version change is valid, at both CREATE and UPDATE time.
// It checks:
//   - Upgrade: at most +2 minor versions from current, and cannot exceed lowest control plane version
//   - Downgrade: at most -2 minor versions from the highest control plane version
//   - Cross-major changes (either direction) require AFEC FeatureExperimentalReleaseFeatures
//   - NP version must be in the allowed skew map when CP and NP are on different majors
func validateNodePoolVersionChange(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolVersionProfile) field.ErrorList {
	spNodePool, spCluster := admissionContext.ServiceProviderNodePool, admissionContext.ServiceProviderCluster
	// Skip validation if no version is specified or version didn't change
	if len(newObj.ID) == 0 || (oldObj != nil && newObj.ID == oldObj.ID) {
		return nil
	}

	errs := field.ErrorList{}

	newVersion, err := semver.Parse(newObj.ID)
	if err != nil {
		errs = append(errs, field.Invalid(fldPath, newObj.ID, fmt.Sprintf("invalid node pool version format: %s", err.Error())))
		// Return early, it cannot validate an unparseable version
		return errs
	}

	var activeVersions []coreapi.ServiceProviderNodePoolActiveVersion
	if spNodePool != nil {
		// Skip validation if the newVersion hasn't changed from the desired Version
		if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil &&
			newVersion.EQ(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
			return nil
		}
		activeVersions = spNodePool.Status.NodePoolVersion.ActiveVersions
	}

	lowestCPVersion, highestCPVersion := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	experimentalFeaturesEnabled, _ := op.HasOption(metadataapi.FeatureExperimentalReleaseFeatures)
	if err := validation.ValidateNodePoolVersionChange(newVersion, activeVersions, lowestCPVersion, highestCPVersion, experimentalFeaturesEnabled); err != nil {
		errs = append(errs, field.Invalid(fldPath, newObj.ID, err.Error()))
	}

	return errs
}
