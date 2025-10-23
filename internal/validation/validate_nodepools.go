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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func ValidateNodePoolCreate(ctx context.Context, newObj *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return validateNodePool(ctx, op, newObj, nil)
}

func ValidateNodePoolUpdate(ctx context.Context, newObj, oldObj *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return validateNodePool(ctx, op, newObj, oldObj)
}

var (
	toNodePoolTrackedResource = func(oldObj *api.HCPOpenShiftClusterNodePool) *arm.TrackedResource { return &oldObj.TrackedResource }
	toNodePoolProperties      = func(oldObj *api.HCPOpenShiftClusterNodePool) *api.HCPOpenShiftClusterNodePoolProperties {
		return &oldObj.Properties
	}
	toNodePoolServiceProviderProperties = func(oldObj *api.HCPOpenShiftClusterNodePool) *api.HCPOpenShiftClusterNodePoolServiceProviderProperties {
		return &oldObj.ServiceProviderProperties
	}
)

func validateNodePool(ctx context.Context, op operation.Operation, newObj, oldObj *api.HCPOpenShiftClusterNodePool) field.ErrorList {
	errs := field.ErrorList{}

	//arm.ProxyResource
	errs = append(errs, validateTrackedResource(ctx, op, field.NewPath("trackedResource"), &newObj.TrackedResource, safe.Field(oldObj, toNodePoolTrackedResource))...)

	//Properties HCPOpenShiftClusterNodePoolProperties `json:"properties" validate:"required"`
	errs = append(errs, validateNodePoolProperties(ctx, op, field.NewPath("properties"), &newObj.Properties, safe.Field(oldObj, toNodePoolProperties))...)

	//ServiceProviderProperties HCPOpenShiftClusterNodePoolServiceProviderProperties `json:"serviceProviderProperties,omitempty" validate:"required"`
	errs = append(errs, validateNodePoolServiceProviderProperties(ctx, op, field.NewPath("serviceProviderProperties"), &newObj.ServiceProviderProperties, safe.Field(oldObj, toNodePoolServiceProviderProperties))...)

	return errs
}

var (
	toNodePoolPropertiesProvisioningState = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) *arm.ProvisioningState {
		return &oldObj.ProvisioningState
	}
	toNodePoolPropertiesVersion = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) *api.NodePoolVersionProfile {
		return &oldObj.Version
	}
	toNodePoolPropertiesPlatform = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) *api.NodePoolPlatformProfile {
		return &oldObj.Platform
	}
	toNodePoolPropertiesReplicas    = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) *int32 { return &oldObj.Replicas }
	toNodePoolPropertiesAutoRepair  = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) *bool { return &oldObj.AutoRepair }
	toNodePoolPropertiesAutoScaling = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) *api.NodePoolAutoScaling {
		return oldObj.AutoScaling
	}
	toNodePoolPropertiesLabels = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) map[string]string { return oldObj.Labels }
	toNodePoolPropertiesTaints = func(oldObj *api.HCPOpenShiftClusterNodePoolProperties) []api.Taint { return oldObj.Taints }
)

func validateNodePoolProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterNodePoolProperties) field.ErrorList {
	errs := field.ErrorList{}

	//ProvisioningState arm.ProvisioningState       `json:"provisioningState"       visibility:"read"                     validate:"omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("provisioningState"), &newObj.ProvisioningState, safe.Field(oldObj, toNodePoolPropertiesProvisioningState))...)

	//Version                 NodePoolVersionProfile  `json:"version,omitempty"`
	errs = append(errs, validateNodePoolVersionProfile(ctx, op, fldPath.Child("version"), &newObj.Version, safe.Field(oldObj, toNodePoolPropertiesVersion))...)

	//Platform                NodePoolPlatformProfile `json:"platform,omitempty"                visibility:"read create"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toNodePoolPropertiesPlatform))...)
	errs = append(errs, validateNodePoolPlatformProfile(ctx, op, fldPath.Child("platform"), &newObj.Platform, safe.Field(oldObj, toNodePoolPropertiesPlatform))...)

	//Replicas                int32                   `json:"replicas,omitempty"                visibility:"read create update" validate:"min=0,excluded_with=AutoScaling"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("replicas"), &newObj.Replicas, safe.Field(oldObj, toNodePoolPropertiesReplicas), 0)...)
	if newObj.AutoScaling != nil {
		errs = append(errs, EQ(ctx, op, fldPath.Child("replicas"), &newObj.Replicas, safe.Field(oldObj, toNodePoolPropertiesReplicas), 0)...)
	}

	//AutoRepair              bool                    `json:"autoRepair,omitempty"              visibility:"read create"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("autoRepair"), &newObj.AutoRepair, safe.Field(oldObj, toNodePoolPropertiesAutoRepair))...)

	//AutoScaling             *NodePoolAutoScaling    `json:"autoScaling,omitempty"             visibility:"read create update"`
	errs = append(errs, validateNodePoolAutoScaling(ctx, op, fldPath.Child("autoScaling"), newObj.AutoScaling, safe.Field(oldObj, toNodePoolPropertiesAutoScaling))...)

	//Labels                  map[string]string       `json:"labels,omitempty"                  visibility:"read create update" validate:"dive,keys,k8s_qualified_name,endkeys,k8s_label_value"`
	errs = append(errs, validate.EachMapKey(ctx, op, fldPath.Child("labels"),
		newObj.Labels, safe.Field(oldObj, toNodePoolPropertiesLabels),
		KubeQualifiedName,
	)...)
	errs = append(errs, validate.EachMapVal(ctx, op, fldPath.Child("labels"),
		newObj.Labels, safe.Field(oldObj, toNodePoolPropertiesLabels),
		nil,
		KubeLabelValue,
	)...)

	//Taints                  []Taint                 `json:"taints,omitempty"                  visibility:"read create update" validate:"dive"`
	errs = append(errs, validate.EachSliceVal(
		ctx, op, fldPath.Child("taints"),
		newObj.Taints, safe.Field(oldObj, toNodePoolPropertiesTaints),
		nil, nil,
		validateTaint,
	)...)

	//NodeDrainTimeoutMinutes *int32                  `json:"nodeDrainTimeoutMinutes,omitempty" visibility:"read create update"`
	// TODO why do we allow this to be negative?

	return errs
}

