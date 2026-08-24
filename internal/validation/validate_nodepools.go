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

package validation

import (
	"context"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/safe"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	// See https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/azure-subscription-service-limits#azure-virtual-machines-limits---azure-resource-manager
	MaxNodePoolNodes = 200

	// See https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules#microsoftcompute
	MaxDiskEncryptionSetNameLen       = 80
	nodePoolK8sLabelKeyNodeRoleMaster = "node-role.kubernetes.io/master"
	nodePoolK8sLabelKeyNodeRoleWorker = "node-role.kubernetes.io/worker"
	nodePoolK8sLabelKeyMachineRole    = "machine.openshift.io/cluster-api-machine-role"
	nodePoolK8sLabelKeyMachineType    = "machine.openshift.io/cluster-api-machine-type"

	// MaxManagedOSDiskSizeGiB is the maximum allowed OS disk size (GiB) for Managed (persistent) OS disks.
	// See https://learn.microsoft.com/en-us/azure/virtual-machines/managed-disks-overview#os-disk
	MaxManagedOSDiskSizeGiB int32 = 4095
	// MaxEphemeralOSDiskSizeGiB is the absolute maximum allowed OS disk size (GiB) for Ephemeral OS disks.
	// Azure may enforce a lower effective limit per VM size based on local cache/temp/NVMe capacity.
	// Values above this absolute cap are rejected here; values within this cap but above the selected
	// VM size's local disk capacity are accepted by this validator and fail later during Azure
	// provisioning.
	// See https://learn.microsoft.com/en-us/azure/virtual-machines/ephemeral-os-disks
	MaxEphemeralOSDiskSizeGiB int32 = 2040
)

// nodePoolForbiddenK8sLabelValuesByKey maps Kubernetes label keys to forbidden values.
// A nil value set means the entire label key is forbidden regardless of value.
var nodePoolForbiddenK8sLabelValuesByKey = map[string]map[string]struct{}{
	nodePoolK8sLabelKeyNodeRoleMaster: nil,
	nodePoolK8sLabelKeyNodeRoleWorker: nil,
	nodePoolK8sLabelKeyMachineRole: {
		"master": {},
		"infra":  {},
	},
	nodePoolK8sLabelKeyMachineType: {
		"master": {},
		"infra":  {},
	},
}

func ValidateNodePool(ctx context.Context, op operation.Operation, newObj, oldObj *coreapi.HCPOpenShiftClusterNodePool) field.ErrorList {
	return validateNodePool(ctx, op, newObj, oldObj)
}

func toNodePoolTrackedResource(oldObj *coreapi.HCPOpenShiftClusterNodePool) *coreapi.TrackedResource {
	return &oldObj.TrackedResource
}

// ToNodePoolProperties returns a pointer to the Properties field of a node pool.
// It is exported for use as a field accessor with safe.Field by external callers
// (e.g. admission code) that need to navigate into the Properties subtree.
func ToNodePoolProperties(oldObj *coreapi.HCPOpenShiftClusterNodePool) *coreapi.HCPOpenShiftClusterNodePoolProperties {
	return &oldObj.Properties
}

func toNodePoolServiceProviderProperties(oldObj *coreapi.HCPOpenShiftClusterNodePool) *coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties {
	return &oldObj.ServiceProviderProperties
}

func validateNodePool(ctx context.Context, op operation.Operation, newObj, oldObj *coreapi.HCPOpenShiftClusterNodePool) field.ErrorList {
	errs := field.ErrorList{}

	//coreapi.ProxyResource
	errs = append(errs, validateTrackedResource(ctx, op, field.NewPath("trackedResource"), &newObj.TrackedResource, safe.Field(oldObj, toNodePoolTrackedResource))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, field.NewPath("id"), newObj.ID, nil, coreapi.NodePoolResourceType.String())...)
	if newObj.ID != nil {
		errs = append(errs, MaxLen(ctx, op, field.NewPath("id"), &newObj.ID.Name, nil, 15)...)
		errs = append(errs, MatchesRegex(ctx, op, field.NewPath("id"), &newObj.ID.Name, nil, nodePoolResourceNameRegex, nodePoolResourceNameErrorString)...)
	}

	//Properties HCPOpenShiftClusterNodePoolProperties `json:"properties"`
	errs = append(errs, validateNodePoolProperties(ctx, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, ToNodePoolProperties))...)

	//ServiceProviderProperties HCPOpenShiftClusterNodePoolServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	errs = append(errs, validateNodePoolServiceProviderProperties(ctx, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties, safe.Field(oldObj, toNodePoolServiceProviderProperties))...)

	return errs
}

