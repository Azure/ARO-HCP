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

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// NodePoolAdmissionContext carries dependencies that node pool mutation/admission needs
// beyond the node pool object itself. It includes the parent cluster and optionally
// the service provider cluster and nodepool (for update-specific validations like version upgrades).
type NodePoolAdmissionContext struct {
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
// the nodepool instance itself. For update operations with version changes, include ServiceProviderNodePool and
// ServiceProviderCluster in the admissionContext to enable version upgrade validation.
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

	// Perform update-specific version upgrade validation
	if op.Type == operation.Update {
		errs = append(errs, validateNodePoolVersionChange(ctx, admissionContext, op, fldPath.Child("id"), newObj, oldObj)...)
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

// validateNodePoolVersionChange validates that a node pool version change is valid.
// It checks:
//   - Upgrade: at most +2 minor versions from current, and cannot exceed lowest control plane version
//   - Downgrade: at most -2 minor versions from the highest control plane version
//   - Cross-major changes (either direction) require AFEC FeatureExperimentalReleaseFeatures
//   - NP version must be in the allowed skew map when CP and NP are on different majors
func validateNodePoolVersionChange(ctx context.Context, admissionContext *NodePoolAdmissionContext, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolVersionProfile) field.ErrorList {
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
