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

// NodePoolAdmissionContext carries dependencies that node pool mutation needs
// beyond the node pool object itself, currently the parent cluster.
type NodePoolAdmissionContext struct {
	Cluster *api.HCPOpenShiftCluster
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

// AdmitNodePool performs non-static checks of nodepool.  Checks that require more information than is contained inside of
// of the nodepool instance itself.
func AdmitNodePool(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	// Check only if it is a creating nodepool or a change in the channelGroup
	if (oldNodePool == nil || newNodePool.Properties.Version.ChannelGroup != oldNodePool.Properties.Version.ChannelGroup) &&
		newNodePool.Properties.Version.ChannelGroup != cluster.CustomerProperties.Version.ChannelGroup {
		errs = append(errs, field.Invalid(
			field.NewPath("properties", "version", "channelGroup"),
			newNodePool.Properties.Version.ChannelGroup,
			fmt.Sprintf("must be the same as control plane channel group '%s'", cluster.CustomerProperties.Version.ChannelGroup),
		))
	}

	// Check only if it is a creating nodepool or a change in the Subnet.
	// Compare by string value (not pointer identity) so equal-but-distinct
	// *azcorearm.ResourceID values aren't treated as a change.
	if newNodePool.Properties.Platform.SubnetID != nil && cluster.CustomerProperties.Platform.SubnetID != nil {
		var oldSubnetID string
		if oldNodePool != nil && oldNodePool.Properties.Platform.SubnetID != nil {
			oldSubnetID = oldNodePool.Properties.Platform.SubnetID.String()
		}
		newSubnetID := newNodePool.Properties.Platform.SubnetID.String()
		if oldNodePool == nil || !strings.EqualFold(newSubnetID, oldSubnetID) {
			clusterVNet := cluster.CustomerProperties.Platform.SubnetID.Parent.String()
			nodePoolVNet := newNodePool.Properties.Platform.SubnetID.Parent.String()
			if !strings.EqualFold(nodePoolVNet, clusterVNet) {
				errs = append(errs, field.Invalid(
					field.NewPath("properties", "platform", "subnetId"),
					newNodePool.Properties.Platform.SubnetID,
					fmt.Sprintf("must belong to the same VNet as the parent cluster VNet '%s'", clusterVNet),
				))
			}
		}
	}

	return errs
}

// AdmitNodePoolUpdate performs update-specific validation that requires both the old and new node pool states.
// It includes all validations from AdmitNodePool plus version upgrade constraints.
// The spNodePool and spCluster parameters provide the service provider state for version validation.
func AdmitNodePoolUpdate(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster,
	spNodePool *api.ServiceProviderNodePool, spCluster *api.ServiceProviderCluster, op operation.Operation) field.ErrorList {
	errs := field.ErrorList{}

	// Include all standard node pool admission checks
	errs = append(errs, AdmitNodePool(newNodePool, oldNodePool, cluster)...)

	// Add update-specific version upgrade validation
	errs = append(errs, validateNodePoolVersionUpgrade(newNodePool, oldNodePool, spNodePool, spCluster, op)...)

	return errs
}

// validateNodePoolVersionUpgrade validates that a node pool version change is a valid upgrade.
// It checks:
//   - No downgrade: new version >= old version
//   - No major version change: new major == old major (unless FeatureExperimentalReleaseFeatures is registered)
//   - No skipping minor versions: new minor <= old minor + 1
//   - Cannot exceed cluster version: new version <= cluster version
func validateNodePoolVersionUpgrade(newNodePool, oldNodePool *api.HCPOpenShiftClusterNodePool, spNodePool *api.ServiceProviderNodePool, spCluster *api.ServiceProviderCluster, op operation.Operation) field.ErrorList {

	// Skip validation if no version is specified or version didn't change
	if len(newNodePool.Properties.Version.ID) == 0 || newNodePool.Properties.Version.ID == oldNodePool.Properties.Version.ID {
		return nil
	}

	errs := field.ErrorList{}
	fldPath := field.NewPath("properties", "version", "id")

	newVersion, err := semver.Parse(newNodePool.Properties.Version.ID)
	if err != nil {
		errs = append(errs, field.Invalid(fldPath, newNodePool.Properties.Version.ID, fmt.Sprintf("invalid node pool version format: %s", err.Error())))
		// Return early, it cannot validate an unparseable version
		return errs
	}
	// Skip validation if the newVersion hasn't changed from the desired Version
	if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil &&
		newVersion.EQ(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
		return nil
	}

	lowestCPVersion, _ := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)
	if err := validation.ValidateNodePoolUpgrade(newVersion, spNodePool.Status.NodePoolVersion.ActiveVersions, lowestCPVersion, op.HasOption(api.FeatureExperimentalReleaseFeatures)); err != nil {
		errs = append(errs, field.Invalid(fldPath, newNodePool.Properties.Version.ID, err.Error()))
	}

	if spNodePool.Spec.NodePoolVersion.DesiredVersion != nil && newVersion.LE(*spNodePool.Spec.NodePoolVersion.DesiredVersion) {
		errs = append(errs, field.Invalid(fldPath, newNodePool.Properties.Version.ID, fmt.Sprintf("cannot downgrade from version %s to %s", spNodePool.Spec.NodePoolVersion.DesiredVersion.String(), newVersion.String())))
	}
	return errs
}