func toNodePoolPropertiesProvisioningState(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *coreapi.ProvisioningState {
	return &oldObj.ProvisioningState
}

// ToNodePoolPropertiesVersion returns a pointer to the Version field of node
// pool properties. It is exported for use as a field accessor with safe.Field
// by external callers (e.g. admission code) that need to navigate into the
// Version subtree.
func ToNodePoolPropertiesVersion(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *coreapi.NodePoolVersionProfile {
	return &oldObj.Version
}

// ToNodePoolPropertiesPlatform returns a pointer to the Platform field of node
// pool properties. It is exported for use as a field accessor with safe.Field
// by external callers (e.g. admission code) that need to navigate into the
// Platform subtree.
func ToNodePoolPropertiesPlatform(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *coreapi.NodePoolPlatformProfile {
	return &oldObj.Platform
}

func toNodePoolPropertiesReplicas(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *int32 {
	return &oldObj.Replicas
}

func toNodePoolPropertiesAutoRepair(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *bool {
	return &oldObj.AutoRepair
}

func toNodePoolPropertiesAutoScaling(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *coreapi.NodePoolAutoScaling {
	return oldObj.AutoScaling
}

func toNodePoolPropertiesLabels(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) map[string]string {
	return oldObj.Labels
}

func toNodePoolPropertiesTaints(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) []coreapi.Taint {
	return oldObj.Taints
}

func toNodePoolPropertiesNodeDrainTimeoutMinutes(oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) *int32 {
	return oldObj.NodeDrainTimeoutMinutes
}

func validateNodePoolProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterNodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	//ProvisioningState coreapi.ProvisioningState       `json:"provisioningState"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("provisioningState"), &newObj.ProvisioningState, safe.Field(oldObj, toNodePoolPropertiesProvisioningState))...)

	//Version                 NodePoolVersionProfile  `json:"version,omitempty"`
	errs = append(errs, validateNodePoolVersionProfile(ctx, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, ToNodePoolPropertiesVersion))...)

	//Platform                NodePoolPlatformProfile `json:"platform,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, ToNodePoolPropertiesPlatform))...)
	errs = append(errs, validateNodePoolPlatformProfile(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, ToNodePoolPropertiesPlatform))...)

	//Replicas                int32                   `json:"replicas,omitempty"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("replicas"), &newObj.Replicas, safe.Field(oldObj, toNodePoolPropertiesReplicas), 0)...)
	// Validate max=200 only when availabilityZone is unset. When availabilityZone is set, no maximum limit applies.
	errs = append(errs, MaximumIfNoAZ(ctx, op, fldPath.Child("replicas"), &newObj.Replicas, safe.Field(oldObj, toNodePoolPropertiesReplicas), MaxNodePoolNodes, newObj.Platform.AvailabilityZone)...)

	if newObj.AutoScaling != nil && newObj.Replicas > 0 {
		errs = append(errs, field.Invalid(fldPath.Child("replicas"), &newObj.AutoScaling.Min, "cannot specify replicas when autoScaling is enabled"))
	}

	//AutoRepair              bool                    `json:"autoRepair,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("autoRepair"), &newObj.AutoRepair, safe.Field(oldObj, toNodePoolPropertiesAutoRepair))...)

	// Ephemeral OS disks require autoRepair=true. Both fields are immutable,
	// so this constraint only fires on CREATE (harmless on UPDATE due to immutability).
	if newObj.Platform.OSDisk.DiskType == metadataapi.OsDiskTypeEphemeral && !newObj.AutoRepair {
		errs = append(errs, field.Invalid(
			fldPath.Child("autoRepair"),
			newObj.AutoRepair,
			"must be true when platform.osDisk.diskType is Ephemeral (nodes with ephemeral disks lose data on host events)",
		))
	}

	//AutoScaling             *NodePoolAutoScaling    `json:"autoScaling,omitempty"`
	errs = append(errs, validateNodePoolAutoScaling(ctx, op, fldPath.Child("autoScaling"), newObj.AutoScaling, safe.Field(oldObj, toNodePoolPropertiesAutoScaling), newObj.Platform.AvailabilityZone)...)

	//Labels                  map[string]string       `json:"labels,omitempty"`
	errs = append(errs, validate.EachMapKey(ctx, op, fldPath.Child("labels"),
		newObj.Labels, safe.Field(oldObj, toNodePoolPropertiesLabels),
		KubeQualifiedName,
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("labels"),
		newObj.Labels, safe.Field(oldObj, toNodePoolPropertiesLabels),
		nil,
		KubeLabelValue,
	)...)
	errs = append(errs, validateNodePoolForbiddenLabels(fldPath.Child("labels"), newObj.Labels)...)

	//Taints                  []Taint                 `json:"taints,omitempty"`
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("taints"),
		newObj.Taints, safe.Field(oldObj, toNodePoolPropertiesTaints),
		nil, nil,
		validateTaint,
	)...)

	//NodeDrainTimeoutMinutes *int32                  `json:"nodeDrainTimeoutMinutes,omitempty"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("nodeDrainTimeoutMinutes"), newObj.NodeDrainTimeoutMinutes, safe.Field(oldObj, toNodePoolPropertiesNodeDrainTimeoutMinutes), 0)...)
	errs = append(errs, Maximum(ctx, op, fldPath.Child("nodeDrainTimeoutMinutes"), newObj.NodeDrainTimeoutMinutes, safe.Field(oldObj, toNodePoolPropertiesNodeDrainTimeoutMinutes), 10080)...)

	return errs
}

func validateNodePoolForbiddenLabels(fldPath *field.Path, newLabels map[string]string) field.ErrorList {
	if len(newLabels) == 0 {
		return nil
	}

	errs := field.ErrorList{}
	for key, value := range newLabels {
		keyPath := fldPath.Key(key)

		forbiddenValues, restricted := nodePoolForbiddenK8sLabelValuesByKey[key]
		if !restricted {
			continue
		}

		if forbiddenValues == nil {
			errs = append(errs, field.Invalid(keyPath, key, "label key is not allowed on node pools"))
			continue
		}

		if _, forbidden := forbiddenValues[value]; !forbidden {
			continue
		}
		errs = append(errs, field.Invalid(keyPath, value, "label value is not allowed for this label key"))
	}

	return errs
}

var (
	toNodePoolServiceProviderClusterServiceID = func(oldObj *coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties) *metadataapi.InternalID {
		return oldObj.ClusterServiceID
	}
)

func validateNodePoolServiceProviderProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	//ClusterServiceID  *InternalID                     `json:"clusterServiceID,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("clusterServiceID"), newObj.ClusterServiceID, safe.Field(oldObj, toNodePoolServiceProviderClusterServiceID))...)

	return errs
}