var (
	toNodePoolServiceProviderCosmosUID = func(oldObj *api.HCPOpenShiftClusterNodePoolServiceProviderProperties) *string {
		return &oldObj.CosmosUID
	}
	toNodePoolServiceProviderClusterServiceID = func(oldObj *api.HCPOpenShiftClusterNodePoolServiceProviderProperties) *api.InternalID {
		return &oldObj.ClusterServiceID
	}
)

func validateNodePoolServiceProviderProperties(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.HCPOpenShiftClusterNodePoolServiceProviderProperties) field.ErrorList {
	errs := field.ErrorList{}

	//CosmosUID         string                         `json:"cosmosUID,omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("cosmosUID"), &newObj.CosmosUID, safe.Field(oldObj, toNodePoolServiceProviderCosmosUID))...)
	if oldObj == nil { // must be unset on creation because we don't know it yet.
		errs = append(errs, validate.ForbiddenValue(ctx, op, fldPath.Child("cosmosUID"), &newObj.CosmosUID, nil)...)
	}

	//ClusterServiceID  InternalID                     `json:"clusterServiceID,omitempty"                visibility:"read"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("clusterServiceID"), &newObj.ClusterServiceID, safe.Field(oldObj, toNodePoolServiceProviderClusterServiceID))...)

	return errs
}

var (
	toNodePoolVersionProfileID           = func(oldObj *api.NodePoolVersionProfile) *string { return &oldObj.ID }
	toNodePoolVersionProfileChannelGroup = func(oldObj *api.NodePoolVersionProfile) *string { return &oldObj.ChannelGroup }
)

func validateNodePoolVersionProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolVersionProfile) field.ErrorList {
	errs := field.ErrorList{}

	//ID           string `json:"id,omitempty"           validate:"required_unless=ChannelGroup stable,omitempty,openshift_version"`
	if newObj.ChannelGroup != "stable" {
		errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toNodePoolVersionProfileID))...)
	}
	errs = append(errs, OpenshiftVersionWithOptionalMicro(ctx, op, fldPath.Child("id"), &newObj.ID, safe.Field(oldObj, toNodePoolVersionProfileID))...)

	//ChannelGroup string `json:"channelGroup,omitempty"`
	// this is required and is later checked for matching the control plane.
	// TODO   Interestingly, they won't match long term since clusters can change channels and aren't check
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("channelGroup"), &newObj.ChannelGroup, safe.Field(oldObj, toNodePoolVersionProfileChannelGroup))...)

	return errs
}

var (
	toNodePoolPlatformProfileSubnetID               = func(oldObj *api.NodePoolPlatformProfile) *string { return &oldObj.SubnetID }
	toNodePoolPlatformProfileVMSize                 = func(oldObj *api.NodePoolPlatformProfile) *string { return &oldObj.VMSize }
	toNodePoolPlatformProfileEnableEncryptionAtHost = func(oldObj *api.NodePoolPlatformProfile) *bool { return &oldObj.EnableEncryptionAtHost }
	toNodePoolPlatformProfileOSDisk                 = func(oldObj *api.NodePoolPlatformProfile) *api.OSDiskProfile { return &oldObj.OSDisk }
	toNodePoolPlatformProfileAvailabilityZone       = func(oldObj *api.NodePoolPlatformProfile) *string { return &oldObj.AvailabilityZone }
)

func validateNodePoolPlatformProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolPlatformProfile) field.ErrorList {
	errs := field.ErrorList{}

	//SubnetID               string        `json:"subnetId,omitempty"         validate:"omitempty,resource_id=Microsoft.Network/virtualNetworks/subnets"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("subnetId"), &newObj.SubnetID, safe.Field(oldObj, toNodePoolPlatformProfileSubnetID))...)
	errs = append(errs, RestrictedResourceID(ctx, op, fldPath.Child("subnetId"), &newObj.SubnetID, safe.Field(oldObj, toNodePoolPlatformProfileSubnetID), "Microsoft.Network/virtualNetworks/subnets")...)

	//VMSize                 string        `json:"vmSize,omitempty"           validate:"required"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("vmSize"), &newObj.VMSize, safe.Field(oldObj, toNodePoolPlatformProfileVMSize))...)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("vmSize"), &newObj.VMSize, safe.Field(oldObj, toNodePoolPlatformProfileVMSize))...)

	//EnableEncryptionAtHost bool          `json:"enableEncryptionAtHost"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("enableEncryptionAtHost"), &newObj.EnableEncryptionAtHost, safe.Field(oldObj, toNodePoolPlatformProfileEnableEncryptionAtHost))...)

	//OSDisk                 OSDiskProfile `json:"osDisk"`
	errs = append(errs, validate.ImmutableByReflect(ctx, op, fldPath.Child("osDisk"), &newObj.OSDisk, safe.Field(oldObj, toNodePoolPlatformProfileOSDisk))...)
	errs = append(errs, validateOSDiskProfile(ctx, op, fldPath.Child("osDisk"), &newObj.OSDisk, safe.Field(oldObj, toNodePoolPlatformProfileOSDisk))...)

	//AvailabilityZone       string        `json:"availabilityZone,omitempty"`
	errs = append(errs, validate.ImmutableByCompare(ctx, op, fldPath.Child("availabilityZone"), &newObj.AvailabilityZone, safe.Field(oldObj, toNodePoolPlatformProfileAvailabilityZone))...)

	return errs
}

var (
	toOSDiskProfileSizeGiB                = func(oldObj *api.OSDiskProfile) *int32 { return &oldObj.SizeGiB }
	toOSDiskProfileDiskStorageAccountType = func(oldObj *api.OSDiskProfile) *api.DiskStorageAccountType { return &oldObj.DiskStorageAccountType }
	toOSDiskProfileEncryptionSetID        = func(oldObj *api.OSDiskProfile) *string { return &oldObj.EncryptionSetID }
)

func validateOSDiskProfile(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.OSDiskProfile) field.ErrorList {
	errs := field.ErrorList{}

	//SizeGiB                int32                  `json:"sizeGiB,omitempty"                validate:"min=1"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("sizeGiB"), &newObj.SizeGiB, safe.Field(oldObj, toOSDiskProfileSizeGiB), 1)...)

	//DiskStorageAccountType DiskStorageAccountType `json:"diskStorageAccountType,omitempty" validate:"enum_diskstorageaccounttype"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("diskStorageAccountType"), &newObj.DiskStorageAccountType, safe.Field(oldObj, toOSDiskProfileDiskStorageAccountType), api.ValidDiskStorageAccountTypes)...)

	//EncryptionSetID        string                 `json:"encryptionSetId,omitempty"        validate:"omitempty,resource_id=Microsoft.Compute/diskEncryptionSets"`
	errs = append(errs, RestrictedResourceID(ctx, op, fldPath.Child("encryptionSetId"), &newObj.EncryptionSetID, safe.Field(oldObj, toOSDiskProfileEncryptionSetID), "Microsoft.Compute/diskEncryptionSets")...)

	return errs
}

var (
	toNodePoolAutoScalingMin = func(oldObj *api.NodePoolAutoScaling) *int32 { return &oldObj.Min }
	toNodePoolAutoScalingMax = func(oldObj *api.NodePoolAutoScaling) *int32 { return &oldObj.Max }
)

func validateNodePoolAutoScaling(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.NodePoolAutoScaling) field.ErrorList {
	if newObj == nil {
		return nil
	}

	errs := field.ErrorList{}

	//Min int32 `json:"min,omitempty" validate:"min=1"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("min"), &newObj.Min, safe.Field(oldObj, toNodePoolAutoScalingMin), 1)...)

	//Max int32 `json:"max,omitempty" validate:"gtefield=Min"`
	errs = append(errs, validate.Minimum(ctx, op, fldPath.Child("max"), &newObj.Max, safe.Field(oldObj, toNodePoolAutoScalingMax), newObj.Min)...)

	return errs
}

func validateTaint(ctx context.Context, op operation.Operation, fldPath *field.Path, newObj, oldObj *api.Taint) field.ErrorList {
	errs := field.ErrorList{}

	//Effect Effect `json:"effect,omitempty" validate:"required,enum_effect"`
	errs = append(errs, validate.Enum(ctx, op, fldPath.Child("effect"), &newObj.Effect, nil, api.ValidEffects)...)

	//Key    string `json:"key,omitempty"    validate:"required,k8s_qualified_name"`
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("key"), &newObj.Key, nil)...)
	errs = append(errs, KubeQualifiedName(ctx, op, fldPath.Child("key"), &newObj.Key, nil)...)

	//Value  string `json:"value,omitempty"  validate:"k8s_label_value"`
	errs = append(errs, KubeLabelValue(ctx, op, fldPath.Child("value"), &newObj.Value, nil)...)

	return errs
}