var (
	toNodePoolVersionProfileID           = func(oldObj *coreapi.NodePoolVersionProfile) *string { return &oldObj.ID }
	toNodePoolVersionProfileChannelGroup = func(oldObj *coreapi.NodePoolVersionProfile) *string { return &oldObj.ChannelGroup }
)

func validateNodePoolVersionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolVersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	// Version ID is required since 20251223preview version but some records may not have had it originally, so don't fail them yet.
	//ID           string `json:"id,omitempty"`
	if oldObj == nil || len(oldObj.ID) > 0 {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toNodePoolVersionProfileID))...)
	}

	// Skip version ID validation if version hasn't changed
	if oldObj == nil || newObj.ID != oldObj.ID {
		errs = append(errs, validateNodePoolVersionID(ctx, op, fldPath, newObj, oldObj)...)
	}

	//ChannelGroup string `json:"channelGroup,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, safe.Field(oldObj, toNodePoolVersionProfileChannelGroup))...)

	if !op.HasOption(metadataapi.FeatureExperimentalReleaseFeatures) {
		errs = append(errs, validate.Enum(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, safe.Field(oldObj, toNodePoolVersionProfileChannelGroup), metadataapi.AllowedChannelGroups, nil)...)
	} else {
		errs = append(errs, validate.Enum(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, safe.Field(oldObj, toNodePoolVersionProfileChannelGroup), metadataapi.AllowedChannelGroupsWithExperimentalFlag, nil)...)
	}

	return errs
}

// validateNodePoolVersionID validates the version ID format and checks it against the minimum version requirement.
func validateNodePoolVersionID(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolVersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, OpenShiftWithOptionalPrerelease(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toNodePoolVersionProfileID))...)
	if len(errs) == 0 {
		// Only validate if version is valid, otherwise we'll get a duplicate invalid version error
		errs = append(errs, VersionMustBeAtLeast(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toNodePoolVersionProfileID), "4.20.8")...)
	}
	return errs
}

var (
	toNodePoolPlatformProfileSubnetID               = func(oldObj *coreapi.NodePoolPlatformProfile) *azcorearm.ResourceID { return oldObj.SubnetID }
	toNodePoolPlatformProfileVMSize                 = func(oldObj *coreapi.NodePoolPlatformProfile) *string { return &oldObj.VMSize }
	toNodePoolPlatformProfileEnableEncryptionAtHost = func(oldObj *coreapi.NodePoolPlatformProfile) *bool { return &oldObj.EnableEncryptionAtHost }
	toNodePoolPlatformProfileOSDisk                 = func(oldObj *coreapi.NodePoolPlatformProfile) *coreapi.OSDiskProfile { return &oldObj.OSDisk }
	toNodePoolPlatformProfileAvailabilityZone       = func(oldObj *coreapi.NodePoolPlatformProfile) *string { return &oldObj.AvailabilityZone }
)

func validateNodePoolPlatformProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	//SubnetID               string        `json:"subnetId,omitempty"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("subnetId"), newObj.SubnetID, safe.Field(oldObj, toNodePoolPlatformProfileSubnetID))...)
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("subnetId"), newObj.SubnetID, safe.Field(oldObj, toNodePoolPlatformProfileSubnetID), "Microsoft.Network/virtualNetworks/subnets")...)

	//VMSize                 string        `json:"vmSize,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("vmSize"), &newObj.VMSize, safe.Field(oldObj, toNodePoolPlatformProfileVMSize))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("vmSize"), &newObj.VMSize, safe.Field(oldObj, toNodePoolPlatformProfileVMSize))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("vmSize"), &newObj.VMSize, safe.Field(oldObj, toNodePoolPlatformProfileVMSize), EnabledNodePoolAzureVMSizes(), nil)...)

	//EnableEncryptionAtHost bool          `json:"enableEncryptionAtHost"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("enableEncryptionAtHost"), &newObj.EnableEncryptionAtHost, safe.Field(oldObj, toNodePoolPlatformProfileEnableEncryptionAtHost))...)

	//OSDisk                 OSDiskProfile `json:"osDisk"`
	errs = append(errs, immutableByReflect(ctx, op, fldPath.Child("osDisk"), &newObj.OSDisk, safe.Field(oldObj, toNodePoolPlatformProfileOSDisk))...)
	errs = append(errs, validateOSDiskProfile(ctx, op, fldPath.Child("osDisk"), &newObj.OSDisk, safe.Field(oldObj, toNodePoolPlatformProfileOSDisk))...)

	//AvailabilityZone       string        `json:"availabilityZone,omitempty"`
	errs = append(errs, immutableByCompare(ctx, op, fldPath.Child("availabilityZone"), &newObj.AvailabilityZone, safe.Field(oldObj, toNodePoolPlatformProfileAvailabilityZone))...)

	return errs
}

var (
	toOSDiskProfileSizeGiB                = func(oldObj *coreapi.OSDiskProfile) *int32 { return oldObj.SizeGiB }
	toOSDiskProfileDiskStorageAccountType = func(oldObj *coreapi.OSDiskProfile) *metadataapi.DiskStorageAccountType {
		return &oldObj.DiskStorageAccountType
	}
	toOSDiskProfileEncryptionSetID = func(oldObj *coreapi.OSDiskProfile) *azcorearm.ResourceID { return oldObj.EncryptionSetID }
	toOSDiskProfileDiskType        = func(oldObj *coreapi.OSDiskProfile) *metadataapi.OsDiskType { return &oldObj.DiskType }
)

func validateOSDiskProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.OSDiskProfile) field.ErrorList {
	errs := field.ErrorList{}

	//SizeGiB                *int32                 `json:"sizeGiB,omitempty"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("sizeGiB"), newObj.SizeGiB, safe.Field(oldObj, toOSDiskProfileSizeGiB), 64)...)

	//DiskStorageAccountType DiskStorageAccountType `json:"diskStorageAccountType,omitempty"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("diskStorageAccountType"), &newObj.DiskStorageAccountType, safe.Field(oldObj, toOSDiskProfileDiskStorageAccountType), metadataapi.ValidDiskStorageAccountTypes, nil)...)

	//DiskType               OsDiskType             `json:"diskType"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("diskType"), &newObj.DiskType, safe.Field(oldObj, toOSDiskProfileDiskType))...)
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("diskType"), &newObj.DiskType, safe.Field(oldObj, toOSDiskProfileDiskType), metadataapi.ValidOsDiskTypes, nil)...)

	switch newObj.DiskType {
	case metadataapi.OsDiskTypeManaged:
		errs = append(errs, Maximum(ctx, op, fldPath.Child("sizeGiB"), newObj.SizeGiB, safe.Field(oldObj, toOSDiskProfileSizeGiB), MaxManagedOSDiskSizeGiB)...)
	case metadataapi.OsDiskTypeEphemeral:
		errs = append(errs, Maximum(ctx, op, fldPath.Child("sizeGiB"), newObj.SizeGiB, safe.Field(oldObj, toOSDiskProfileSizeGiB), MaxEphemeralOSDiskSizeGiB)...)
	}

	//EncryptionSetID        string                 `json:"encryptionSetId,omitempty"`
	errs = append(errs, RestrictedResourceIDWithResourceGroup(ctx, op, fldPath.Child("encryptionSetId"), newObj.EncryptionSetID, safe.Field(oldObj, toOSDiskProfileEncryptionSetID), "Microsoft.Compute/diskEncryptionSets")...)
	if newObj.EncryptionSetID != nil {
		errs = append(errs, MaxLen(ctx, op, fldPath.Child("encryptionSetId"), &newObj.EncryptionSetID.Name, nil, MaxDiskEncryptionSetNameLen)...)
		errs = append(errs, MatchesRegex(ctx, op, fldPath.Child("encryptionSetId"), &newObj.EncryptionSetID.Name, nil, diskEncryptionSetNameRegex, diskEncryptionSetNameErrorString)...)
	}

	return errs
}

var (
	toNodePoolAutoScalingMin = func(oldObj *coreapi.NodePoolAutoScaling) *int32 { return &oldObj.Min }
	toNodePoolAutoScalingMax = func(oldObj *coreapi.NodePoolAutoScaling) *int32 { return &oldObj.Max }
)

func validateNodePoolAutoScaling(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.NodePoolAutoScaling, availabilityZone string) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//Min int32 `json:"min,omitempty"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("min"), &newObj.Min, safe.Field(oldObj, toNodePoolAutoScalingMin), 0)...)
	errs = append(errs, MaximumIfNoAZ(ctx, op, fldPath.Child("min"), &newObj.Min, safe.Field(oldObj, toNodePoolAutoScalingMin), MaxNodePoolNodes, availabilityZone)...)

	//Max int32 `json:"max,omitempty"`
	errs = append(errs, MaximumIfNoAZ(ctx, op, fldPath.Child("max"), &newObj.Max, safe.Field(oldObj, toNodePoolAutoScalingMax), MaxNodePoolNodes, availabilityZone)...)

	// Validate max >= min only if previous validations passed (i.e., min and max are both valid)
	if len(errs) == 0 {
		errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("max"), &newObj.Max, safe.Field(oldObj, toNodePoolAutoScalingMax), newObj.Min)...)
	}

	return errs
}

func validateTaint(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *coreapi.Taint) field.ErrorList {
	errs := field.ErrorList{}

	//Effect Effect `json:"effect,omitempty"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("effect"), &newObj.Effect, nil, metadataapi.ValidEffects, nil)...)

	//Key    string `json:"key,omitempty"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("key"), &newObj.Key, nil)...)
	errs = append(errs, KubeQualifiedName(ctx, op, fldPath.Child("key"), &newObj.Key, nil)...)

	//Value  string `json:"value,omitempty"`
	errs = append(errs, KubeLabelValue(ctx, op, fldPath.Child("value"), &newObj.Value, nil)...)

	return errs
}
